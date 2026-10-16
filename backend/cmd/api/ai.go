package main

import (
	"log/slog"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/config"
)

/* The M4 provider choice, from the resolved settings (ADR 0005).
 *
 * Generation is pluggable and off unless a provider is named. The default is
 * the deterministic fake provider: it runs the real pipeline — validation,
 * throwaway environment, reference solution, cross-check, adversarial fixtures,
 * bounded repair — with no network and no key, so the lab works out of the box
 * and the exit gate is testable in CI. Its provenance says `fake` and the model
 * `fake-v1`, so nothing downstream mistakes it for a hosted model.
 *
 * A hosted or local model is one setting away: ai_provider=openai-compatible
 * with ai_base_url and ai_model (plus ai_key when the server wants one), in
 * querycraft.json. The key is read
 * here, handed to the provider, and never written to a log, a response or a
 * record. */

// aiProviderFrom builds the configured provider, or nil when AI is off. A
// misconfiguration is logged once at boot and degrades to "off", which is a
// supported mode, rather than failing the gateway's start-up.
func aiProviderFrom(settings config.Settings, log *slog.Logger) provider.Provider {
	switch kind := strings.ToLower(strings.TrimSpace(settings.AIProvider)); kind {
	case "", "off", "none", "disabled":
		log.Info("AI generation is off", "hint", "set ai_provider to fake or openai-compatible in querycraft.json to enable the challenge lab")
		return nil
	case "fake", "deterministic":
		return ai.NewFakeProvider()
	case "openai", "openai-compatible":
		base := strings.TrimSpace(settings.AIBaseURL)
		model := strings.TrimSpace(settings.AIModel)
		if base == "" || model == "" {
			log.Warn("AI stays off: the openai-compatible provider needs ai_base_url and ai_model in querycraft.json")
			return nil
		}
		key := settings.AIKey
		log.Info("AI provider configured", "provider", "openai-compatible", "model", model, "base", base, "keySet", key != "")
		return provider.NewHTTPProvider(provider.HTTPProviderConfig{
			BaseURL:        base,
			Model:          model,
			APIKey:         key,
			Timeout:        time.Duration(settings.AITimeoutMS) * time.Millisecond,
			MaxOutputBytes: settings.AIMaxOutputBytes,
		})
	default:
		log.Warn("AI stays off: unknown provider", "ai_provider", kind)
		return nil
	}
}

// aiLimitsFrom returns the pipeline's own bounds. The run budget must be at
// least one provider call, and in practice several, so it is derived from
// ai_timeout_ms times (1 + MaxRepairs) unless ai_run_timeout_ms says
// otherwise. A run budget shorter than a single provider call can only ever
// produce a timeout, so that is the floor rather than a warning.
func aiLimitsFrom(settings config.Settings) ai.Limits {
	limits := ai.DefaultLimits()
	call := time.Duration(settings.AITimeoutMS) * time.Millisecond
	if run := time.Duration(settings.AIRunTimeoutMS) * time.Millisecond; run > 0 {
		limits.Timeout = run
	} else {
		limits.Timeout = call * time.Duration(1+limits.MaxRepairs)
	}
	limits.MaxOutputBytes = settings.AIMaxOutputBytes
	if limits.Timeout < call {
		limits.Timeout = call
	}
	return limits
}
