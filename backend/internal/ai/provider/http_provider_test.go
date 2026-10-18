package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const testKey = "sk-super-secret-key-do-not-log"

func TestHTTPProviderSuccess(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("Authorization = %q, want a bearer token", got)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		captured, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": `{"title":"from the model"}`}}},
			"usage":   map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
		})
	}))
	defer srv.Close()

	p := NewHTTPProvider(HTTPProviderConfig{
		BaseURL:        srv.URL,
		Model:          "gpt-test",
		APIKey:         testKey,
		Timeout:        2 * time.Second,
		MaxOutputBytes: 4096,
	})
	if !p.Descriptor().Configured || p.MissingCredential() {
		t.Fatalf("descriptor = %+v, want configured with a credential", p.Descriptor())
	}
	resp, err := p.Complete(context.Background(), ProviderRequest{
		Kind:   "challenge",
		System: "be careful",
		Prompt: "make a bundle",
		Schema: []byte(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if resp.Text != `{"title":"from the model"}` {
		t.Fatalf("text = %q", resp.Text)
	}

	var sent map[string]any
	if err := json.Unmarshal(captured, &sent); err != nil {
		t.Fatalf("the request body was not JSON: %v", err)
	}
	if sent["model"] != "gpt-test" {
		t.Errorf("model = %v, want gpt-test", sent["model"])
	}
	if _, ok := sent["response_format"]; !ok {
		t.Error("a schema was supplied but response_format was not sent")
	}
	msgs, ok := sent["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %v, want a system and a user message", sent["messages"])
	}
	user := msgs[1].(map[string]any)["content"].(string)
	if !strings.Contains(user, "make a bundle") || !strings.Contains(user, `"type":"object"`) {
		t.Errorf("the user message did not carry the prompt and the schema: %q", user)
	}
}

// A repair pass must tell the model what was wrong. Before this test existed,
// UserContent dropped Previous and Failure, so the repair asked the same
// question twice and the live audit measured two identical rejections.
func TestHTTPProviderCarriesTheRepairContext(t *testing.T) {
	var captured struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": `{"title":"fixed"}`}}},
		})
	}))
	defer srv.Close()

	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: testKey, Timeout: 2 * time.Second})
	if _, err := p.Complete(context.Background(), ProviderRequest{
		Kind:     "repair",
		Prompt:   "make a bundle",
		Previous: `{"title":"four hints","hints":["a","b","c","d"]}`,
		Failure:  "validate_artifact: The generated challenge did not pass validation.; 4 hints exceeds the limit of 3",
	}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("messages = %d, want a system and a user message", len(captured.Messages))
	}
	user := captured.Messages[1].Content
	if !strings.Contains(user, "make a bundle") {
		t.Errorf("the user message lost the prompt: %q", user)
	}
	if !strings.Contains(user, `"four hints"`) {
		t.Errorf("the repair did not carry the previous reply: %q", user)
	}
	if !strings.Contains(user, "4 hints exceeds the limit of 3") {
		t.Errorf("the repair did not name the failure: %q", user)
	}
}

// A previous reply can be as large as the output cap, so the repair context is
// bounded — on a rune boundary, because a slice in the middle of a multi-byte
// character produces a body some servers reject outright.
func TestUserContentClipsTheRepairContext(t *testing.T) {
	long := strings.Repeat("é", maxPreviousContextChars*2)
	got := UserContent(ProviderRequest{Prompt: "p", Previous: long, Failure: "f"})
	if len(got) >= len(long) {
		t.Fatalf("the previous reply was not clipped: %d bytes", len(got))
	}
	if !strings.Contains(got, "(truncated)") {
		t.Error("the clipped reply does not say it was truncated")
	}
	if !utf8.ValidString(got) {
		t.Error("the clipped reply is not valid UTF-8")
	}
}

func TestHTTPProviderRedactsTheKeyFromErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A hostile or chatty server echoes the credential back. It must not
		// survive into the error the caller sees.
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key `+testKey+`","dsn":"postgres://user:`+testKey+`@db/x"}}`)
	}))
	defer srv.Close()

	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: testKey})
	_, err := p.Complete(context.Background(), ProviderRequest{Kind: "challenge", Prompt: "p"})
	if err == nil {
		t.Fatal("a 500 should be an error")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("the API key leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("the error does not say what the server answered: %v", err)
	}
}

func TestHTTPProviderTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: testKey, Timeout: 50 * time.Millisecond})
	start := time.Now()
	_, err := p.Complete(context.Background(), ProviderRequest{Kind: "challenge", Prompt: "p"})
	if err == nil {
		t.Fatal("a slow provider should time out")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the timeout took %s; the configured deadline was 50ms", elapsed)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("the API key leaked into the error: %v", err)
	}
}

func TestHTTPProviderRejectsAnOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 4096))
	}))
	defer srv.Close()

	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: testKey, MaxOutputBytes: 64})
	_, err := p.Complete(context.Background(), ProviderRequest{Kind: "challenge", Prompt: "p"})
	if err == nil {
		t.Fatal("an oversized reply should be an error, not a truncated parse")
	}
	if !strings.Contains(err.Error(), "64-byte limit") {
		t.Fatalf("error = %v, want an over-budget error naming the limit", err)
	}

	// A per-request budget can tighten the client's ceiling.
	p = NewHTTPProvider(HTTPProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: testKey, MaxOutputBytes: 4096})
	if _, err := p.Complete(context.Background(), ProviderRequest{Kind: "challenge", Prompt: "p", MaxOutputBytes: 16}); err == nil {
		t.Fatal("the request's own byte budget was ignored")
	}
}

func TestHTTPProviderRejectsAnUnparsableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not json at all")
	}))
	defer srv.Close()

	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: testKey})
	_, err := p.Complete(context.Background(), ProviderRequest{Kind: "challenge", Prompt: "p"})
	if err == nil {
		t.Fatal("an unparsable body should be an error")
	}
	if !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("error = %v, want an unparsable-reply error", err)
	}
}

func TestHTTPProviderWithoutAKeyIsUnavailable(t *testing.T) {
	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: "http://127.0.0.1:1/v1", Model: "m"})
	if !p.Descriptor().Configured {
		t.Fatal("an addressable provider must report configured")
	}
	if !p.MissingCredential() {
		t.Fatal("MissingCredential() = false, want true without a key")
	}
	if _, err := p.Complete(context.Background(), ProviderRequest{Kind: "challenge", Prompt: "p"}); err == nil {
		t.Fatal("Complete() should fail without a key")
	}
}

func TestHTTPProviderDescriptorReportsConfiguration(t *testing.T) {
	if d := NewHTTPProvider(HTTPProviderConfig{}).Descriptor(); d.Configured {
		t.Fatal("a provider with no address or model must not report configured")
	}
	if d := NewHTTPProvider(HTTPProviderConfig{BaseURL: "http://x"}).Descriptor(); d.Configured {
		t.Fatal("a provider with no model must not report configured")
	}
	d := NewHTTPProvider(HTTPProviderConfig{BaseURL: "http://x", Model: "m"}).Descriptor()
	if !d.Configured || d.Name != "openai-compatible" || d.Model != "m" || d.Deterministic {
		t.Fatalf("descriptor = %+v", d)
	}
}

/* The schema and the empty reply.
 *
 * These tests pin two facts about the one-shot request: a supplied schema is
 * sent as response_format beside the system and user messages, and a reply
 * with no content is an error rather than an empty success. */

// TestHTTPProviderAsksForTheSchema: a schema is sent both in the prompt and
// as response_format, and the request is the system+user pair.
func TestHTTPProviderAsksForTheSchema(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": `{"ok":true}`}}},
		})
	}))
	defer srv.Close()

	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: testKey})
	if _, err := p.Complete(context.Background(), ProviderRequest{
		Kind: "course", System: "s", Prompt: "p", Schema: []byte(`{"type":"object"}`),
	}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if _, present := captured["response_format"]; !present {
		t.Error("a schema was supplied but response_format was not sent")
	}
	msgs := captured["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want the system and user pair", len(msgs))
	}
}

// TestHTTPProviderRejectsAnEmptyReply: a choice with no content is an error.
func TestHTTPProviderRejectsAnEmptyReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": ""}, "finish_reason": "stop"}},
		})
	}))
	defer srv.Close()

	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: testKey})
	_, err := p.Complete(context.Background(), ProviderRequest{Kind: "course", Prompt: "p"})
	if err == nil {
		t.Fatal("an empty reply should be an error")
	}
	if !strings.Contains(err.Error(), "content") {
		t.Fatalf("error = %v, want it to name what was missing", err)
	}
}
