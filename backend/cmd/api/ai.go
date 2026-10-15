package main

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
)

/* The M4 provider choice, from the environment (ADR 0005).
 *
 * Generation is pluggable and off unless a provider is named. The default is
 * the deterministic fake provider: it runs the real pipeline — validation,
 * throwaway environment, reference solution, cross-check, adversarial fixtures,
 * bounded repair — with no network and no key, so the lab works out of the box
 * and the exit gate is testable in CI. Its provenance says `fake` and the model
 * `fake-v1`, so nothing downstream mistakes it for a hosted model.
 *
 * A hosted or local model is one variable away: QC_AI_PROVIDER=openai-compatible
 * with QC_AI_BASE_URL and QC_AI_MODEL (plus QC_AI_KEY when the server wants
 * one). The key is read here, handed to the provider, and never written to a
 * log, a response or a record. */

// defaultAICallTimeoutMS is how long one provider call may take, unless
// QC_AI_TIMEOUT_MS says otherwise.
//
// It is sized for the largest legitimate answer, not the smallest: a course
// bundle is four chapters of markdown plus every exercise and its reference
// query in a single reply, and a hosted model needed 58.5 s for one and blew
// past 60 s on another. At the old 45 s default the default product path --
// Create a course, press "Design my course" -- could not complete against a
// real provider at all, and the learner got "the provider did not answer within
// 45s" for a provider that was working. A challenge bundle is a tenth of the
// size and still answers in seconds; this bound only decides how patient the
// waiting is, and the pipeline's own run budget stays derived from it so the
// two can never disagree.
const defaultAICallTimeoutMS = 120000

// aiProviderFromEnv builds the configured provider, or nil when AI is off. A
// misconfiguration is logged once at boot and degrades to "off", which is a
// supported mode, rather than failing the gateway's start-up.
func aiProviderFromEnv(log *slog.Logger) ai.Provider {
	switch kind := strings.ToLower(strings.TrimSpace(env("QC_AI_PROVIDER", "fake"))); kind {
	case "", "off", "none", "disabled":
		log.Info("AI generation is off", "hint", "set QC_AI_PROVIDER=fake or openai-compatible to enable the challenge lab")
		return nil
	case "fake", "deterministic":
		return ai.NewFakeProvider()
	case "openai", "openai-compatible":
		base := strings.TrimSpace(env("QC_AI_BASE_URL", ""))
		model := strings.TrimSpace(env("QC_AI_MODEL", ""))
		if base == "" || model == "" {
			log.Warn("AI stays off: the openai-compatible provider needs QC_AI_BASE_URL and QC_AI_MODEL")
			return nil
		}
		key := os.Getenv("QC_AI_KEY")
		log.Info("AI provider configured", "provider", "openai-compatible", "model", model, "base", base, "keySet", key != "")
		return ai.NewHTTPProvider(ai.HTTPProviderConfig{
			BaseURL:        base,
			Model:          model,
			APIKey:         key,
			Timeout:        time.Duration(envInt("QC_AI_TIMEOUT_MS", defaultAICallTimeoutMS)) * time.Millisecond,
			MaxOutputBytes: envInt("QC_AI_MAX_OUTPUT_BYTES", 256*1024),
		})
	default:
		log.Warn("AI stays off: unknown provider", "QC_AI_PROVIDER", kind)
		return nil
	}
}

// aiLimitsFromEnv returns the pipeline's own bounds. The run budget must be at
// least one provider call, and in practice several, so it is derived from
// QC_AI_TIMEOUT_MS times (1 + MaxRepairs) unless QC_AI_RUN_TIMEOUT_MS says
// otherwise. A run budget shorter than a single provider call can only ever
// produce a timeout, so that is the floor rather than a warning.
func aiLimitsFromEnv() ai.Limits {
	limits := ai.DefaultLimits()
	call := time.Duration(envInt("QC_AI_TIMEOUT_MS", defaultAICallTimeoutMS)) * time.Millisecond
	if run := time.Duration(envInt("QC_AI_RUN_TIMEOUT_MS", 0)) * time.Millisecond; run > 0 {
		limits.Timeout = run
	} else {
		limits.Timeout = call * time.Duration(1+limits.MaxRepairs)
	}
	limits.MaxOutputBytes = envInt("QC_AI_MAX_OUTPUT_BYTES", limits.MaxOutputBytes)
	if limits.Timeout < call {
		limits.Timeout = call
	}
	return limits
}
