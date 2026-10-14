package ai

import "context"

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

// ProviderRequest is one completion. Everything the model may see is here, and
// nothing else: the reference solution is deliberately absent from tutoring
// requests so the policy cannot leak it (§11.2).
type ProviderRequest struct {
	// Kind is one of "challenge", "course", "tutor", "critique", "repair".
	Kind  string
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
	Text      string
	Provider  string
	Model     string
	TokensIn  int
	TokensOut int
	LatencyMS float64
}

// Provider is the whole model surface. It is deliberately small: a completion
// in, text out, with no tools and no filesystem reach (§11.2).
type Provider interface {
	Descriptor() ProviderDescriptor
	Complete(ctx context.Context, req ProviderRequest) (ProviderResponse, error)
}
