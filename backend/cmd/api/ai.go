// ai.go turns the resolved AI settings into the challenge lab's provider and
// the pipeline's bounds. The provider key (ai_key) is read here and handed to
// the provider; it is never written to a log, a response or a record.

package main

import (
	"log/slog"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/config"
)

// classifyAIProvider resolves the provider from the settings. kind is "off",
// "fake" or "openai-compatible"; reason explains an off result for the boot log
// and the status report.
//
// The rules live here so the boot choice (aiProviderFrom) and `querycraft db
// status` cannot disagree about whether a provider is live: an
// openai-compatible setting with no ai_base_url or ai_model is off for both.
//
// The M4 provider choice, from the resolved settings (ADR 0005). Generation is
// pluggable and off unless a provider is named. The default is the
// deterministic fake provider: it runs the real pipeline (validation, throwaway
// environment, reference solution, cross-check, adversarial fixtures, bounded
// repair) with no network and no key, so the lab works out of the box and the
// exit gate is testable in CI. Its provenance says `fake` and its model says
// `fake-v1`, so nothing downstream mistakes it for a hosted model.
//
// A hosted or local model is one setting away: ai_provider=openai-compatible
// with ai_base_url and ai_model, plus ai_key when the server wants one, in
// querycraft.json.
func classifyAIProvider(settings config.Settings) (kind, reason string) {
	switch name := strings.ToLower(strings.TrimSpace(settings.AIProvider)); name {
	case "", "off", "none", "disabled":
		return "off", "set ai_provider to fake or openai-compatible in querycraft.json to enable the challenge lab"
	case "fake", "deterministic":
		return "fake", ""
	case "openai", "openai-compatible":
		if strings.TrimSpace(settings.AIBaseURL) == "" || strings.TrimSpace(settings.AIModel) == "" {
			return "off", "the openai-compatible provider needs ai_base_url and ai_model in querycraft.json"
		}
		return "openai-compatible", ""
	default:
		return "off", "unknown ai_provider " + name
	}
}

// aiProviderFrom builds the configured provider, or nil when AI is off. A
// misconfiguration is logged once at boot and degrades to "off", which is a
// supported mode, rather than failing the gateway's start-up.
func aiProviderFrom(settings config.Settings, log *slog.Logger) provider.Provider {
	kind, reason := classifyAIProvider(settings)
	switch kind {
	case "fake":
		return ai.NewFakeProvider()
	case "openai-compatible":
		base := strings.TrimSpace(settings.AIBaseURL)
		model := strings.TrimSpace(settings.AIModel)
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
		log.Info("AI generation is off", "reason", reason)
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
