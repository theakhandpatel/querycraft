package provider

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

// HTTPProvider is an OpenAI-compatible chat-completions client. It speaks the
// wire shape OpenAI, most hosted clones and local servers (llama.cpp, Ollama's
// compatibility endpoint, vLLM) accept. The provider seam stays pluggable in
// practice: a fake provider proves the pipeline, and this one proves the wire.
//
// It uses net/http and encoding/json only. A vendor SDK would move the contract
// into a dependency and bring its own retry and timeout policy, which is
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

// HTTPProviderConfig configures the client.
type HTTPProviderConfig struct {
	BaseURL        string
	Model          string
	APIKey         string
	Timeout        time.Duration
	MaxOutputBytes int
}

// NewHTTPProvider builds the client. A provider with no BaseURL or Model is not
// configured, and a provider with no key cannot be used. Descriptor and
// Service.Available report both, so the failure lands at configuration time
// rather than at call time.
func NewHTTPProvider(cfg HTTPProviderConfig) *HTTPProvider {
	maxOut := cfg.MaxOutputBytes
	if maxOut <= 0 {
		maxOut = DefaultMaxOutputBytes
	}
	return &HTTPProvider{
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		model:   strings.TrimSpace(cfg.Model),
		apiKey:  cfg.APIKey,
		timeout: cfg.Timeout,
		maxOut:  maxOut,
		client:  &http.Client{},
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
}

// httpChatMsg is one turn on the wire.
type httpChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type httpChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// maxCompletionTokens bounds what the server may generate. It is a request
// parameter, not a policy: MaxOutputBytes still decides what this client will
// accept, because a server can ignore max_tokens.
const maxCompletionTokens = 2048

// DefaultMaxOutputBytes is the reply cap when the caller asks for no smaller
// limit. It is 256 KiB, because a hosted reasoning model puts its reasoning in
// the reply body next to the answer and the cap measures the whole body (see
// Complete). A smaller cap rejects a good bundle whose reasoning text is large.
const DefaultMaxOutputBytes = 256 * 1024

// Complete implements Provider. The pipeline reports any failure as
// AI_UNAVAILABLE, and every detail string is redacted before it leaves this
// method.
func (p *HTTPProvider) Complete(ctx context.Context, req ProviderRequest) (ProviderResponse, error) {
	start := time.Now()
	if p == nil || p.baseURL == "" || p.model == "" {
		return ProviderResponse{}, fmt.Errorf("the openai-compatible provider is not configured")
	}
	if p.apiKey == "" {
		return ProviderResponse{}, fmt.Errorf("the openai-compatible provider has no API key")
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
		Model: p.model,
		Messages: []httpChatMsg{
			{Role: "system", Content: req.System},
			{Role: "user", Content: UserContent(req)},
		},
		MaxTokens: maxCompletionTokens,
	}
	// The schema is advertised as response_format. The model may ignore it;
	// the pipeline validates the reply either way, so this is a request for
	// cooperation, never a trust boundary.
	if len(req.Schema) > 0 {
		payload.ResponseFormat = &responseFormat{Type: "json_object"}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("could not encode the request: %s", p.redact(err.Error()))
	}
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("could not build the request: %s", p.redact(err.Error()))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		// A deadline is reported as a timeout rather than an opaque transport
		// error so the caller can tell "slow" from "unreachable".
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return ProviderResponse{}, fmt.Errorf("the provider did not answer within %s", timeout)
		}
		return ProviderResponse{}, fmt.Errorf("the provider could not be reached: %s", p.redact(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	// Read one byte past the cap: if it arrives, the reply is over budget and
	// the caller gets an error instead of a body that was silently cut in half
	// and then parsed as if it were whole.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("the reply could not be read: %s", p.redact(err.Error()))
	}
	if len(raw) > limit {
		return ProviderResponse{}, fmt.Errorf("the reply exceeded the %d-byte limit", limit)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProviderResponse{}, fmt.Errorf("the provider answered %d: %s", resp.StatusCode, p.redact(truncate(raw, 300)))
	}

	var parsed httpChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ProviderResponse{}, fmt.Errorf("the reply was not valid JSON: %s", p.redact(err.Error()))
	}
	if len(parsed.Choices) == 0 {
		return ProviderResponse{}, fmt.Errorf("the reply contained no choices[0]")
	}
	choice := parsed.Choices[0]
	if strings.TrimSpace(choice.Message.Content) == "" {
		return ProviderResponse{}, fmt.Errorf("the reply contained no choices[0].message.content")
	}

	return ProviderResponse{
		Text:      choice.Message.Content,
		LatencyMS: float64(time.Since(start).Microseconds()) / 1000,
	}, nil
}

// Bounds on the repair context. Previous is the model's own earlier reply, so
// it can be as large as the output cap; the failure line is what actually
// directs the repair, so it keeps a smaller budget of its own.
const (
	maxPreviousContextChars = 16000
	maxFailureChars         = 2000
)

// UserContent builds the user message: the prompt, the repair context when
// this is a repair pass, and the schema when the caller asked for one.
//
// The repair context is the entire point of a repair pass. A repair that cannot
// name what was wrong is not a repair: the model answers the same question
// again and returns the same bundle back. Previous and Failure carry that
// context, so both travel with the request.
func UserContent(req ProviderRequest) string {
	var b strings.Builder
	b.WriteString(req.Prompt)
	if previous := strings.TrimSpace(req.Previous); previous != "" {
		b.WriteString("\n\nYour previous reply is below. It was rejected, so produce a corrected reply rather than the same one.\n```json\n")
		b.WriteString(ClipRunes(previous, maxPreviousContextChars))
		b.WriteString("\n```")
	}
	if failure := strings.TrimSpace(req.Failure); failure != "" {
		b.WriteString("\n\nWhat was wrong with it: ")
		b.WriteString(ClipRunes(failure, maxFailureChars))
		b.WriteString("\nFix that specific problem and change nothing that already passed.")
	}
	if len(req.Schema) > 0 {
		b.WriteString("\n\nReply with a single JSON object that satisfies this JSON Schema:\n")
		b.WriteString(string(req.Schema))
	}
	return b.String()
}

// Course is the HTTP provider's answer to the course-agent capability: a
// custom OpenAI-compatible server, reached through pi's own adapters with the
// server's model id and the configured key.
func (p *HTTPProvider) Course() CourseCapability {
	return CourseCapability{
		Model: CustomModel(p.baseURL, p.model),
		Key:   func(string) string { return p.apiKey },
	}
}

// ClipRunes truncates on a rune boundary. A cut in the middle of a multi-byte
// character would make the request body invalid UTF-8, which some servers
// reject outright. That turns a long repair into a transport error.
func ClipRunes(s string, n int) string {
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
// exported form of the internal scrub, so the transcript recorder reuses the
// same rule every error path uses. A second credential list would drift from
// this one.
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
