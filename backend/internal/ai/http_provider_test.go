package ai

import (
	"context"
	"encoding/json"
	"errors"
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
	if resp.Provider != "openai-compatible" || resp.Model != "gpt-test" {
		t.Fatalf("response attribution = %+v", resp)
	}
	if resp.TokensIn != 11 || resp.TokensOut != 7 {
		t.Fatalf("usage = %d/%d, want 11/7", resp.TokensIn, resp.TokensOut)
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
// userContent dropped Previous and Failure, so the repair asked the same
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
	got := userContent(ProviderRequest{Prompt: "p", Previous: long, Failure: "f"})
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
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("error = %v, want it to wrap ErrProviderUnavailable", err)
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
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("error = %v, want it to wrap ErrProviderUnavailable", err)
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
	if !errors.Is(err, ErrProviderUnavailable) || !strings.Contains(err.Error(), "64-byte limit") {
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
	if !errors.Is(err, ErrProviderUnavailable) || !strings.Contains(err.Error(), "valid JSON") {
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
	if _, err := p.Complete(context.Background(), ProviderRequest{Kind: "challenge", Prompt: "p"}); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Complete() error = %v, want ErrProviderUnavailable", err)
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

/* The tool-calling half of the wire.
 *
 * The course agent talks to the provider in a conversation with functions
 * advertised, which is a different request shape from the one-shot JSON
 * generation the challenge path uses. These tests pin the three facts the
 * agent depends on: the conversation is sent as it stands, tools are sent
 * without a response_format (the pair is rejected by the endpoint), and a
 * tool-calling reply is parsed rather than mistaken for an empty one. */

// TestHTTPProviderSendsToolsAndParsesAToolCall drives the conversation shape.
func TestHTTPProviderSendsToolsAndParsesAToolCall(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": "",
					"tool_calls": []any{map[string]any{
						"id":       "call_1",
						"type":     "function",
						"function": map[string]any{"name": "save_chapter", "arguments": `{"id":"ch.1"}`},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 6},
		})
	}))
	defer srv.Close()

	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: testKey, Timeout: 2 * time.Second})
	tools := []Tool{{
		Type: "function",
		Function: ToolFunction{
			Name:        "save_chapter",
			Description: "Save one chapter.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
	}}
	resp, err := p.Complete(context.Background(), ProviderRequest{
		Kind:   "course",
		Prompt: "the course prompt",
		Schema: []byte(`{"type":"object"}`),
		Messages: []ProviderMessage{
			{Role: "system", Content: "you write chapters"},
			{Role: "user", Content: "write chapter one"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_0", Type: "function", Function: ToolCallFunction{Name: "save_chapter", Arguments: "{}"}}}},
			{Role: "tool", ToolCallID: "call_0", Content: `{"status":"refused"}`},
		},
		Tools: tools,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Name != "save_chapter" {
		t.Fatalf("tool calls = %+v, want the one the server sent", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Function.Arguments != `{"id":"ch.1"}` {
		t.Fatalf("arguments = %q, want them passed through unparsed", resp.ToolCalls[0].Function.Arguments)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q, want tool_calls", resp.FinishReason)
	}
	if resp.Text != "" {
		t.Fatalf("text = %q, want empty: a tool-calling reply carries its answer in tool_calls", resp.Text)
	}

	// Tools and response_format are mutually exclusive on the wire, so a
	// request that advertises tools must not carry the schema as a format even
	// when the caller supplied one.
	if _, present := captured["response_format"]; present {
		t.Error("response_format was sent alongside tools; the endpoint rejects the pair")
	}
	if captured["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want auto", captured["tool_choice"])
	}
	if _, present := captured["tools"]; !present {
		t.Error("the tools were not sent")
	}

	msgs, ok := captured["messages"].([]any)
	if !ok || len(msgs) != 4 {
		t.Fatalf("messages = %v, want the 4 turns the caller supplied", captured["messages"])
	}
	if got := msgs[3].(map[string]any); got["role"] != "tool" || got["tool_call_id"] != "call_0" {
		t.Errorf("the tool turn lost its call id: %v", got)
	}
	if got := msgs[2].(map[string]any); got["tool_calls"] == nil {
		t.Errorf("the assistant turn lost its tool calls: %v", got)
	}
}

// TestHTTPProviderWithoutToolsStillAsksForTheSchema keeps the one-shot path
// unchanged: a request with no tools is still the system+user pair with the
// prompt and the schema in it.
func TestHTTPProviderWithoutToolsStillAsksForTheSchema(t *testing.T) {
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
	if _, present := captured["tools"]; present {
		t.Error("a request with no tools advertised them anyway")
	}
	if _, present := captured["response_format"]; !present {
		t.Error("a schema was supplied but response_format was not sent")
	}
	msgs := captured["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want the system and user pair", len(msgs))
	}
}

// TestHTTPProviderCarriesAMalformedToolCallThrough: the arguments of a call are
// untrusted text, and it is the caller — not the transport — that decides
// whether they describe a chapter. Dropping them would hide a repair the model
// needs to be told about.
func TestHTTPProviderCarriesAMalformedToolCallThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": "",
					"tool_calls": []any{map[string]any{
						"id":       "call_1",
						"type":     "function",
						"function": map[string]any{"name": "save_chapter", "arguments": "{not json"},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: testKey})
	resp, err := p.Complete(context.Background(), ProviderRequest{
		Kind: "course", Prompt: "p", Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "save_chapter"}}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v; the transport must not parse the arguments", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Arguments != "{not json" {
		t.Fatalf("tool calls = %+v, want the malformed arguments passed through", resp.ToolCalls)
	}
}

// TestHTTPProviderRejectsAReplyWithNeitherTextNorCalls: a choice with no
// content is now only an error when it also asked for nothing.
func TestHTTPProviderRejectsAReplyWithNeitherTextNorCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": ""}, "finish_reason": "stop"}},
		})
	}))
	defer srv.Close()

	p := NewHTTPProvider(HTTPProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: testKey})
	_, err := p.Complete(context.Background(), ProviderRequest{
		Kind: "course", Prompt: "p", Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "save_chapter"}}},
	})
	if err == nil {
		t.Fatal("an empty reply with no tool calls should be an error")
	}
	if !errors.Is(err, ErrProviderUnavailable) || !strings.Contains(err.Error(), "tool_calls") {
		t.Fatalf("error = %v, want it to name what was missing", err)
	}
}
