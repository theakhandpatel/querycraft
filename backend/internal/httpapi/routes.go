package httpapi

/* The routing table.
 *
 * One table declares every route the gateway serves: its method, its path, its
 * handler, whether it needs a session, the operation name the access log
 * carries, and the error vocabulary its unreadable bodies keep. Handler()
 * builds the mux from it, withRouteAuth enforces the same declaration, and the
 * observability middleware reads the operation from it, so one table is the
 * source for all three and they cannot drift apart.
 *
 * A route is closed unless it declares authPublic. The zero value is
 * authRequired, so an entry that forgets to name its requirement is still
 * gated, and a pattern the table does not declare at all is gated too.
 */

import (
	"context"
	"net/http"

	"github.com/querycraft/sqlquery/backend/internal/apidocs"
	"github.com/querycraft/sqlquery/backend/internal/webui"
)

// authMode is a route's requirement, enforced by withRouteAuth.
type authMode int

const (
	// authRequired refuses a request with no attached user: 401 AUTH_REQUIRED.
	// It is the zero value, so a route that declares nothing is closed.
	authRequired authMode = iota
	// authPublic serves an anonymous caller.
	authPublic
)

// bodyVocab names the error vocabulary a route's unreadable body keeps. A
// statement route's body IS a statement, so it answers with the query codes;
// an AI route's client branches on the AI error enum; an auth route's on the
// auth one. The zero value is the generic request vocabulary.
type bodyVocab int

const (
	vocabRequest bodyVocab = iota
	vocabStatement
	vocabAI
	vocabAuth
)

// route is one entry in the routing table.
type route struct {
	method  string
	path    string
	handler http.HandlerFunc
	auth    authMode
	// operation is the stable name the access log carries. It is deliberately
	// decoupled from the URL: renaming a path must not change how past logs
	// read, and a route added without one logs as "other" rather than
	// vanishing into the route label.
	operation string
	// vocab selects the refusal codes for an unreadable body.
	vocab bodyVocab
}

// pattern is the ServeMux pattern the route registers as.
func (rt route) pattern() string { return rt.method + " " + rt.path }

// routes declares the gateway's surface. It reads no Server state, so the
// document test reads the same table from a zero Server.
func (s *Server) routes() []route {
	routes := []route{
		// Liveness, measurement and the API document. /diagnostics reports
		// pool state and learner privileges, so it needs a session.
		{http.MethodGet, "/health", s.handleHealth, authPublic, "health", 0},
		{http.MethodGet, "/capabilities", s.handleCapabilities, authPublic, "capabilities", 0},
		{http.MethodGet, "/metrics", s.handleMetrics, authPublic, "metrics", 0},
		{http.MethodGet, "/diagnostics", s.handleDiagnostics, authRequired, "diagnostics", 0},
		{http.MethodGet, "/openapi.json", apidocs.Handler, authPublic, "openapi", 0},
		{http.MethodGet, "/docs", apidocs.DocsHandler, authPublic, "docs", 0},
		{http.MethodGet, "/docs/", apidocs.DocsHandler, authPublic, "docs", 0},

		// Accounts. Signup and login are the two routes an anonymous caller
		// must reach; logout revokes the session it names, and /auth/me reads
		// it.
		{http.MethodPost, "/auth/signup", s.handleSignup, authPublic, "signup", vocabAuth},
		{http.MethodPost, "/auth/login", s.handleLogin, authPublic, "login", vocabAuth},
		{http.MethodPost, "/auth/logout", s.handleLogout, authRequired, "logout", vocabAuth},
		{http.MethodGet, "/auth/me", s.handleMe, authRequired, "me", vocabAuth},

		// Statements. An anonymous caller keeps the shared, read-only dataset;
		// a session routes the statement to the caller's own workspace.
		{http.MethodPost, "/query", s.handleQuery, authPublic, "query", vocabStatement},
		{http.MethodPost, "/explain", s.handleExplain, authPublic, "explain", vocabStatement},
		{http.MethodPost, "/answers/evaluate", s.handleEvaluate, authPublic, "evaluate", vocabStatement},
		{http.MethodGet, "/environment", s.handleEnvironment, authPublic, "environment", 0},
		{http.MethodGet, "/datasets/{id}/tables", s.handleTables, authPublic, "tables", 0},
		{http.MethodPost, "/environment/reset", s.handleEnvironmentReset, authRequired, "reset_environment", 0},
		{http.MethodPost, "/environment/prepare", s.handleEnvironmentPrepare, authRequired, "prepare_environment", 0},

		// Owner-scoped learner data.
		{http.MethodGet, "/progress/{course}", s.handleGetProgress, authRequired, "progress", 0},
		{http.MethodPut, "/progress/{course}", s.handlePutProgress, authRequired, "progress", 0},
		{http.MethodGet, "/preferences", s.handleGetPreferences, authRequired, "preferences", 0},
		{http.MethodPut, "/preferences", s.handlePutPreferences, authRequired, "preferences", 0},
		{http.MethodPost, "/events", s.handleEvent, authRequired, "event", 0},

		// Generation and the records it stores.
		{http.MethodPost, "/courses/generate", s.handleGenerateCourse, authRequired, "generate_course", vocabAI},
		{http.MethodPost, "/challenges/generate", s.handleGenerateChallenge, authRequired, "generate_challenge", vocabAI},
		{http.MethodGet, "/generation/{id}", s.handleGenerationJob, authRequired, "generation_job", 0},
		{http.MethodGet, "/courses", s.handleListCourses, authRequired, "courses", 0},
		{http.MethodGet, "/admin/courses", s.handleAdminCourses, authRequired, "admin_courses", 0},
		{http.MethodGet, "/admin/ai", s.handleAdminAI, authRequired, "admin_ai", 0},
		{http.MethodGet, "/admin/ai/models", s.handleAdminAIModels, authRequired, "admin_ai_models", 0},
		{http.MethodPut, "/admin/ai", s.handleAdminAISave, authRequired, "admin_ai", 0},
		{http.MethodGet, "/courses/{id}", s.handleGetCourse, authRequired, "course", vocabAI},
		{http.MethodPut, "/courses/{id}", s.handlePutCourse, authRequired, "course", vocabAI},
		{http.MethodPost, "/courses/{id}/build", s.handleBuildCourse, authRequired, "build_course", vocabAI},
		{http.MethodDelete, "/courses/{id}", s.handleDeleteCourse, authRequired, "course", vocabAI},
		{http.MethodGet, "/challenges", s.handleListChallenges, authRequired, "challenges", 0},
		{http.MethodGet, "/challenges/{id}", s.handleGetChallenge, authRequired, "challenge", vocabAI},
		{http.MethodPost, "/challenges/{id}/reset", s.handleResetChallenge, authRequired, "reset_challenge", vocabAI},
		{http.MethodPost, "/challenges/{id}/critique", s.handleCritique, authRequired, "critique", vocabAI},
		{http.MethodPost, "/challenges/{id}/report", s.handleReportDefect, authRequired, "report_defect", vocabAI},
		{http.MethodPost, "/challenges/{id}/patches/{patchID}", s.handlePatch, authRequired, "apply_patch", vocabAI},
		{http.MethodPost, "/tutor", s.handleTutor, authRequired, "tutor", vocabAI},
		{http.MethodGet, "/runs", s.handleListRuns, authRequired, "runs", 0},
		{http.MethodGet, "/runs/{id}", s.handleGetRun, authRequired, "run", 0},
	}

	// Catch-all for the embedded SPA. In Go 1.22+ ServeMux the more specific
	// patterns above (including /health, /query, /docs and /openapi.json) still
	// win, so the UI only handles paths no API route claims. A page carries no
	// data, so both the SPA and the placeholder are public.
	if webui.Available() {
		routes = append(routes, route{http.MethodGet, "/", webui.Handler().ServeHTTP, authPublic, "spa", 0})
	} else {
		routes = append(routes, route{http.MethodGet, "/{$}", webui.Placeholder().ServeHTTP, authPublic, "spa", 0})
	}
	return routes
}

// routeKey is the context key the route table entry travels under, so a
// handler can ask which route it is serving without parsing the URL again.
type routeKey struct{}

func withRoute(ctx context.Context, rt route) context.Context {
	return context.WithValue(ctx, routeKey{}, rt)
}

func routeFrom(ctx context.Context) (route, bool) {
	rt, ok := ctx.Value(routeKey{}).(route)
	return rt, ok
}
