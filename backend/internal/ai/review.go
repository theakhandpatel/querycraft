package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/eval"
)

// Tutoring, critique, defect verification and patch application (§6.5, §11.2).
//
// One rule runs through all of it: the model never sees a reference solution,
// and a reply that contains one is discarded rather than returned. The learner
// is the only actor that may accept a failure or approve a fix, so no function
// here ever sets those flags on the learner's behalf.

// tutor answers with a hint that never reveals the reference solution.
func (s *Service) tutor(ctx context.Context, req TutorRequest) (TutorResponse, error) {
	if err := s.requireAvailable(); err != nil {
		return TutorResponse{}, err
	}
	if strings.TrimSpace(req.SQL) == "" || strings.TrimSpace(req.Objective) == "" {
		return TutorResponse{}, newError(CodeInvalidRequest, "",
			"A hint needs the learner's SQL and the objective.", "sql/objective")
	}
	limits := s.cfg.Limits.Merge(DefaultLimits())
	level := clampInt(req.HintLevel, 0, limits.MaxHints)

	text, terr := s.complete(ctx, ProviderRequest{
		Kind:   "tutor",
		System: systemPrompt(),
		// Only the learner's own material. The reference solution is absent by
		// construction, so this request cannot leak it even if the model wanted
		// to quote it back.
		Prompt:         tutorPrompt(req, level),
		MaxOutputBytes: limits.MaxOutputBytes,
	})
	if terr != nil {
		return TutorResponse{}, terr
	}
	if err := s.guardSolutions(ctx, req.ChallengeID, text); err != nil {
		return TutorResponse{}, err
	}
	return TutorResponse{
		Message:         text,
		HintLevel:       level,
		RevealsSolution: false,
		Provenance:      s.stampProvenance(0),
	}, nil
}

// critique reviews the learner's query on demand, under the same no-solution
// rule as tutoring.
func (s *Service) critique(ctx context.Context, req CritiqueRequest) (CritiqueResponse, error) {
	if err := s.requireAvailable(); err != nil {
		return CritiqueResponse{}, err
	}
	if strings.TrimSpace(req.SQL) == "" {
		return CritiqueResponse{}, newError(CodeInvalidRequest, "",
			"A critique needs the learner's SQL.", "sql")
	}
	limits := s.cfg.Limits.Merge(DefaultLimits())
	text, terr := s.complete(ctx, ProviderRequest{
		Kind:           "critique",
		System:         systemPrompt(),
		Prompt:         critiquePrompt(req),
		MaxOutputBytes: limits.MaxOutputBytes,
	})
	if terr != nil {
		return CritiqueResponse{}, terr
	}
	if err := s.guardSolutions(ctx, req.ChallengeID, text); err != nil {
		return CritiqueResponse{}, err
	}
	return CritiqueResponse{
		Message:    text,
		Notes:      noteLines(text),
		Provenance: s.stampProvenance(0),
	}, nil
}

// guardSolutions refuses a reply that contains a known solution. It is
// defence in depth: a well-behaved provider cannot produce the reference
// because it was never sent one, and this guard is what keeps a future
// provider — or a future prompt change that accidentally includes the
// artifact — from regressing that property. When there is no stored challenge
// there is nothing to compare against, which is not a silent pass on the
// property; the request itself still carried no solution.
func (s *Service) guardSolutions(ctx context.Context, challengeID, text string) *Error {
	art, ok, err := s.storedArtifact(ctx, challengeID)
	if err != nil || !ok {
		return nil
	}
	for field, solution := range map[string]string{
		"reference_solution":   art.ReferenceSolution,
		"cross_check_solution": art.CrossCheckSolution,
	} {
		if containsSolution(text, solution) {
			return newError(CodeInvalidArtifact, "",
				"The provider reply contained a solution and was discarded.",
				field+": the reply reveals a stored solution, so it was not returned")
		}
	}
	return nil
}

// containsSolution reports whether reply contains a stored solution, ignoring
// case and whitespace. Short strings are not armed: a two-word fragment would
// match ordinary prose and turn every hint into a false positive.
func containsSolution(reply, solution string) bool {
	norm := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				continue
			}
			b.WriteRune(r)
		}
		return b.String()
	}
	want := norm(solution)
	if len(want) < 24 {
		return false
	}
	return strings.Contains(norm(reply), want)
}

// reportDefect verifies a claimed defect by execution before proposing
// anything. The claim this settles is concrete: the learner's query returns
// something the reference result contradicts under the challenge's own
// contract. A claim we cannot turn into a probe is reported as not verified,
// never as agreed with.
func (s *Service) reportDefect(ctx context.Context, rep DefectReport) (DefectVerdict, error) {
	if err := s.requireAvailable(); err != nil {
		return DefectVerdict{}, err
	}
	if strings.TrimSpace(rep.ChallengeID) == "" {
		return DefectVerdict{}, newError(CodeInvalidRequest, "", "A defect report needs a challenge id.", "challenge_id")
	}
	rec, found, err := s.cfg.Depot.Challenge(ctx, rep.ChallengeID)
	if err != nil {
		return DefectVerdict{}, newError(CodeUnavailable, "", "The challenge could not be loaded.", engine.RedactSecrets(err.Error()))
	}
	if !found {
		return DefectVerdict{}, newError(CodeNotFound, "", "No such challenge.", rep.ChallengeID)
	}
	if strings.TrimSpace(rep.SQL) == "" {
		return DefectVerdict{
			Verified: false,
			Applied:  false,
			Summary:  "The report contains no query to run, so the claim cannot be verified by execution. Describe the problem as a query whose result contradicts the exercise.",
		}, nil
	}

	limits := s.cfg.Limits.Merge(DefaultLimits())
	runCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	ex, release, serr := s.cfg.Runner.Scratch(runCtx, "ai_defect_"+shortHash(rep.ChallengeID+rep.SQL))
	if serr != nil {
		return DefectVerdict{}, newError(CodeEnvironmentDown, "",
			"A throwaway environment could not be created for the verification.", engine.RedactSecrets(serr.Error()))
	}
	defer release()
	if perr := s.prepareScratch(runCtx, ex, rec.Artifact); perr != nil {
		return DefectVerdict{}, perr
	}

	probe := engine.Execute(runCtx, ex, rep.SQL, engine.DefaultLimits())
	reference := engine.Execute(runCtx, ex, rec.Artifact.ReferenceSolution, engine.DefaultLimits())
	contract := evalContract(rec.Artifact.ResultContract)

	verdict := DefectVerdict{Evidence: Evidence{}}
	switch {
	case reference.Status != "succeeded":
		// The exercise's own reference does not run, so the exercise is broken
		// regardless of what the learner claimed.
		verdict.Verified = true
		verdict.Summary = "The exercise's reference solution does not run: " + queryErrorText(reference)
	case probe.Status != "succeeded":
		verdict.Summary = "The reported query does not run against the exercise's data, so the claim did not reproduce: " + queryErrorText(probe)
	default:
		verdict.Evidence.ReferenceRows = reference.RowCount
		cmp := eval.CompareDetailed(
			eval.ExpectedResult{Columns: contract.Columns, Rows: reference.Rows},
			contract, probe)
		if cmp.OK {
			verdict.Summary = "The reported query and the reference agree under the result contract, so the exercise is not contradicted by execution."
		} else {
			verdict.Verified = true
			verdict.Summary = "Execution reproduced the report: the query disagrees with the reference under the result contract (" + strings.Join(cmp.Codes, ", ") + ")."
		}
	}

	if !verdict.Verified {
		return verdict, nil
	}
	proposal, perr := s.proposePatch(runCtx, rec, rep)
	if perr != nil {
		return DefectVerdict{}, perr
	}
	verdict.Patch = proposal
	return verdict, nil
}

// proposePatch asks the provider for a corrected bundle and turns the fields it
// changed into a reviewable diff. When the model has nothing better, the
// proposal records the clarification in the prompt: a verified defect must
// still produce something the learner can approve or dismiss.
func (s *Service) proposePatch(ctx context.Context, rec Record, rep DefectReport) (*PatchProposal, *Error) {
	limits := s.cfg.Limits.Merge(DefaultLimits())
	previous, _ := json.Marshal(rec.Artifact)
	// The objectives come first so a generator that picks its topic from the
	// brief (the fake provider does) stays on the same topic as the artifact.
	prompt := strings.Join([]string{
		strings.Join(rec.Artifact.Objectives, " "),
		rec.Artifact.Title,
		rec.Artifact.Prompt,
	}, "\n")
	text, cerr := s.complete(ctx, ProviderRequest{
		Kind:           "repair",
		System:         systemPrompt(),
		Prompt:         prompt,
		MaxOutputBytes: limits.MaxOutputBytes,
		Previous:       string(previous),
		Failure:        "A learner reported: " + rep.Description + " (probe: " + rep.SQL + ")",
	})
	changes := []PatchChange{}
	if cerr == nil {
		var candidate Artifact
		if decodeBundle(text, &candidate) == nil && sameContract(rec.Artifact.ResultContract, candidate.ResultContract) {
			// A M4 patch does not change the result contract: a new contract
			// would make the patch and the exercise disagree about what the
			// answer is, and no execution evidence here could settle that
			// against the learner's own attempt history. Contract changes stay
			// a human edit.
			changes = diffArtifact(rec.Artifact, candidate)
		}
	}
	if len(changes) == 0 {
		changes = []PatchChange{{
			Field: "prompt",
			From:  rec.Artifact.Prompt,
			To:    rec.Artifact.Prompt + "\n\nClarification from a verified report: " + strings.TrimSpace(rep.Description),
		}}
	}
	proposal := PatchProposal{
		ID:          "patch-" + shortHash(rec.Artifact.ID+"\x00"+rep.SQL+"\x00"+rep.Description),
		ChallengeID: rec.Artifact.ID,
		Summary:     "Reword or correct the exercise after a report that reproduced by execution.",
		Changes:     changes,
	}
	if err := s.cfg.Depot.SavePatch(ctx, proposal); err != nil {
		return nil, newError(CodeUnavailable, "", "The patch could not be stored.", engine.RedactSecrets(err.Error()))
	}
	return &proposal, nil
}

// applyPatch applies a patch only with an explicit approval, and only after the
// patched artifact has passed the same verification every generated bundle
// passes. Applied:false is returned on every refusal.
func (s *Service) applyPatch(ctx context.Context, dec PatchDecision) (PatchResult, error) {
	if err := s.requireAvailable(); err != nil {
		return PatchResult{}, err
	}
	if strings.TrimSpace(dec.PatchID) == "" {
		return PatchResult{}, newError(CodeInvalidRequest, "", "A patch id is required.", "patch_id")
	}
	patch, found, err := s.cfg.Depot.Patch(ctx, dec.PatchID)
	if err != nil {
		return PatchResult{}, newError(CodeUnavailable, "", "The patch could not be loaded.", engine.RedactSecrets(err.Error()))
	}
	if !found {
		return PatchResult{}, newError(CodeNotFound, "", "No such patch.", dec.PatchID)
	}
	if !dec.Approved || strings.TrimSpace(dec.ApprovedBy) == "" {
		e := newError(CodePatchUnapproved, "",
			"A patch is never applied without an explicit approval.", "approved/approved_by")
		return PatchResult{Applied: false, Patch: &patch, Error: e}, e
	}
	rec, found, err := s.cfg.Depot.Challenge(ctx, patch.ChallengeID)
	if err != nil {
		return PatchResult{}, newError(CodeUnavailable, "", "The challenge could not be loaded.", engine.RedactSecrets(err.Error()))
	}
	if !found {
		return PatchResult{}, newError(CodeNotFound, "", "The patched challenge no longer exists.", patch.ChallengeID)
	}

	patched, aerr := applyChanges(rec.Artifact, patch.Changes)
	if aerr != nil {
		return PatchResult{Applied: false, Patch: &patch, Error: aerr}, aerr
	}
	limits := s.cfg.Limits.Merge(DefaultLimits())
	runCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()
	if verr := s.verifyArtifact(runCtx, patched, limits); verr != nil {
		e := newError(CodePatchUnverified, verr.Stage,
			"The patched challenge did not pass verification; it was not applied.",
			strings.TrimSpace(verr.Message+" "+verr.Detail))
		return PatchResult{Applied: false, Patch: &patch, Error: e}, e
	}

	if err := s.cfg.Depot.ApprovePatch(ctx, patch.ID, dec.ApprovedBy); err != nil {
		return PatchResult{}, newError(CodeUnavailable, "", "The approval could not be recorded.", engine.RedactSecrets(err.Error()))
	}
	patched.Version = rec.Artifact.Version + 1
	patched.Provenance = s.stampProvenance(0)
	out := Outcome{Status: StatusActive, Kind: "challenge", Artifact: &patched, Stages: []Stage{}, Attempts: []Attempt{}}
	if err := s.cfg.Depot.SaveChallenge(ctx, Record{
		Artifact:  patched,
		Outcome:   out,
		Status:    StatusActive,
		OwnerID:   rec.OwnerID,
		CreatedAt: patched.Provenance.CreatedAt,
	}); err != nil {
		return PatchResult{}, newError(CodeUnavailable, "", "The patched challenge could not be stored.", engine.RedactSecrets(err.Error()))
	}
	return PatchResult{Applied: true, Patch: &patch}, nil
}

/* ---------- helpers ---------- */

// requireAvailable is the honest refusal every AI entry point shares. It names
// the missing piece rather than returning an empty success.
func (s *Service) requireAvailable() *Error {
	av := s.Available()
	if av.Available {
		return nil
	}
	code := CodeUnavailable
	if s != nil && s.cfg.Provider != nil && s.cfg.Runner == nil {
		code = CodeEnvironmentDown
	}
	return newError(code, "", av.Reason, "")
}

func (s *Service) storedArtifact(ctx context.Context, id string) (Artifact, bool, error) {
	if strings.TrimSpace(id) == "" || s.cfg.Depot == nil {
		return Artifact{}, false, nil
	}
	rec, found, err := s.cfg.Depot.Challenge(ctx, id)
	if err != nil || !found {
		return Artifact{}, false, err
	}
	return rec.Artifact, true, nil
}

// stampProvenance attributes a non-artifact reply (a hint, a critique) to the
// provider that produced it. DatasetVersion may be zero here: a hint is not
// graded against a dataset, and zero says so rather than inventing a version.
func (s *Service) stampProvenance(repairs int) Provenance {
	d := s.cfg.Provider.Descriptor()
	return Provenance{
		Provider:         d.Name,
		Model:            d.Model,
		ProviderVersion:  d.Version,
		PipelineVersion:  PipelineVersion,
		EvaluatorVersion: eval.EvaluatorVersion,
		DatasetVersion:   s.cfg.DatasetVersion,
		CreatedAt:        time.Now().UTC(),
		Repairs:          repairs,
	}
}

// verifyArtifact runs the SQL-executing half of the pipeline against an
// artifact that already exists (a patch), without the repair loop: a patch is
// either correct as approved or it is not applied.
func (s *Service) verifyArtifact(ctx context.Context, art Artifact, limits Limits) *Error {
	p := newPipeline(s, GenerationRequest{Kind: "challenge"}, limits)
	return p.verify(ctx, art)
}

// prepareScratch applies a stored artifact's setup to a scratch environment and
// re-checks its dataset, so a defect probe runs against the same objects the
// challenge runs against.
func (s *Service) prepareScratch(ctx context.Context, ex engine.Executor, art Artifact) *Error {
	limits := s.cfg.Limits.Merge(DefaultLimits())
	p := newPipeline(s, GenerationRequest{Kind: "challenge"}, limits)
	return p.prepare(ctx, ex, art)
}

func tutorPrompt(req TutorRequest, level int) string {
	var b strings.Builder
	b.WriteString("Learner SQL:\n")
	b.WriteString(req.SQL)
	b.WriteString("\nObjective:\n")
	b.WriteString(req.Objective)
	fmt.Fprintf(&b, "\nhint_level: %d\n", level)
	if req.ErrorCode != "" {
		b.WriteString("Error code: " + req.ErrorCode + "\n")
	}
	if req.ErrorMessage != "" {
		b.WriteString("Error message: " + req.ErrorMessage + "\n")
	}
	if req.ResultSummary != "" {
		b.WriteString("Result summary: " + req.ResultSummary + "\n")
	}
	b.WriteString("Give one hint at that level. Do not write the full solution.")
	return b.String()
}

func critiquePrompt(req CritiqueRequest) string {
	var b strings.Builder
	b.WriteString("Review this query and list the concrete problems, without writing a full replacement.\n")
	b.WriteString("Learner SQL:\n")
	b.WriteString(req.SQL)
	if req.Objective != "" {
		b.WriteString("\nObjective:\n")
		b.WriteString(req.Objective)
	}
	return b.String()
}

// noteLines turns a plain-text critique into the notes list the response
// carries. Empty lines are dropped rather than rendered as blank notes.
func noteLines(text string) []string {
	var notes []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if line != "" {
			notes = append(notes, line)
		}
	}
	return notes
}

// sameContract reports whether two contracts are the same. It is a field-by-field
// comparison rather than a deep-equal so an int-vs-float zero tolerance cannot
// make a correct answer look like a contract change.
func sameContract(a, b ResultContract) bool {
	if len(a.Columns) != len(b.Columns) {
		return false
	}
	for i := range a.Columns {
		if a.Columns[i] != b.Columns[i] {
			return false
		}
	}
	return a.ColumnOrder == b.ColumnOrder &&
		a.RowOrder == b.RowOrder &&
		a.Duplicates == b.Duplicates &&
		a.NumericTolerance == b.NumericTolerance
}

// diffArtifact lists the content fields the model changed. Only fields a defect
// report can legitimately fix are compared; identity and provenance are not the
// model's to change.
func diffArtifact(before, after Artifact) []PatchChange {
	var changes []PatchChange
	add := func(field, from, to string) {
		if from != to {
			changes = append(changes, PatchChange{Field: field, From: from, To: to})
		}
	}
	add("title", before.Title, after.Title)
	add("prompt", before.Prompt, after.Prompt)
	add("reference_solution", before.ReferenceSolution, after.ReferenceSolution)
	add("cross_check_solution", before.CrossCheckSolution, after.CrossCheckSolution)
	add("difficulty", string(before.Difficulty), string(after.Difficulty))
	return changes
}

// applyChanges edits the fields a PatchChange names. An unknown field is an
// error rather than a silent no-op: a patch that changed nothing must not be
// reported as applied.
func applyChanges(art Artifact, changes []PatchChange) (Artifact, *Error) {
	for i, c := range changes {
		switch c.Field {
		case "title":
			art.Title = c.To
		case "prompt":
			art.Prompt = c.To
		case "reference_solution":
			art.ReferenceSolution = c.To
		case "cross_check_solution":
			art.CrossCheckSolution = c.To
		case "difficulty":
			art.Difficulty = Difficulty(c.To)
		default:
			return art, artifactError("changes",
				fmt.Sprintf("change %d names an unsupported field %q", i, c.Field))
		}
	}
	return art, nil
}

func shortHash(s string) string {
	return fmt.Sprintf("%016x", hashPrompt(s))
}
