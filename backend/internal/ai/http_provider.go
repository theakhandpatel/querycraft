package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

// HTTPProvider is an OpenAI-compatible chat-completions client: the same wire
// shape OpenAI, most hosted clones and local servers (llama.cpp, Ollama's
// compatibility endpoint, vLLM) accept. It exists so the provider seam is
// pluggable in practice, not only in principle — the fake provider proves the
// pipeline, this one proves the wire.
//
// It uses net/http and encoding/json only. A vendor SDK would move the contract
// into a dependency and bring its own retry and timeout behaviour, which is
// exactly the surface §11.2 wants kept small and visible.
//
// Secrets: the API key is held in an unexported field, is sent only in the
// Authorization header, and is scrubbed from every error string this type
// builds. A key must never reach a log line, an HTTP response or a struct the
// caller can read back.
type HTTPProvider struct {
	baseURL string
	model   string
	apiKey  string
	timeout time.Duration
	maxOut  int
	client  *http.Client
}

// HTTPProviderConfig configures the client. HTTPClient is a seam for tests (and
// for a caller that needs its own transport/proxy); nil means a default client.
type HTTPProviderConfig struct {
	BaseURL        string
	Model          string
	APIKey         string
	Timeout        time.Duration
	MaxOutputBytes int
	HTTPClient     *http.Client
}

// NewHTTPProvider builds the client. A provider with no BaseURL or Model is not
// configured, and a provider with no key cannot be used — Descriptor and
// Service.Available report both rather than failing at call time.
func NewHTTPProvider(cfg HTTPProviderConfig) *HTTPProvider {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	maxOut := cfg.MaxOutputBytes
	if maxOut <= 0 {
		maxOut = DefaultLimits().MaxOutputBytes
	}
	return &HTTPProvider{
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		model:   strings.TrimSpace(cfg.Model),
		apiKey:  cfg.APIKey,
		timeout: cfg.Timeout,
		maxOut:  maxOut,
		client:  client,
	}
}

// Descriptor implements Provider. Configured is about having somewhere to send
// a request; whether a key is present is MissingCredential's job, because a
// local server needs no key.
func (p *HTTPProvider) Descriptor() ProviderDescriptor {
	if p == nil {
		return ProviderDescriptor{Name: "openai-compatible"}
	}
	return ProviderDescriptor{
		Name:          "openai-compatible",
		Model:         p.model,
		Version:       "1",
		Deterministic: false,
		Configured:    p.baseURL != "" && p.model != "",
	}
}

// MissingCredential reports that this provider is addressable but has no API
// key. Service.Available consults it through an optional interface so the
// reason says "no key" instead of repeating "not configured".
func (p *HTTPProvider) MissingCredential() bool {
	return p != nil && p.apiKey == ""
}

/* ---------- wire types ---------- */

type httpChatRequest struct {
	Model          string          `json:"model"`
	Messages       []httpChatMsg   `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Tools          []Tool          `json:"tools,omitempty"`
	ToolChoice     string          `json:"tool_choice,omitempty"`
}

// httpChatMsg is one turn on the wire. Content is always sent, even when it is
// empty: a tool-calling assistant turn carries its calls in tool_calls, and a
// server that expects a content key would rather see "" than nothing.
type httpChatMsg struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type httpChatResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// maxCompletionTokens bounds what the server may generate. It is a request
// parameter, not a policy: MaxOutputBytes still decides what this client will
// accept, because a server can ignore max_tokens.
const maxCompletionTokens = 2048

// Complete implements Provider. Every failure path wraps ErrProviderUnavailable
// so the pipeline can report AI_UNAVAILABLE honestly, and every detail string
// is redacted before it leaves this method.
func (p *HTTPProvider) Complete(ctx context.Context, req ProviderRequest) (ProviderResponse, error) {
	start := time.Now()
	if p == nil || p.baseURL == "" || p.model == "" {
		return ProviderResponse{}, fmt.Errorf("%w: the openai-compatible provider is not configured", ErrProviderUnavailable)
	}
	if p.apiKey == "" {
		return ProviderResponse{}, fmt.Errorf("%w: the openai-compatible provider has no API key", ErrProviderUnavailable)
	}

	timeout := p.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	limit := p.maxOut
	if req.MaxOutputBytes > 0 && req.MaxOutputBytes < limit {
		limit = req.MaxOutputBytes
	}

	payload := httpChatRequest{
		Model:     p.model,
		Messages:  p.messages(req),
		MaxTokens: maxCompletionTokens,
	}
	// Tools and response_format are mutually exclusive on the wire: a server
	// that is asked for a tool call cannot also be asked to constrain the reply
	// to a JSON object, and the pair is rejected rather than narrowed. So the
	// schema becomes prompt text when tools are advertised and response_format
	// is not sent at all.
	//
	// A schema is otherwise advertised both in the prompt and as
	// response_format. The model may ignore one or both; the pipeline validates
	// the reply either way, so this is a request for cooperation, never a trust
	// boundary.
	if len(req.Tools) > 0 {
		payload.Tools = req.Tools
		payload.ToolChoice = req.ToolChoice
		if payload.ToolChoice == "" {
			payload.ToolChoice = "auto"
		}
	} else if len(req.Schema) > 0 {
		payload.ResponseFormat = &responseFormat{Type: "json_object"}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("%w: could not encode the request: %s", ErrProviderUnavailable, p.redact(err.Error()))
	}
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("%w: could not build the request: %s", ErrProviderUnavailable, p.redact(err.Error()))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		// A deadline is reported as a timeout rather than an opaque transport
		// error so the caller can tell "slow" from "unreachable".
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return ProviderResponse{}, fmt.Errorf("%w: the provider did not answer within %s", ErrProviderUnavailable, timeout)
		}
		return ProviderResponse{}, fmt.Errorf("%w: the provider could not be reached: %s", ErrProviderUnavailable, p.redact(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	// Read one byte past the cap: if it arrives, the reply is over budget and
	// the caller gets an error instead of a body that was silently cut in half
	// and then parsed as if it were whole.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("%w: the reply could not be read: %s", ErrProviderUnavailable, p.redact(err.Error()))
	}
	if len(raw) > limit {
		return ProviderResponse{}, fmt.Errorf("%w: the reply exceeded the %d-byte limit", ErrProviderUnavailable, limit)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProviderResponse{}, fmt.Errorf("%w: the provider answered %d: %s", ErrProviderUnavailable, resp.StatusCode, p.redact(truncate(raw, 300)))
	}

	var parsed httpChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ProviderResponse{}, fmt.Errorf("%w: the reply was not valid JSON: %s", ErrProviderUnavailable, p.redact(err.Error()))
	}
	if len(parsed.Choices) == 0 {
		return ProviderResponse{}, fmt.Errorf("%w: the reply contained no choices[0]", ErrProviderUnavailable)
	}
	choice := parsed.Choices[0]
	// A tool-calling reply carries its answer in tool_calls and may have no
	// text at all. Requiring content would make every successful tool call look
	// like an empty reply, which is the failure this transport used to report.
	if len(choice.Message.ToolCalls) == 0 && strings.TrimSpace(choice.Message.Content) == "" {
		return ProviderResponse{}, fmt.Errorf("%w: the reply contained neither choices[0].message.content nor tool_calls", ErrProviderUnavailable)
	}

	return ProviderResponse{
		Text:         choice.Message.Content,
		ToolCalls:    choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
		Provider:     "openai-compatible",
		Model:        p.model,
		TokensIn:     parsed.Usage.PromptTokens,
		TokensOut:    parsed.Usage.CompletionTokens,
		LatencyMS:    float64(time.Since(start).Microseconds()) / 1000,
	}, nil
}

// Bounds on the repair context. Previous is the model's own earlier reply, so
// it can be as large as the output cap; the failure line is what actually
// directs the repair, so it keeps a smaller budget of its own.
const (
	maxPreviousContextChars = 16000
	maxFailureChars         = 2000
)

// userContent builds the user message: the prompt, the repair context when
// this is a repair pass, and the schema when the caller asked for one.
//
// The repair context is the entire point of a repair pass. ProviderRequest has
// carried Previous and Failure since the seam landed, but this transport never
// sent them, so a repair asked the model the same question again and got the
// same bundle back — which is exactly what the live-provider audit measured:
// two repairs, the same rejects-the-limit violation each time, exhausted. A
// repair that cannot name what was wrong is not a repair.
func userContent(req ProviderRequest) string {
	var b strings.Builder
	b.WriteString(req.Prompt)
	if previous := strings.TrimSpace(req.Previous); previous != "" {
		b.WriteString("\n\nYour previous reply is below. It was rejected, so produce a corrected reply rather than the same one.\n```json\n")
		b.WriteString(clipRunes(previous, maxPreviousContextChars))
		b.WriteString("\n```")
	}
	if failure := strings.TrimSpace(req.Failure); failure != "" {
		b.WriteString("\n\nWhat was wrong with it: ")
		b.WriteString(clipRunes(failure, maxFailureChars))
		b.WriteString("\nFix that specific problem and change nothing that already passed.")
	}
	if len(req.Schema) > 0 {
		b.WriteString("\n\nReply with a single JSON object that satisfies this JSON Schema:\n")
		b.WriteString(string(req.Schema))
	}
	return b.String()
}

// messages builds the wire turns. A conversation is sent as it stands; a
// single-shot request is the system prompt plus the user message this transport
// has always built (prompt, repair context, schema).
func (p *HTTPProvider) messages(req ProviderRequest) []httpChatMsg {
	if len(req.Messages) > 0 {
		out := make([]httpChatMsg, 0, len(req.Messages))
		for _, m := range req.Messages {
			out = append(out, httpChatMsg{
				Role:       m.Role,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
				ToolCalls:  m.ToolCalls,
			})
		}
		return out
	}
	return []httpChatMsg{
		{Role: "system", Content: req.System},
		{Role: "user", Content: userContent(req)},
	}
}

// clipRunes truncates on a rune boundary. A cut in the middle of a multi-byte
// character would make the request body invalid UTF-8, which some servers
// reject outright — turning a long repair into a transport error.
func clipRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n... (truncated)"
}

// Redact removes credentials this provider knows about from a string. It is the
// exported form of the internal scrub, so the transcript recorder can call the
// same rule that every error path uses — the alternative would be a second
// credential list that drifts from this one.
func (p *HTTPProvider) Redact(s string) string { return p.redact(s) }

// redact removes credentials this client knows about from any string it is
// about to return. engine.RedactSecrets covers URL userinfo and password= DSN
// shapes; the explicit key replacement covers a server that echoes the
// Authorization header back in an error body.
func (p *HTTPProvider) redact(s string) string {
	s = engine.RedactSecrets(s)
	if p != nil && p.apiKey != "" {
		s = strings.ReplaceAll(s, p.apiKey, "xxxxx")
	}
	return s
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}
