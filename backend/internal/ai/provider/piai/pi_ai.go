// Package piai implements the gateway's provider seam over sky-valley/pi's ai
// package — the Go port of pi's unified multi-provider LLM API.
//
// The split this package exists to honor: the wire primitives are a solved
// problem and not ours. pi/ai owns the Anthropic Messages, OpenAI completions
// and responses, and Google Gemini adapters, SSE parsing, Retry-After-aware
// retries, the embedded model catalog and token accounting. This package owns
// only the mapping: the gateway's ProviderRequest becomes one pi Context, and
// the reply's text, tool calls, stop reason and token counts come back as the
// gateway's own ProviderResponse.
//
// Two things deliberately stay out of here. The JSON-schema enforcement is the
// pipeline's: pi carries the prompt, the gateway grades the reply against the
// schema itself. And the API key is passed through to pi's request options
// exactly once — pi's adapters handle the header, the scrubbing and the retry
// policy, which is the reason this dependency was adopted (ADR 0005's reopening
// note: the primitives are commodity; the policy is not).
package piai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sky-valley/pi/ai"

	// The catalog adapters (anthropic-messages, openai-completions,
	// openai-responses, google-generative-ai) register themselves in init. The
	// blank import is pi's documented contract for using the ai package
	// without its coding package; without it, every CompleteSimple resolves
	// no provider for its Api.
	_ "github.com/sky-valley/pi/ai/providers"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
)

// Config selects the model. Provider+Model name a catalog entry (any of the
// embedded catalog's providers — anthropic, openai, google, openrouter, …).
// BaseURL, when set, targets a custom OpenAI-compatible server instead — a
// model gateway, vLLM, llama.cpp — and Provider is ignored.
type Config struct {
	Provider   string
	Model      string
	APIKey     string
	BaseURL    string
	TimeoutMS  int
	MaxRetries int
}

// PIAI is the seam implementation over pi/ai. It is safe for concurrent use:
// every Complete runs through pi's own client.
type PIAI struct {
	cfg    Config
	model  *ai.Model
	api    string
	provID string
}

// New resolves the model. Catalog mode looks the ref up in the embedded
// catalog and fails on an unknown entry, so a typo is a configuration error at
// construction, not a failure at first grade. Custom mode builds the model
// record itself, because a customer's server has no catalog entry to find.
func New(cfg Config) (*PIAI, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("pi-ai: model is required")
	}
	p := &PIAI{cfg: cfg}
	if cfg.BaseURL != "" {
		p.model = &ai.Model{
			ID:            cfg.Model,
			Name:          cfg.Model,
			Api:           "openai-completions",
			Provider:      "custom",
			BaseURL:       cfg.BaseURL,
			Input:         []string{"text"},
			ContextWindow: 128000,
			MaxTokens:     16384,
		}
		p.api = "openai-completions"
		p.provID = "custom"
		return p, nil
	}
	if cfg.Provider == "" {
		return nil, fmt.Errorf("pi-ai: provider is required (or set base_url for a custom server)")
	}
	model := ai.GetModel(cfg.Provider, cfg.Model)
	if model == nil {
		return nil, fmt.Errorf("pi-ai: unknown model %s/%s in the model catalog", cfg.Provider, cfg.Model)
	}
	p.model = model
	p.api = model.Api
	p.provID = string(model.Provider)
	return p, nil
}

// CatalogEntry is one chat model in the embedded catalog.
type CatalogEntry struct {
	Provider string
	Model    string
	Name     string
}

// Catalog lists every chat model pi's embedded catalog knows — the same list
// the provider resolver matches Model refs against, sourced from models.dev.
// The manage surface offers it as the provider picker.
func Catalog() []CatalogEntry {
	out := make([]CatalogEntry, 0, 1024)
	for _, prov := range ai.GetProviders() {
		for _, m := range ai.GetModels(prov) {
			if mt := string(m.Type); mt != "" && mt != "chat" {
				continue
			}
			out = append(out, CatalogEntry{Provider: prov, Model: m.ID, Name: m.Name})
		}
	}
	return out
}

// Descriptor implements provider.Provider.
func (p *PIAI) Descriptor() provider.ProviderDescriptor {
	name := "pi-ai"
	if p.model.BaseURL != "" {
		name = "pi-ai (custom)"
	}
	return provider.ProviderDescriptor{
		Name:       name,
		Model:      p.provID + "/" + p.model.ID,
		Configured: true,
	}
}

// Complete implements provider.Provider. It is one pi completion: the request
// becomes a Context, the reply's text and usage come back through the seam.
func (p *PIAI) Complete(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	aiCtx := p.toContext(req)
	opts := &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions{
				APIKey:     p.cfg.APIKey,
				TimeoutMs:  p.cfg.TimeoutMS,
				MaxRetries: p.cfg.MaxRetries,
			},
		},
	}
	msg := ai.CompleteSimple(ctx, p.model, aiCtx, opts)
	if msg == nil {
		return provider.ProviderResponse{}, fmt.Errorf("pi-ai: the provider returned no message")
	}
	if msg.ErrorMessage != "" {
		return provider.ProviderResponse{}, fmt.Errorf("pi-ai: %s", msg.ErrorMessage)
	}
	resp := provider.ProviderResponse{
		FinishReason: string(msg.StopReason),
		Provider:     "pi-ai",
		Model:        p.model.ID,
		TokensIn:     msg.Usage.Input,
		TokensOut:    msg.Usage.Output,
	}
	var text strings.Builder
	for _, c := range msg.Content {
		switch tc := c.(type) {
		case ai.TextContent:
			text.WriteString(tc.Text)
		case ai.ToolCall:
			args, err := json.Marshal(tc.Arguments)
			if err != nil {
				return provider.ProviderResponse{}, fmt.Errorf("pi-ai: tool call arguments: %w", err)
			}
			resp.ToolCalls = append(resp.ToolCalls, provider.ToolCall{
				ID: tc.ID,
				Function: provider.ToolCallFunction{
					Name:      tc.Name,
					Arguments: string(args),
				},
			})
		}
	}
	resp.Text = text.String()
	return resp, nil
}

// toContext maps the seam's request onto pi's Context. A single-shot request
// is the system prompt plus the user message the proven transport builds —
// UserContent carries the repair context and the schema instruction, so a
// pi-ai reply has exactly the shape the pipeline validates. A conversation is
// mapped turn by turn, including the tool turns the course agent writes.
func (p *PIAI) toContext(req provider.ProviderRequest) ai.Context {
	if len(req.Messages) == 0 {
		return ai.Context{
			SystemPrompt: req.System,
			Messages: []ai.Message{
				ai.UserMessage{Content: ai.ContentList{ai.TextContent{Text: provider.UserContent(req)}}},
			},
		}
	}
	out := make([]ai.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			out = append(out, ai.SystemMessage{Content: ai.ContentList{ai.TextContent{Text: m.Content}}})
		case "assistant":
			content := ai.ContentList{ai.TextContent{Text: m.Content}}
			for _, tc := range m.ToolCalls {
				args := map[string]any{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				content = append(content, ai.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args})
			}
			out = append(out, ai.AssistantMessage{Content: content})
		case "tool":
			out = append(out, ai.ToolResultMessage{
				ToolCallID: m.ToolCallID,
				Content:    ai.ContentList{ai.TextContent{Text: m.Content}},
			})
		default: // "user" and anything unrecognized reads as the learner's turn.
			out = append(out, ai.UserMessage{Content: ai.ContentList{ai.TextContent{Text: m.Content}}})
		}
	}
	return ai.Context{Messages: out}
}
