package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/aiconfig"
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
		KeySet    bool `json:"key_set"`
		TimeoutMS int  `json:"timeout_ms"`
	}
	mustDecode(t, w, &kept)
	if !kept.KeySet {
		t.Fatal("an empty key on save dropped the stored key")
	}
	// A save that names no timeout gets the shipped default; it is the same
	// number config.Defaults() gives the boot path (ai.DefaultCallTimeoutMS).
	if kept.TimeoutMS != ai.DefaultCallTimeoutMS {
		t.Fatalf("timeout after a save that omitted it = %d, want the default %d", kept.TimeoutMS, ai.DefaultCallTimeoutMS)
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
	cfg, err := aiconfig.AIConfigFromSetting(raw, base, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("apply setting: %v", err)
	}
	if d := cfg.Provider.Descriptor(); d.Name != "openai-compatible" || d.Model != "inhouse-4" {
		t.Fatalf("boot-applied provider = %+v, want the persisted one", d)
	}
	// A broken row keeps the config file's provider and reports why.
	if _, err := aiconfig.AIConfigFromSetting(`{"provider":`, base, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("a broken setting applied without error")
	}
	if cfg.Provider.Descriptor().Name != "openai-compatible" {
		t.Fatal("a broken setting modified the base configuration")
	}
}

// TestAdminAIModelsUseLowercaseWireKeys pins the catalog's wire shape. The
// admin picker reads provider/model/name in lowercase, so CatalogEntry owns
// those JSON names and the handler returns the catalog directly. A tag lost in
// a refactor would leak Provider/Model/Name here.
func TestAdminAIModelsUseLowercaseWireKeys(t *testing.T) {
	s, adminToken, learnerToken, _, _ := adminServer(t)

	if w := doAs(t, s, learnerToken, http.MethodGet, "/admin/ai/models", ""); w.Code != http.StatusForbidden {
		t.Fatalf("learner status = %d, want 403", w.Code)
	}

	w := doAs(t, s, adminToken, http.MethodGet, "/admin/ai/models", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var body struct {
		OK     bool                         `json:"ok"`
		Models []map[string]json.RawMessage `json:"models"`
	}
	mustDecode(t, w, &body)
	if !body.OK || len(body.Models) == 0 {
		t.Fatalf("catalog = %d entries, want a non-empty ok list", len(body.Models))
	}
	for i, m := range body.Models {
		if len(m) != 3 {
			t.Fatalf("entry %d = %v, want exactly provider/model/name", i, m)
		}
		for _, key := range []string{"provider", "model", "name"} {
			if _, ok := m[key]; !ok {
				t.Fatalf("entry %d = %v, missing lowercase key %q", i, m, key)
			}
		}
	}
}

/* ---------- per-account preferences ---------- */

// TestPreferencesArePerAccountAndOwnerScoped: two accounts keep independent
// documents; a learner without a document reads null, never an invented
// default; and the account comes from the session, never the body.
func TestPreferencesArePerAccountAndOwnerScoped(t *testing.T) {
	s, adminToken, learnerToken, _, _ := adminServer(t)

	// No document yet: null, not an invented default.
	w := doAs(t, s, learnerToken, http.MethodGet, "/preferences", "")
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", w.Code, w.Body.String())
	}
	var empty struct {
		OK          bool            `json:"ok"`
		Preferences json.RawMessage `json:"preferences"`
	}
	mustDecode(t, w, &empty)
	if !empty.OK || string(empty.Preferences) != "null" {
		t.Fatalf("first read = %s, want null preferences", empty.Preferences)
	}

	// The learner saves one document.
	body := `{"theme":"midnight","customThemes":[{"id":"mine","name":"Mine","accent":"#ff5c8a"}]}`
	if w := doAs(t, s, learnerToken, http.MethodPut, "/preferences", body); w.Code != http.StatusOK {
		t.Fatalf("put status = %d body = %s", w.Code, w.Body.String())
	}

	// The learner reads their own document back, whole. The document is
	// compared as JSON: every backend stores it in a JSON column that
	// normalizes key order and whitespace, so the value is what must survive,
	// not its original bytes.
	w = doAs(t, s, learnerToken, http.MethodGet, "/preferences", "")
	mustDecode(t, w, &empty)
	if !sameJSONDocument(t, empty.Preferences, []byte(body)) {
		t.Fatalf("read-back = %s, want the saved document", empty.Preferences)
	}

	// The admin's account is independent: still no document.
	w = doAs(t, s, adminToken, http.MethodGet, "/preferences", "")
	mustDecode(t, w, &empty)
	if string(empty.Preferences) != "null" {
		t.Fatalf("admin read = %s, want null (documents are per account)", empty.Preferences)
	}

	// Anonymous is refused like every authed route.
	if w := do(t, s, http.MethodGet, "/preferences", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anon status = %d, want 401", w.Code)
	}
}

// TestPreferencesReplaceWholesaleAndRejectMalformed: PUT replaces the whole
// document, and a malformed body is a 400 that changes nothing.
func TestPreferencesReplaceWholesaleAndRejectMalformed(t *testing.T) {
	s, learnerToken, _, _, _ := adminServer(t)
	if w := doAs(t, s, learnerToken, http.MethodPut, "/preferences", `{"theme":"midnight"}`); w.Code != http.StatusOK {
		t.Fatalf("first put = %d", w.Code)
	}
	if w := doAs(t, s, learnerToken, http.MethodPut, "/preferences", `{"theme":"ember"}`); w.Code != http.StatusOK {
		t.Fatalf("second put = %d", w.Code)
	}
	w := doAs(t, s, learnerToken, http.MethodGet, "/preferences", "")
	var got struct {
		Preferences json.RawMessage `json:"preferences"`
	}
	mustDecode(t, w, &got)
	if string(got.Preferences) != `{"theme":"ember"}` {
		t.Fatalf("document = %s, want the wholesale replacement", got.Preferences)
	}
	if w := doAs(t, s, learnerToken, http.MethodPut, "/preferences", `{"theme":`); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed put = %d, want 400", w.Code)
	}
	// And the stored document survived the malformed attempt.
	w = doAs(t, s, learnerToken, http.MethodGet, "/preferences", "")
	mustDecode(t, w, &got)
	if string(got.Preferences) != `{"theme":"ember"}` {
		t.Fatalf("document after malformed put = %s, want it untouched", got.Preferences)
	}
}

// sameJSONDocument compares two JSON documents by value. A stored preference
// document comes back from a JSON column, which normalizes key order and
// whitespace on every backend, so tests assert on the document, not its bytes.
func sameJSONDocument(t *testing.T, got, want []byte) bool {
	t.Helper()
	var gv, wv any
	if err := json.Unmarshal(got, &gv); err != nil {
		t.Fatalf("decode stored document %s: %v", got, err)
	}
	if err := json.Unmarshal(want, &wv); err != nil {
		t.Fatalf("decode expected document %s: %v", want, err)
	}
	return reflect.DeepEqual(gv, wv)
}
