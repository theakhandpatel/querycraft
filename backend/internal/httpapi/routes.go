package httpapi

/* The routing table.
 *
 * One table declares every route the gateway serves: its method, its path, its
 * handler and whether it needs a session. Handler() builds the mux from it, and
 * withRouteAuth enforces the same declaration, so the two cannot disagree about
 * what is served or who may reach it.
 *
 * A route is closed unless it declares authPublic. The zero value is
 * authRequired, so an entry that forgets to name its requirement is still
 * gated, and a pattern the table does not declare at all is gated too.
 */

import (
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

// route is one entry in the routing table.
type route struct {
	method  string
	path    string
	handler http.HandlerFunc
	auth    authMode
}

// pattern is the ServeMux pattern the route registers as.
func (rt route) pattern() string { return rt.method + " " + rt.path }

// routes declares the gateway's surface. It reads no Server state, so the
// document test reads the same table from a zero Server.
func (s *Server) routes() []route {
	routes := []route{
		// Liveness, measurement and the API document. /diagnostics reports
		// pool state and learner privileges, so it needs a session.
		{http.MethodGet, "/health", s.handleHealth, authPublic},
		{http.MethodGet, "/capabilities", s.handleCapabilities, authPublic},
		{http.MethodGet, "/metrics", s.handleMetrics, authPublic},
		{http.MethodGet, "/diagnostics", s.handleDiagnostics, authRequired},
		{http.MethodGet, "/openapi.json", apidocs.Handler, authPublic},
		{http.MethodGet, "/docs", apidocs.DocsHandler, authPublic},
		{http.MethodGet, "/docs/", apidocs.DocsHandler, authPublic},

		// Accounts. Signup and login are the two routes an anonymous caller
		// must reach; logout revokes the session it names, and /auth/me reads
		// it.
		{http.MethodPost, "/auth/signup", s.handleSignup, authPublic},
		{http.MethodPost, "/auth/login", s.handleLogin, authPublic},
		{http.MethodPost, "/auth/logout", s.handleLogout, authRequired},
		{http.MethodGet, "/auth/me", s.handleMe, authRequired},

		// Statements. An anonymous caller keeps the shared, read-only dataset;
		// a session routes the statement to the caller's own workspace.
		{http.MethodPost, "/query", s.handleQuery, authPublic},
		{http.MethodPost, "/explain", s.handleExplain, authPublic},
		{http.MethodPost, "/answers/evaluate", s.handleEvaluate, authPublic},
		{http.MethodGet, "/environment", s.handleEnvironment, authPublic},
		{http.MethodGet, "/datasets/{id}/tables", s.handleTables, authPublic},
		{http.MethodPost, "/environment/reset", s.handleEnvironmentReset, authRequired},

		// Owner-scoped learner data.
		{http.MethodGet, "/progress/{course}", s.handleGetProgress, authRequired},
		{http.MethodPut, "/progress/{course}", s.handlePutProgress, authRequired},
		{http.MethodPost, "/events", s.handleEvent, authRequired},

		// Generation and the records it stores.
		{http.MethodPost, "/courses/generate", s.handleGenerateCourse, authRequired},
		{http.MethodPost, "/challenges/generate", s.handleGenerateChallenge, authRequired},
		{http.MethodGet, "/generation/{id}", s.handleGenerationJob, authRequired},
		{http.MethodGet, "/courses", s.handleListCourses, authRequired},
		{http.MethodGet, "/admin/courses", s.handleAdminCourses, authRequired},
		{http.MethodGet, "/admin/ai", s.handleAdminAI, authRequired},
		{http.MethodGet, "/admin/ai/models", s.handleAdminAIModels, authRequired},
		{http.MethodPut, "/admin/ai", s.handleAdminAISave, authRequired},
		{http.MethodGet, "/courses/{id}", s.handleGetCourse, authRequired},
		{http.MethodPost, "/courses/{id}/build", s.handleBuildCourse, authRequired},
		{http.MethodDelete, "/courses/{id}", s.handleDeleteCourse, authRequired},
		{http.MethodGet, "/challenges", s.handleListChallenges, authRequired},
		{http.MethodGet, "/challenges/{id}", s.handleGetChallenge, authRequired},
		{http.MethodPost, "/challenges/{id}/reset", s.handleResetChallenge, authRequired},
		{http.MethodPost, "/challenges/{id}/critique", s.handleCritique, authRequired},
		{http.MethodPost, "/challenges/{id}/report", s.handleReportDefect, authRequired},
		{http.MethodPost, "/challenges/{id}/patches/{patchID}", s.handlePatch, authRequired},
		{http.MethodPost, "/tutor", s.handleTutor, authRequired},
		{http.MethodGet, "/runs", s.handleListRuns, authRequired},
		{http.MethodGet, "/runs/{id}", s.handleGetRun, authRequired},
	}

	// Catch-all for the embedded SPA. In Go 1.22+ ServeMux the more specific
	// patterns above (including /health, /query, /docs and /openapi.json) still
	// win, so the UI only handles paths no API route claims. A page carries no
	// data, so both the SPA and the placeholder are public.
	if webui.Available() {
		routes = append(routes, route{http.MethodGet, "/", webui.Handler().ServeHTTP, authPublic})
	} else {
		routes = append(routes, route{http.MethodGet, "/{$}", webui.Placeholder().ServeHTTP, authPublic})
	}
	return routes
}
