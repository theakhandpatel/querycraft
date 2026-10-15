package ai

import (
	"context"
	"encoding/json"
)

// ProviderDescriptor is what the UI and the provenance record need to know
// about the thing that produced a bundle.
type ProviderDescriptor struct {
	Name          string
	Model         string
	Version       string
	Deterministic bool
	// Configured is false when the provider is known but has no key or no
	// endpoint. A configured provider can still fail a call; that is
	// ErrProviderUnavailable, not a configuration problem.
	Configured bool
}

// ProviderMessage is one turn of a multi-turn conversation. It exists because
// the course agent writes one chapter per call: the model has to see what it
// already wrote and what the engine said about it, so the seam carries the
// conversation rather than one prompt.
type ProviderMessage struct {
	// Role is "system", "user", "assistant" or "tool".
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCallID names the call a "tool" message answers. It is empty for the
	// other roles.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCalls is what an assistant message asked for, when it asked.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall is one function call a model asked for. Arguments is the JSON text
// the model produced, unparsed: whether it is a usable object is the caller's
// decision, because the caller is the one that knows the function.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type,omitempty"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is the name of the function and its arguments.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is a function the model may call.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction describes one tool: what it does and the JSON Schema of the
// arguments it is called with.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ProviderRequest is one completion. Everything the model may see is here, and
// nothing else: the reference solution is deliberately absent from tutoring
// requests so the policy cannot leak it (§11.2).
type ProviderRequest struct {
	// Kind is one of "challenge", "course", "tutor", "critique", "repair".
	Kind   string
	System string
	Prompt string
	// Schema is a JSON Schema the reply must satisfy. A provider that cannot
	// constrain output natively must still be validated against it.
	Schema []byte
	// MaxOutputBytes bounds the reply before it is parsed.
	MaxOutputBytes int
	// Previous and Failure are set for a repair pass.
	Previous string
	Failure  string
	// Messages, when non-empty, is the whole conversation and replaces
	// System+Prompt: the transport sends these turns in order, with Tools
	// advertised. It is how the course agent carries what the model already
	// wrote and what the engine said about it.
	Messages []ProviderMessage
	// Tools, when non-empty, advertises functions the model may call and is
	// what makes the turn a tool-calling turn. A transport must not also send
	// a response_format then: the wire contract makes the two mutually
	// exclusive, and a server that receives both rejects the request.
	Tools []Tool
	// ToolChoice is "auto", "none" or "required". The empty value means the
	// transport's own default, which is "auto" whenever tools are advertised.
	ToolChoice string
}

// ProviderResponse is the raw reply plus the facts needed to attribute it.
type ProviderResponse struct {
	Text string
	// ToolCalls is what the reply asked the caller to do, when it asked. A
	// tool-calling reply can carry no text at all, which is why Text being
	// empty is not by itself a failure once Tools were advertised.
	ToolCalls []ToolCall
	// FinishReason is the server's own word for why it stopped: "stop",
	// "tool_calls", "length". It is reported rather than interpreted.
	FinishReason string
	Provider     string
	Model        string
	TokensIn     int
	TokensOut    int
	LatencyMS    float64
}

// Provider is the whole model surface. It is deliberately small: a completion
// in, the reply out. It reaches no filesystem and no network of its own; the
// only functions a model may "call" are the ones the caller advertises in
// Tools and executes itself (§11.2).
type Provider interface {
	Descriptor() ProviderDescriptor
	Complete(ctx context.Context, req ProviderRequest) (ProviderResponse, error)
}
