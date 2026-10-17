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
	s.UseAI(ai.Config{Provider: ai.NewFakeProvider(), Depot: st})

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

/* ---------- the AI configuration surface ---------- */

// TestAdminAIReportsTheBootProvider: with nothing saved, the manage surface
// reads the boot configuration and says so.
func TestAdminAIReportsTheBootProvider(t *testing.T) {
	s, adminToken, _, _, _ := adminServer(t)
	w := doAs(t, s, adminToken, http.MethodGet, "/admin/ai", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var view struct {
		OK       bool   `json:"ok"`
		Provider string `json:"provider"`
		KeySet   bool   `json:"key_set"`
		Source   string `json:"source"`
	}
	mustDecode(t, w, &view)
	if !view.OK || view.Provider != "fake" || view.Source != "boot" || view.KeySet {
		t.Fatalf("view = %+v, want the fake boot provider with source boot", view)
	}
}

// TestAdminAISaveValidatesAndMasksTheKey: a save persists, swaps the running
// configuration, and the key never travels back in a body.
func TestAdminAISaveValidatesAndMasksTheKey(t *testing.T) {
	s, adminToken, learnerToken, _, _ := adminServer(t)

	// A learner is refused.
	if w := doAs(t, s, learnerToken, http.MethodPut, "/admin/ai", `{"provider":"fake"}`); w.Code != http.StatusForbidden {
		t.Fatalf("learner put status = %d, want 403", w.Code)
	}
	if w := doAs(t, s, learnerToken, http.MethodGet, "/admin/ai", ""); w.Code != http.StatusForbidden {
		t.Fatalf("learner get status = %d, want 403", w.Code)
	}

	// An unknown provider and an incomplete openai-compatible configuration
	// are refused before anything is persisted or swapped.
	if w := doAs(t, s, adminToken, http.MethodPut, "/admin/ai", `{"provider":"claude"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider status = %d, want 400", w.Code)
	}
	if w := doAs(t, s, adminToken, http.MethodPut, "/admin/ai", `{"provider":"openai-compatible","base_url":"http://llm.test/v1"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("openai-compatible without model status = %d, want 400", w.Code)
	}

	// The valid save swaps the running configuration.
	body := `{"provider":"openai-compatible","base_url":"http://llm.test/v1","model":"inhouse-4","key":"sk-secret","timeout_ms":90000}`
	w := doAs(t, s, adminToken, http.MethodPut, "/admin/ai", body)
	if w.Code != http.StatusOK {
		t.Fatalf("put status = %d body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-secret") {
		t.Fatal("the API key travelled back in the response body")
	}
	var view struct {
		OK        bool   `json:"ok"`
		Provider  string `json:"provider"`
		Model     string `json:"model"`
		BaseURL   string `json:"base_url"`
		KeySet    bool   `json:"key_set"`
		TimeoutMS int    `json:"timeout_ms"`
		Source    string `json:"source"`
	}
	mustDecode(t, w, &view)
	if !view.OK || view.Provider != "openai-compatible" || view.Model != "inhouse-4" || view.BaseURL != "http://llm.test/v1" || !view.KeySet || view.TimeoutMS != 90000 || view.Source != "database" {
		t.Fatalf("view = %+v, want the saved configuration from the database", view)
	}

	// The running configuration actually changed: the availability probe and
	// the per-request service read through the holder, not the boot field.
	cfg, ok := s.currentAI()
	if !ok {
		t.Fatal("no AI configuration after the swap")
	}
	if d := cfg.Provider.Descriptor(); d.Name != "openai-compatible" || d.Model != "inhouse-4" {
		t.Fatalf("live provider descriptor = %+v, want the swapped provider", d)
	}

	// A follow-up save without a key keeps the stored one: the key is
	// write-only, so a round-trip through the form cannot demand a retype.
	w = doAs(t, s, adminToken, http.MethodPut, "/admin/ai", `{"provider":"openai-compatible","base_url":"http://llm.test/v1","model":"inhouse-4"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("second put status = %d body = %s", w.Code, w.Body.String())
	}
	var kept struct {
		KeySet bool `json:"key_set"`
	}
	mustDecode(t, w, &kept)
	if !kept.KeySet {
		t.Fatal("an empty key on save dropped the stored key")
	}

	// Switching the provider off is a supported configuration, and the live
	// availability answer follows it.
	if w := doAs(t, s, adminToken, http.MethodPut, "/admin/ai", `{"provider":"off"}`); w.Code != http.StatusOK {
		t.Fatalf("off put status = %d", w.Code)
	}
	if av := s.aiAvailability(); av.Provider != "" || av.Available {
		t.Fatalf("availability after off = %+v, want the lab off", av)
	}
}

// TestAdminAISettingSurvivesBoot is the restart half of the contract: the
// boot path applies the persisted setting over the config file's provider.
func TestAdminAISettingSurvivesBoot(t *testing.T) {
	raw := `{"provider":"openai-compatible","base_url":"http://llm.test/v1","model":"inhouse-4","key":"sk-secret","timeout_ms":90000}`
	base := ai.Config{Provider: ai.NewFakeProvider()}
	cfg, err := AIConfigFromSetting(raw, base, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("apply setting: %v", err)
	}
	if d := cfg.Provider.Descriptor(); d.Name != "openai-compatible" || d.Model != "inhouse-4" {
		t.Fatalf("boot-applied provider = %+v, want the persisted one", d)
	}
	// A broken row keeps the config file's provider and reports why.
	if _, err := AIConfigFromSetting(`{"provider":`, base, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("a broken setting applied without error")
	}
	if cfg.Provider.Descriptor().Name != "openai-compatible" {
		t.Fatal("a broken setting modified the base configuration")
	}
}
