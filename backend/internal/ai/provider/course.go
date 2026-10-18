package provider

import (
	piagent "github.com/sky-valley/pi/agent"
	piAI "github.com/sky-valley/pi/ai"
)

/* The course capability: the second thing a provider may be able to do.
 *
 * A provider serves one-shot completions through Complete. A course run is a
 * conversation on pi's agent loop instead, and it needs three things from the
 * wire side: the pi model record to run on, a resolver for its API key, and a
 * stream function. A provider that can serve that conversation implements
 * CourseProvider. The built-in provider answers the conversation itself
 * (StreamFn set); a real model leaves StreamFn nil so pi resolves the provider
 * from the model record.
 */

// CourseCapability is one provider's answer to the course agent's needs.
type CourseCapability struct {
	// Model is the pi model record the chapter conversation runs on.
	Model *piAI.Model
	// Key resolves the API key for a provider id. pi calls it with the
	// model's own provider; an implementation may ignore the argument.
	Key func(provider string) string
	// StreamFn serves one request of the conversation. Nil means pi's own
	// stream, resolved from Model.
	StreamFn piagent.StreamFn
}

// CourseProvider is implemented by a provider that can serve the course
// chapter conversation. A provider that only answers one-shot completions
// does not implement it, and a course run under one reports why instead of
// guessing.
type CourseProvider interface {
	Course() CourseCapability
}

// CourseCapabilityOf reports whether p can serve the course conversation.
func CourseCapabilityOf(p Provider) (CourseCapability, bool) {
	cp, ok := p.(CourseProvider)
	if !ok {
		return CourseCapability{}, false
	}
	return cp.Course(), true
}

// CustomModel is the pi model record for a custom OpenAI-compatible server: a
// model gateway, vLLM, llama.cpp. The model id is the server's own, and the
// key travels through the capability's Key resolver.
func CustomModel(baseURL, model string) *piAI.Model {
	return &piAI.Model{
		ID:            model,
		Name:          model,
		Api:           "openai-completions",
		Provider:      "custom",
		BaseURL:       baseURL,
		Input:         []string{"text"},
		ContextWindow: 128000,
		MaxTokens:     16384,
	}
}
