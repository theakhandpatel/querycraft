package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/ai/jobs"
	"github.com/querycraft/sqlquery/backend/internal/metrics"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* The M4 generation surface.
 *
 * The service is stored as configuration, not as a built Service, because the
 * owner of a generated challenge comes from the authenticated session and
 * nothing else. Each request builds a service whose Depot stamps that owner,
 * which is cheap and keeps the "a caller can never name the user it acts for"
 * rule that the rest of the gateway follows.
 *
 * A nil configuration means the lab is off. Nothing here invents a result in
 * that case: /capabilities reports why, and the generation routes answer with
 * the same honest `unavailable` envelope a configured-but-broken provider
 * would produce. */

// UseAI attaches the generation configuration. It must be called before
// Handler() serves requests.
func (s *Server) UseAI(cfg ai.Config) {
	s.setAIConfig(cfg)
	// The registry is built from the same configuration, so the number of
	// generations a gateway will run at once is the capacity it advertises
	// rather than a second, private guess. Its services are built per owner on
	// demand, for the same reason a request builds one: the owner of a job
	// comes from the session and never from the body.
	s.jobs = jobs.NewJobs(s.aiServiceFor, cfg.Limits)
}

// setAIConfig installs one AI configuration. It is the writer behind
// SetAIConfig and UseAI.
func (s *Server) setAIConfig(cfg ai.Config) {
	s.aiMu.Lock()
	defer s.aiMu.Unlock()
	s.aiCfg = &cfg
}

// SetAIConfig swaps the generation configuration while the gateway runs: the
// boot path applies the administrator's persisted AI setting over the config
// file's provider, and PUT /admin/ai applies what the manage surface sends.
// Requests and jobs submitted after the swap build their services from the new
// configuration; a job already running keeps the provider it started with,
// which is the honest semantics — a run half-completed by one provider is not
// handed to another.
func (s *Server) SetAIConfig(cfg ai.Config) {
	s.setAIConfig(cfg)
}

// currentAI returns the live configuration and whether one was attached.
func (s *Server) currentAI() (ai.Config, bool) {
	if s == nil {
		return ai.Config{}, false
	}
	s.aiMu.RLock()
	defer s.aiMu.RUnlock()
	if s.aiCfg == nil {
		return ai.Config{}, false
	}
	return *s.aiCfg, true
}

// DrainJobs waits for running generation jobs to finish, or for the context to
// end. It is the shutdown path: a job detaches from the request that submitted
// it, so the server closing does not stop it, and the store it writes to closes
// when run returns.
func (s *Server) DrainJobs(ctx context.Context) error {
	if s == nil || s.jobs == nil {
		return nil
	}
	return s.jobs.Drain(ctx)
}

// aiConfigured reports whether a generation service was attached at all.
func (s *Server) aiConfigured() bool {
	_, ok := s.currentAI()
	return ok
}

// aiAvailability is what /capabilities advertises. It never fails: a missing
// service is a capability answer, not an error.
func (s *Server) aiAvailability() ai.Availability {
	if !s.aiConfigured() {
		// The run budget belongs to the pipeline, not to the provider, so a
		// gateway with no generation service attached still advertises it. A
		// client that cached the budget keeps a sane deadline across a gateway
		// that happens to be AI-off right now, instead of falling back on its
		// own guess.
		av := ai.NewService(ai.Config{}).Available()
		av.Reason = "AI is off: no generation service is configured."
		return av
	}
	cfg, ok := s.currentAI()
	if !ok {
		av := ai.NewService(ai.Config{}).Available()
		av.Reason = "AI is off: no generation service is configured."
		return av
	}
	return ai.NewService(cfg).Available()
}

// aiServiceFor returns a service whose Depot is scoped to the caller. It
// returns nil when no configuration is attached.
func (s *Server) aiServiceFor(ownerID string) *ai.Service {
	if !s.aiConfigured() {
		return nil
	}
	cfg, _ := s.currentAI()
	if cfg.Depot != nil {
		cfg.Depot = ownerScopedDepot{Depot: cfg.Depot, owner: ownerID}
	}
	return ai.NewService(cfg)
}

// ownerScopedDepot records who asked for a challenge without letting the
// request name that owner itself.
type ownerScopedDepot struct {
	schema.Depot
	owner string
}

func (d ownerScopedDepot) SaveChallenge(ctx context.Context, rec schema.Record) error {
	rec.OwnerID = d.owner
	return d.Depot.SaveChallenge(ctx, rec)
}

func (d ownerScopedDepot) SaveCourse(ctx context.Context, rec schema.CourseRecord) error {
	rec.OwnerID = d.owner
	return d.Depot.SaveCourse(ctx, rec)
}

// SaveRun stamps the transcript with the session that asked for the run, for the
// same reason SaveChallenge and SaveCourse do: the owner of a record comes from
// the session and never from the caller. A run's own id travels unchanged, so a
// job id a client polled names the transcript it can later audit.
func (d ownerScopedDepot) SaveRun(ctx context.Context, rec schema.RunTranscript) error {
	rec.OwnerID = d.owner
	return d.Depot.SaveRun(ctx, rec)
}

// The list reads are filtered to the session's owner by the store, so a list
// route never loads every learner's records and discards them in the handler.
// Overriding the *For methods here is what makes the service's own calls
// owner-scoped without the service needing to know the caller.
func (d ownerScopedDepot) ChallengesFor(ctx context.Context, _ string) ([]schema.Record, error) {
	return d.Depot.ChallengesFor(ctx, d.owner)
}
func (d ownerScopedDepot) CoursesFor(ctx context.Context, _ string) ([]schema.CourseRecord, error) {
	return d.Depot.CoursesFor(ctx, d.owner)
}
func (d ownerScopedDepot) RunsFor(ctx context.Context, _ string) ([]schema.RunSummary, error) {
	return d.Depot.RunsFor(ctx, d.owner)
}

/* ---------- generation ---------- */

// handleGenerateChallenge implements POST /challenges/generate.
func (s *Server) handleGenerateChallenge(w http.ResponseWriter, r *http.Request) {
	s.runGeneration(w, r, "challenge")
}

// handleGenerateCourse implements POST /courses/generate.
func (s *Server) handleGenerateCourse(w http.ResponseWriter, r *http.Request) {
	s.runGeneration(w, r, "course")
}

func (s *Server) runGeneration(w http.ResponseWriter, r *http.Request, kind string) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req schema.GenerationRequest
	if !readJSON(w, r, &req) {
		return
	}
	// The route decides the kind; a body that disagrees is not an error, it is
	// simply ignored, so one contract serves both endpoints.
	req.Kind = kind

	svc := s.aiServiceFor(u.ID)
	if svc == nil {
		out := unavailableOutcome(kind, "AI is off: no generation service is configured.")
		s.recordGeneration(out)
		writeJSON(w, http.StatusServiceUnavailable, out)
		return
	}

	// A caller that asks for a job gets an id immediately; the run continues on
	// the registry's own context. The blocking path below is the default
	// because it is the one a script can read in a single request, and because
	// every audit that drives generation reads the envelope directly.
	if s.jobs != nil && asyncRequested(r) {
		s.submitGenerationJob(w, r, u.ID, kind, req)
		return
	}

	// Generation runs real SQL in a throwaway environment, so it spends the
	// same statement budget as a query. A gateway at capacity refuses rather
	// than queueing a second generation behind the first.
	// The pipeline owns its own deadline; the transport gives it headroom so a
	// pipeline that stops on time reports its own timeout rather than being
	// cut off by the request context.
	cfg, _ := s.currentAI()
	timeout := cfg.Limits.Merge(ai.DefaultLimits()).Timeout + 5*time.Second
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	release, admitted := s.enter(ctx)
	if !admitted {
		out := unavailableOutcome(kind, "The gateway is at its concurrency limit; try again shortly.")
		out.Error.Code = ai.CodeEnvironmentDown
		s.recordGeneration(out)
		writeJSON(w, http.StatusServiceUnavailable, out)
		return
	}
	defer release()

	var out schema.Outcome
	if kind == "course" {
		out = svc.GenerateCourse(ctx, req)
	} else {
		out = svc.GenerateChallenge(ctx, req)
	}
	out.Kind = kind
	s.recordGeneration(out)
	writeJSON(w, statusForOutcome(out), out)
}

// asyncRequested reports whether the caller wants a job rather than an answer.
// `Prefer: respond-async` is the HTTP spelling of the same request and is what
// a client written against the standard would send; `async=1` is here because a
// query parameter is what a person reaches for with curl.
func asyncRequested(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("async"))) {
	case "1", "true":
		return true
	}
	for _, pref := range strings.Split(r.Header.Get("Prefer"), ",") {
		if strings.EqualFold(strings.TrimSpace(pref), "respond-async") {
			return true
		}
	}
	return false
}

// submitGenerationJob answers POST /courses/generate?async=1 with the id to
// poll. The registry refuses a submission it cannot start — AI is off, the
// gateway is already at its generation limit — and that refusal is reported in
// the same envelope the blocking route uses, so a client has one shape to read
// either way.
func (s *Server) submitGenerationJob(w http.ResponseWriter, r *http.Request, ownerID, kind string, req schema.GenerationRequest) {
	job, err := s.jobs.Submit(r.Context(), jobs.Submission{
		OwnerID:  ownerID,
		Kind:     kind,
		Request:  req,
		OnFinish: s.recordJob,
	})
	if err != nil {
		var aiErr *schema.Error
		if errors.As(err, &aiErr) {
			out := unavailableOutcome(kind, aiErr.Message)
			out.Error.Code = aiErr.Code
			s.recordGeneration(out)
			writeJSON(w, statusForOutcome(out), out)
			return
		}
		writeAIError(w, err)
		return
	}
	s.metrics.Set(metricGenerationJobs, metrics.Labels{"state": "running"}, float64(s.jobs.Running()), helpGenerationJobs)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "job": job})
}

// handleGenerationJob implements GET /generation/{id}: the poll a client runs
// after a submission. The body is always the job's own view, which is a live
// copy of the pipeline's report while the run is going and the pipeline's final
// report once it is over; the status line says only whether the caller may see
// it at all. A job that belongs to another learner is reported as unknown,
// never as forbidden, so an id cannot be probed for existence.
func (s *Server) handleGenerationJob(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if s.jobs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok": false, "job": nil,
			"error": map[string]string{"code": ai.CodeUnavailable, "message": "AI is off: no generation service is configured."},
		})
		return
	}
	view, found, err := s.jobs.Job(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	if !found {
		notFound(w, "unknown generation job: "+r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": view})
}

// unavailableOutcome is the shape every "AI is off" answer takes.
func unavailableOutcome(kind, message string) schema.Outcome {
	return schema.Outcome{
		Status:   schema.StatusUnavailable,
		Kind:     kind,
		Artifact: nil,
		Stages:   []schema.Stage{},
		Attempts: []schema.Attempt{},
		Error:    &schema.Error{Code: ai.CodeUnavailable, Message: message},
	}
}

// statusForOutcome maps the envelope onto a status line. The envelope is always
// the body, so a client can branch on `status` without inspecting the code; the
// status line exists for caches, proxies and humans.
func statusForOutcome(o schema.Outcome) int {
	if o.Error != nil && o.Error.Code == ai.CodeInvalidRequest {
		return http.StatusBadRequest
	}
	switch o.Status {
	case schema.StatusActive, schema.StatusAwaitingApproval:
		// Both mean a course was created and stored: an active one is finished,
		// an awaiting-approval one has its outline stored and is waiting for the
		// learner. 201 is the honest status line for either.
		return http.StatusCreated
	case schema.StatusUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusUnprocessableEntity
	}
}

/* ---------- stored challenges ---------- */

// handleListChallenges implements GET /challenges.
func (s *Server) handleListChallenges(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	svc := s.aiServiceFor(u.ID)
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok": false, "challenges": []schema.Record{},
			"error": map[string]string{"code": ai.CodeUnavailable, "message": "AI is off: no generation service is configured."},
		})
		return
	}
	recs, err := svc.ChallengesFor(r.Context(), u.ID)
	if err != nil {
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "challenges": recs})
}

// handleGetChallenge implements GET /challenges/{id}.
func (s *Server) handleGetChallenge(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	svc := s.aiServiceFor(u.ID)
	if svc == nil {
		writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: "AI is off: no generation service is configured."})
		return
	}
	rec, found, err := svc.Challenge(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	if !found {
		notFound(w, "unknown challenge: "+r.PathValue("id"))
		return
	}
	// The {ok, challenge} envelope, matching GET /courses/{id}'s {ok, course}.
	// The two resource reads differed here for no reason; one shape is the rule.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "challenge": rec})
}

// handleResetChallenge implements POST /challenges/{id}/reset. It removes the
// generated definition; learner attempts are the learner's data and stay.
func (s *Server) handleResetChallenge(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	svc := s.aiServiceFor(u.ID)
	if svc == nil {
		writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: "AI is off: no generation service is configured."})
		return
	}
	if err := svc.Reset(r.Context(), r.PathValue("id")); err != nil {
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ---------- stored courses ---------- */

// aiCourseDepot is the configured course store, or nil when AI is off. Stored
// courses are read through the depot rather than through the owner-scoped
// wrapper, because the wrapper scopes writes only: ownership is enforced here,
// in the handlers that a request reaches with an id of its own choosing.
func (s *Server) aiCourseDepot() schema.Depot {
	cfg, ok := s.currentAI()
	if !ok || cfg.Depot == nil {
		return nil
	}
	return cfg.Depot
}

// courseView is a stored course as a client sees it. The owner is the session's
// own account and travels nowhere in the body.
func courseView(rec schema.CourseRecord) schema.CourseRecord {
	rec.OwnerID = ""
	return rec
}

// handleListCourses implements GET /courses.
func (s *Server) handleListCourses(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	depot := s.aiCourseDepot()
	if depot == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok": false, "courses": []schema.CourseRecord{},
			"error": map[string]string{"code": ai.CodeUnavailable, "message": "AI is off: no generation service is configured."},
		})
		return
	}
	recs, err := depot.CoursesFor(r.Context(), u.ID)
	if err != nil {
		writeAIError(w, err)
		return
	}
	/* The store filters by owner in SQL; nothing another learner owns is loaded
	   only to be discarded. */
	mine := make([]schema.CourseRecord, 0, len(recs))
	for _, rec := range recs {
		mine = append(mine, courseView(rec))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "courses": mine})
}

// handleAdminCourses implements GET /admin/courses: every stored course, every
// owner, with the owner id kept in the body — an inventory an administrator can
// act on, not a learner view. A learner is refused with 403; the owner-scoped
// GET /courses stays the learner's own list. A course that is not the caller's
// stays 404 on the detail routes, so an id still cannot be probed for
// existence from here.
func (s *Server) handleAdminCourses(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	depot := s.aiCourseDepot()
	if depot == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok": false, "courses": []schema.CourseRecord{},
			"error": map[string]string{"code": ai.CodeUnavailable, "message": "AI is off: no generation service is configured."},
		})
		return
	}
	recs, err := depot.Courses(r.Context())
	if err != nil {
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "courses": recs})
}

// ownedCourse fetches a course by id and enforces the owner rule: a course that
// is not the caller's is reported as missing, never as forbidden, so an id
// cannot be probed for existence. The store error is returned so the caller can
// answer it with the AI error envelope.
func ownedCourse(ctx context.Context, depot schema.Depot, ownerID, id string) (schema.CourseRecord, bool, error) {
	rec, found, err := depot.Course(ctx, id)
	if err != nil {
		return schema.CourseRecord{}, false, err
	}
	if !found || rec.OwnerID != ownerID {
		return schema.CourseRecord{}, false, nil
	}
	return rec, true, nil
}

// handleGetCourse implements GET /courses/{id}.
func (s *Server) handleGetCourse(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	depot := s.aiCourseDepot()
	if depot == nil {
		writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: "AI is off: no generation service is configured."})
		return
	}
	rec, owned, err := ownedCourse(r.Context(), depot, u.ID, r.PathValue("id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	if !owned {
		notFound(w, "unknown course: "+r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "course": courseView(rec)})
}

// handleDeleteCourse implements DELETE /courses/{id}. It removes the generated
// plan; learner attempts and progress are the learner's data and stay.
func (s *Server) handleDeleteCourse(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	depot := s.aiCourseDepot()
	if depot == nil {
		writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: "AI is off: no generation service is configured."})
		return
	}
	id := r.PathValue("id")
	_, owned, err := ownedCourse(r.Context(), depot, u.ID, id)
	if err != nil {
		writeAIError(w, err)
		return
	}
	if !owned {
		notFound(w, "unknown course: "+id)
		return
	}
	if err := depot.DeleteCourse(r.Context(), id); err != nil {
		writeAIError(w, err)
		return
	}
	// Discarding a course that was waiting on the learner's approval ends the
	// run that was paused on it, so a poller is told the run it was watching
	// will never continue rather than being left on `awaiting_approval`.
	if s.jobs != nil {
		s.jobs.DiscardCourse(u.ID, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleBuildCourse implements POST /courses/{id}/build: the approval that
// continues a paused course into its chapter phase. It is owner-scoped exactly
// like the read and delete routes — another account's course is 404, never 403,
// because an id must not be probeable for existence — and it refuses a course
// that is not waiting for approval rather than silently re-running chapters.
func (s *Server) handleBuildCourse(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	depot := s.aiCourseDepot()
	if depot == nil {
		writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: "AI is off: no generation service is configured."})
		return
	}
	id := r.PathValue("id")
	rec, owned, err := ownedCourse(r.Context(), depot, u.ID, id)
	if err != nil {
		writeAIError(w, err)
		return
	}
	if !owned {
		notFound(w, "unknown course: "+id)
		return
	}
	// Only a course that is still being built can be continued. A finished
	// course is not re-run, and a course refused earlier has no row at all.
	if rec.Status != schema.StatusBuilding && rec.Status != schema.StatusAwaitingApproval {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]string{
				"code":    ai.CodeInvalidRequest,
				"message": "That course is not awaiting approval: only a course that is still being built can be continued.",
			},
		})
		return
	}

	// A continuation is minutes of work, so it can be submitted as a job the
	// same way a fresh generation is; a client that does not ask for one waits
	// for the envelope.
	if s.jobs != nil && asyncRequested(r) {
		s.submitCourseBuild(w, r, u.ID, id)
		return
	}

	cfg, _ := s.currentAI()
	timeout := cfg.Limits.Merge(ai.DefaultLimits()).Timeout + 5*time.Second
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	release, admitted := s.enter(ctx)
	if !admitted {
		out := unavailableOutcome("course", "The gateway is at its concurrency limit; try again shortly.")
		out.Error.Code = ai.CodeEnvironmentDown
		s.recordGeneration(out)
		writeJSON(w, http.StatusServiceUnavailable, out)
		return
	}
	defer release()

	svc := s.aiServiceFor(u.ID)
	if svc == nil {
		out := unavailableOutcome("course", "AI is off: no generation service is configured.")
		s.recordGeneration(out)
		writeJSON(w, http.StatusServiceUnavailable, out)
		return
	}
	out := svc.BuildCourse(ctx, id)
	out.Kind = "course"
	s.recordGeneration(out)
	writeJSON(w, statusForOutcome(out), out)
}

// submitCourseBuild answers POST /courses/{id}/build?async=1 with the id of the
// continuation to poll. It mirrors submitGenerationJob: a refusal the registry
// cannot start is reported in the same envelope the blocking route uses.
func (s *Server) submitCourseBuild(w http.ResponseWriter, r *http.Request, ownerID, courseID string) {
	job, err := s.jobs.Submit(r.Context(), jobs.Submission{
		OwnerID:       ownerID,
		Kind:          "course",
		BuildCourseID: courseID,
		OnFinish:      s.recordJob,
	})
	if err != nil {
		var aiErr *schema.Error
		if errors.As(err, &aiErr) {
			out := unavailableOutcome("course", aiErr.Message)
			out.Error.Code = aiErr.Code
			s.recordGeneration(out)
			writeJSON(w, statusForOutcome(out), out)
			return
		}
		writeAIError(w, err)
		return
	}
	s.metrics.Set(metricGenerationJobs, metrics.Labels{"state": "running"}, float64(s.jobs.Running()), helpGenerationJobs)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "job": job})
}

/* ---------- tutoring, critique, defects ---------- */

// handleTutor implements POST /tutor.
func (s *Server) handleTutor(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req ai.TutorRequest
	if !readJSON(w, r, &req) {
		return
	}
	svc := s.aiServiceFor(u.ID)
	if svc == nil {
		writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: "AI is off: no generation service is configured."})
		return
	}
	resp, err := svc.Tutor(r.Context(), req)
	if err != nil {
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleCritique implements POST /challenges/{id}/critique.
func (s *Server) handleCritique(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req ai.CritiqueRequest
	if !readJSON(w, r, &req) {
		return
	}
	// The path names the challenge; a body that disagrees is ignored.
	req.ChallengeID = r.PathValue("id")
	svc := s.aiServiceFor(u.ID)
	if svc == nil {
		writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: "AI is off: no generation service is configured."})
		return
	}
	resp, err := svc.Critique(r.Context(), req)
	if err != nil {
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleReportDefect implements POST /challenges/{id}/report. A defect is only
// ever "verified" after the service has reproduced it by execution.
func (s *Server) handleReportDefect(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var rep ai.DefectReport
	if !readJSON(w, r, &rep) {
		return
	}
	rep.ChallengeID = r.PathValue("id")
	svc := s.aiServiceFor(u.ID)
	if svc == nil {
		writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: "AI is off: no generation service is configured."})
		return
	}
	verdict, err := svc.ReportDefect(r.Context(), rep)
	if err != nil {
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, verdict)
}

// handlePatch implements POST /challenges/{id}/patches/{patchID}. The identity
// of the patch comes from the path and the approval from the body, and the
// service refuses an unapproved or unverified patch on its own.
func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var dec ai.PatchDecision
	if !readJSON(w, r, &dec) {
		return
	}
	dec.PatchID = r.PathValue("patchID")
	// The route names the challenge the approval is for. The service refuses a
	// patch that belongs to a different one, so an approval cannot be redirected
	// by pairing a valid patch id with another challenge's route.
	dec.ChallengeID = r.PathValue("id")
	// The approver is the authenticated account, never a name the body chose.
	dec.ApprovedBy = u.Email
	svc := s.aiServiceFor(u.ID)
	if svc == nil {
		writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: "AI is off: no generation service is configured."})
		return
	}
	res, err := svc.ApplyPatch(r.Context(), dec)
	if err != nil {
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

/* ---------- errors and metrics ---------- */

// writeAIError maps a service error onto a status line. An *schema.Error carries a
// stable code; anything else is a bug and is reported as one.
func writeAIError(w http.ResponseWriter, err error) {
	var aiErr *schema.Error
	if !errors.As(err, &aiErr) {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "EVALUATION_ERROR", "message": err.Error()},
		})
		return
	}
	status := http.StatusInternalServerError
	switch aiErr.Code {
	case ai.CodeUnavailable, ai.CodeEnvironmentDown:
		status = http.StatusServiceUnavailable
	case ai.CodeInvalidRequest:
		status = http.StatusBadRequest
	case ai.CodeInvalidArtifact, ai.CodeReferenceFailed, ai.CodeCrossCheckFailed,
		ai.CodeRepairExhausted, ai.CodePatchUnverified:
		status = http.StatusUnprocessableEntity
	case ai.CodeNotFound:
		status = http.StatusNotFound
	case ai.CodePatchUnapproved:
		status = http.StatusConflict
	default:
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]any{"error": aiErr})
}

// metricGenerations counts generation runs by outcome, so "AI is on but every
// attempt is rejected" is visible in /metrics instead of only in a log.
const (
	metricGenerations = "querycraft_generations_total"
	helpGenerations   = "AI generation runs, by kind, status and error code."
)

// metricGenerationJobs is a gauge, not a counter: how many generations are
// running right now is the question an asynchronous gateway raises, and a
// counter could only ever go up.
const (
	metricGenerationJobs = "querycraft_generation_jobs"
	helpGenerationJobs   = "Generation jobs held by the gateway, by state."
)

// recordJob counts a generation that ran in the background. It is the same
// series the blocking route increments, because from the operator's side a
// generation is a generation however it was requested.
func (s *Server) recordJob(v jobs.JobView) {
	code := ""
	if v.Error != nil {
		code = v.Error.Code
	}
	kind := v.Kind
	if kind == "" {
		kind = "challenge"
	}
	s.metrics.Inc(metricGenerations, metrics.Labels{"kind": kind, "status": v.Status, "code": code}, helpGenerations)
	s.metrics.Set(metricGenerationJobs, metrics.Labels{"state": "running"}, float64(s.jobs.Running()), helpGenerationJobs)
}

func (s *Server) recordGeneration(out schema.Outcome) {
	code := ""
	if out.Error != nil {
		code = out.Error.Code
	}
	kind := out.Kind
	if kind == "" {
		kind = "challenge"
	}
	s.metrics.Inc(metricGenerations,
		metrics.Labels{"kind": kind, "status": out.Status, "code": code},
		helpGenerations)
}
