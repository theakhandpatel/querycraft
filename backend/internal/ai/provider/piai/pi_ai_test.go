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
}

func TestCompleteCarriesTheRepairContext(t *testing.T) {
	registerScripted(t, func(req ai.TranscriptContext) *ai.AssistantMessage {
		m := providers.FauxAssistantMessage(ai.ContentList{providers.FauxText("fixed")}, ai.StopStop)
		m.Usage = ai.Usage{Input: 120, Output: 45}
		return m
	})
	p := scriptedProvider(t, registerScriptedName(t))

	_, err := p.Complete(context.Background(), provider.ProviderRequest{
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
