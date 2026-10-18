// Package provider is the model seam: the small Provider interface, the wire
// types every implementation shares, and the OpenAI-compatible client the
// gateway ships with. Nothing here knows about the pipeline, so a provider can
// be exercised on its own.
package provider

import "context"

// ProviderDescriptor is what the UI and the provenance record need to know
// about the thing that produced a bundle.
type ProviderDescriptor struct {
	Name          string
	Model         string
	Version       string
	Deterministic bool
	// Configured is false when the provider is known but has no key or no
	// endpoint. A configured provider can still fail a call; that is a
	// call-time failure, not a configuration problem.
	Configured bool
}

// ToolCall is one function call a model asked for. Arguments is the JSON text
// the model produced, unparsed: whether it is a usable object is the caller's
// decision, because the caller is the one that knows the function.
type ToolCall struct {
	ID       string           `json:"id"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is the name of the function and its arguments.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
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
}

// ProviderResponse is the raw reply plus the facts needed to attribute it.
type ProviderResponse struct {
	Text string
	// ToolCalls is what the reply asked the caller to do, when it asked. A
	// tool-calling reply can carry no text at all, which is why Text being
	// empty is not by itself a failure when ToolCalls says it asked.
	ToolCalls []ToolCall
	LatencyMS float64
}

// Provider is the whole model surface. It is deliberately small: a completion
// in, the reply out. It reaches no filesystem and no network of its own; the
// only functions a model may "call" are the ones the caller advertises and
// executes itself (§11.2).
type Provider interface {
	Descriptor() ProviderDescriptor
	Complete(ctx context.Context, req ProviderRequest) (ProviderResponse, error)
}
