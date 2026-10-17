package piai

import (
	"context"
	"strings"
	"testing"

	"github.com/sky-valley/pi/ai"
	"github.com/sky-valley/pi/ai/providers"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
)

// The test registers a scripted Api provider under a test-only Api, so the
// adapter's mapping is exercised end to end — ProviderRequest in, pi Context
// out, AssistantMessage back through the seam — with no network and no
// dependency on any catalog provider's endpoint.

var scripted = struct {
	seen ai.TranscriptContext
}{}

func registerScripted(t *testing.T, fn func(req ai.TranscriptContext) *ai.AssistantMessage) ai.Api {
	t.Helper()
	api := ai.Api("querycraft-test-" + t.Name())
	ai.RegisterApiProvider(ai.ApiProvider{
		Api: api,
		StreamSimple: func(ctx context.Context, model *ai.Model, req ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			scripted.seen = req
			s := ai.NewAssistantMessageEventStream()
			msg := fn(req)
			s.Push(ai.AssistantMessageEvent{Type: ai.EventDone, Reason: msg.StopReason, Message: msg})
			s.End()
			return s
		},
	}, "querycraft-test")
	t.Cleanup(func() { ai.UnregisterApiProviders("querycraft-test") })
	return api
}

func scriptedProvider(t *testing.T, api ai.Api) *PIAI {
	t.Helper()
	// Built directly rather than through New: New resolves against the catalog,
	// and this test's model is the scripted Api itself.
	p := &PIAI{cfg: Config{Provider: "test", Model: "scripted"}}
	// Point the adapter at the scripted Api: the model record is the adapter's
	// own output, and CompleteSimple resolves the Api off it.
	p.model = &ai.Model{ID: "scripted", Name: "Scripted", Api: api, Provider: "test", Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100}
	p.api = api
	p.provID = "test"
	return p
}

func TestCompleteMapsSingleShotRequest(t *testing.T) {
	api := registerScripted(t, func(req ai.TranscriptContext) *ai.AssistantMessage {
		return providers.FauxAssistantMessage(ai.ContentList{providers.FauxText(`{"title":"Joins"}`)}, ai.StopStop)
	})
	p := scriptedProvider(t, api)

	resp, err := p.Complete(context.Background(), provider.ProviderRequest{
		Kind:   "challenge",
		System: "You are a SQL curriculum writer.",
		Prompt: "Draft one exercise about self-joins.",
		Schema: []byte(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	// The system prompt rides the initial system message, and the user message
	// is the proven transport's UserContent: prompt, then schema instruction.
	seen := scripted.seen
	if len(seen.Messages) != 2 {
		t.Fatalf("messages = %d, want system + user", len(seen.Messages))
	}
	sys, ok := ai.GetInitialSystemMessage(seen.Messages)
	if !ok || !strings.Contains(sys.Content[0].(ai.TextContent).Text, "curriculum writer") {
		t.Errorf("system prompt did not ride the transcript: %+v", sys)
	}
	user := seen.Messages[1].(ai.UserMessage).Content[0].(ai.TextContent).Text
	if !strings.Contains(user, "self-joins") {
		t.Errorf("user message lost the prompt: %q", user)
	}
	if !strings.Contains(user, "Reply with a single JSON object") {
		t.Errorf("user message lost the schema instruction: %q", user)
	}

	if resp.Text != `{"title":"Joins"}` {
		t.Errorf("text = %q, want the scripted reply", resp.Text)
	}
	if resp.Provider != "pi-ai" || resp.FinishReason != string(ai.StopStop) {
		t.Errorf("response metadata = %+v", resp)
	}
}

func TestCompleteCarriesRepairContextAndTokens(t *testing.T) {
	registerScripted(t, func(req ai.TranscriptContext) *ai.AssistantMessage {
		m := providers.FauxAssistantMessage(ai.ContentList{providers.FauxText("fixed")}, ai.StopStop)
		m.Usage = ai.Usage{Input: 120, Output: 45}
		return m
	})
	p := scriptedProvider(t, registerScriptedName(t))

	resp, err := p.Complete(context.Background(), provider.ProviderRequest{
		Prompt:   "try again",
		Previous: `{"bad": true}`,
		Failure:  "row order mismatch",
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	user := ""
	for _, m := range scripted.seen.Messages {
		if u, ok := m.(ai.UserMessage); ok {
			user = u.Content[0].(ai.TextContent).Text
		}
	}
	if !strings.Contains(user, "rejected") || !strings.Contains(user, "row order mismatch") {
		t.Errorf("the repair context did not reach the prompt: %q", user)
	}
	if resp.TokensIn != 120 || resp.TokensOut != 45 {
		t.Errorf("usage = %d/%d, want the scripted counts", resp.TokensIn, resp.TokensOut)
	}
}

// registerScriptedName is the second registration in one test function: t.Name
// collides with the first call, and each test registers exactly once per name.
func registerScriptedName(t *testing.T) ai.Api {
	t.Helper()
	return registerScripted(t, func(req ai.TranscriptContext) *ai.AssistantMessage {
		m := providers.FauxAssistantMessage(ai.ContentList{providers.FauxText("fixed")}, ai.StopStop)
		m.Usage = ai.Usage{Input: 120, Output: 45}
		return m
	})
}

func TestCompleteMapsToolConversation(t *testing.T) {
	api := registerScripted(t, func(req ai.TranscriptContext) *ai.AssistantMessage {
		return providers.FauxAssistantMessage(ai.ContentList{providers.FauxText("saved")}, ai.StopStop)
	})
	p := scriptedProvider(t, api)

	resp, err := p.Complete(context.Background(), provider.ProviderRequest{
		Messages: []provider.ProviderMessage{
			{Role: "system", Content: "Chapter phase."},
			{Role: "user", Content: "Write chapter one."},
			{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
				{ID: "call-1", Function: provider.ToolCallFunction{Name: "save_chapter", Arguments: `{"title":"One"}`}},
			}},
			{Role: "tool", ToolCallID: "call-1", Content: `{"stored": true}`},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resp.Text != "saved" {
		t.Errorf("text = %q", resp.Text)
	}

	seen := scripted.seen
	if len(seen.Messages) != 4 {
		t.Fatalf("conversation = %d messages, want 4", len(seen.Messages))
	}
	if _, ok := seen.Messages[0].(ai.SystemMessage); !ok {
		t.Errorf("message 0 = %T, want the system message", seen.Messages[0])
	}
	asst, ok := seen.Messages[2].(ai.AssistantMessage)
	if !ok {
		t.Fatalf("message 2 = %T, want an assistant message", seen.Messages[2])
	}
	var call *ai.ToolCall
	for _, c := range asst.Content {
		if tc, isCall := c.(ai.ToolCall); isCall && tc.Name == "save_chapter" {
			call = &tc
		}
	}
	if call == nil {
		t.Fatal("the assistant's tool call did not survive the mapping")
	}
	if call.ID != "call-1" || call.Arguments["title"] != "One" {
		t.Errorf("tool call = %+v, want call-1 with the parsed arguments", call)
	}
	tool, ok := seen.Messages[3].(ai.ToolResultMessage)
	if !ok {
		t.Fatalf("message 3 = %T, want a tool result", seen.Messages[3])
	}
	if tool.ToolCallID != "call-1" {
		t.Errorf("tool result id = %q, want call-1", tool.ToolCallID)
	}
}

func TestCompleteReportsProviderErrors(t *testing.T) {
	api := registerScripted(t, func(req ai.TranscriptContext) *ai.AssistantMessage {
		m := providers.FauxAssistantMessage(nil, ai.StopError)
		m.ErrorMessage = "the endpoint refused the key"
		return m
	})
	p := scriptedProvider(t, api)
	_, err := p.Complete(context.Background(), provider.ProviderRequest{Prompt: "hi"})
	if err == nil || !strings.Contains(err.Error(), "refused the key") {
		t.Fatalf("error = %v, want the provider's message surfaced", err)
	}
}

func TestDescriptorNamesTheModel(t *testing.T) {
	p := scriptedProvider(t, registerScripted(t, func(req ai.TranscriptContext) *ai.AssistantMessage {
		return providers.FauxAssistantMessage(ai.ContentList{providers.FauxText("x")}, ai.StopStop)
	}))
	d := p.Descriptor()
	if d.Name != "pi-ai" || d.Model != "test/scripted" || !d.Configured {
		t.Errorf("descriptor = %+v", d)
	}
}
