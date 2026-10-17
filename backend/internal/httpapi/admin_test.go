package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/auth"
	"github.com/querycraft/sqlquery/backend/internal/schema"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

// adminServer seeds the default administrator (empty password) plus one
// learner, and stores one course per account, exactly the state the manage
// surface's inventory needs to prove itself against.
func adminServer(t *testing.T) (*Server, string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := auth.EnsureDefaultAdmin(ctx, st, auth.DefaultAdmin{
		Email:    "admin@querycraft.local",
		Password: "",
		Name:     "Administrator",
	}, log); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	s := New(&fakeExecutor{}, st, Config{MaxRows: 100, TimeoutMS: 1000, MaxConcurrent: 2}, log)
	s.UseAI(ai.Config{Depot: st})

	w := do(t, s, http.MethodPost, "/auth/login", `{"email":"admin@querycraft.local","password":""}`)
	if w.Code != http.StatusOK {
		t.Fatalf("admin login status = %d body = %s", w.Code, w.Body.String())
	}
	adminToken := decodeAuthResponse(t, w.Body.Bytes()).Token
	learnerToken := signupUser(t, s, "courses-learner@example.test")
	learnerID := userIDFor(t, s, learnerToken)
	adminID := userIDFor(t, s, adminToken)
	now := time.Now().UTC()
	seedCourse(t, s.aiCourseDepot(), adminID, "course-admin", "Joins for Admins", now)
	seedCourse(t, s.aiCourseDepot(), learnerID, "course-learner", "Windows for Learners", now.Add(time.Second))
	return s, adminToken, learnerToken, "course-admin", "course-learner"
}

func TestAdminCoursesListEveryOwnerForAnAdmin(t *testing.T) {
	s, adminToken, _, _, _ := adminServer(t)
	w := doAs(t, s, adminToken, http.MethodGet, "/admin/courses", "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin list status = %d body = %s", w.Code, w.Body.String())
	}
	var list struct {
		OK      bool                  `json:"ok"`
		Courses []schema.CourseRecord `json:"courses"`
	}
	mustDecode(t, w, &list)
	if !list.OK || len(list.Courses) != 2 {
		t.Fatalf("admin list = %+v, want both owners' courses", list)
	}
	ids := map[string]string{}
	for _, rec := range list.Courses {
		ids[rec.Plan.ID] = rec.OwnerID
	}
	if ids["course-admin"] == "" || ids["course-learner"] == "" {
		t.Fatalf("owner ids must reach an administrator: %+v", list.Courses)
	}
}

func TestAdminCoursesRefuseALearnerAndAnAnonymousCaller(t *testing.T) {
	s, _, learnerToken, _, _ := adminServer(t)
	w := doAs(t, s, learnerToken, http.MethodGet, "/admin/courses", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("learner status = %d body = %s, want 403", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ADMIN_REQUIRED") {
		t.Fatalf("learner refusal does not name ADMIN_REQUIRED: %s", w.Body.String())
	}
	anon := do(t, s, http.MethodGet, "/admin/courses", "")
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", anon.Code)
	}
	if strings.Contains(anon.Body.String(), "ADMIN_REQUIRED") {
		t.Fatalf("a 401 must not claim ADMIN_REQUIRED: %s", anon.Body.String())
	}
}

func TestAdminCoursesListIsEmptyNotInventedWhenAIOff(t *testing.T) {
	s, adminToken, _, _, _ := adminServer(t)
	s.UseAI(ai.Config{Depot: nil})
	w := doAs(t, s, adminToken, http.MethodGet, "/admin/courses", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("ai-off status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"courses":[]`) {
		t.Fatalf("ai-off body = %s, want an empty courses array", w.Body.String())
	}
}