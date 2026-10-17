package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/ai/provider/piai"
)

/*
 * The AI configuration an administrator can change while the gateway runs.
 *
 * The config file decides what the gateway boots with; the manage surface
 * decides what it runs with. A persisted setting (the store's "ai" key) is the
 * live truth once it exists — the boot path applies it over the file's
 * provider, and PUT /admin/ai writes it and swaps the running configuration.
 * The API key travels one way: it is stored, it is never logged and never
 * returned in a body, which is why every read answers key_set, a boolean.
 */

// settingAIKey is the store key the AI configuration persists under.
const settingAIKey = "ai"

// aiSetting is the persisted shape.
type aiSetting struct {
	Provider  string `json:"provider"`
	BaseURL   string `json:"base_url,omitempty"`
	Model     string `json:"model,omitempty"`
	Key       string `json:"key,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// aiConfigView is what the admin surface reads. The key is a boolean, by the
// same rule /health follows for the admin password.
type aiConfigView struct {
	OK        bool   `json:"ok"`
	Provider  string `json:"provider"`
	BaseURL   string `json:"base_url,omitempty"`
	Model     string `json:"model,omitempty"`
	KeySet    bool   `json:"key_set"`
	TimeoutMS int    `json:"timeout_ms"`
	Source    string `json:"source"`
}

// defaultAICallTimeoutMS is the per-call timeout a saved setting gets when it
// names none. It matches the config file's shipped default.
const defaultAICallTimeoutMS = 120000

// handleAdminAI implements GET /admin/ai: the effective configuration and
// where it came from. "database" means an administrator saved it here; "boot"
// means the config file's provider is live and the manage surface has not
// saved one yet.
func (s *Server) handleAdminAI(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	view := s.aiConfigView(r.Context())
	writeJSON(w, http.StatusOK, view)
}

// aiConfigView builds the read model from the persisted setting, falling back
// to the live boot configuration when nothing was saved here.
func (s *Server) aiConfigView(ctx context.Context) aiConfigView {
	raw, found, err := s.store.Setting(ctx, settingAIKey)
	if err == nil && found {
		var saved aiSetting
		if jsonErr := json.Unmarshal([]byte(raw), &saved); jsonErr == nil {
			return aiConfigView{
				OK:        true,
				Provider:  saved.Provider,
				BaseURL:   saved.BaseURL,
				Model:     saved.Model,
				KeySet:    saved.Key != "",
				TimeoutMS: saved.TimeoutMS,
				Source:    "database",
			}
		}
		// An unreadable saved value falls through to the boot view rather
		// than failing the surface: the live provider is still serving, and
		// a broken row must not hide it. PUT replaces the row wholesale.
	}

	cfg, ok := s.currentAI()
	if !ok || cfg.Provider == nil {
		return aiConfigView{OK: true, Provider: "off", Source: "boot"}
	}
	d := cfg.Provider.Descriptor()
	view := aiConfigView{OK: true, Provider: d.Name, Model: d.Model, Source: "boot"}
	if mc, isMc := cfg.Provider.(interface{ MissingCredential() bool }); isMc {
		view.KeySet = !mc.MissingCredential()
	}
	return view
}

// handleAdminAISave implements PUT /admin/ai: validate, persist, and swap the
// running configuration. An invalid request changes nothing.
func (s *Server) handleAdminAISave(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var body aiSetting
	if !readJSON(w, r, &body) {
		return
	}
	switch body.Provider {
	case "", "off", "none", "disabled":
		body.Provider = "off"
	case "fake", "deterministic", "openai", "openai-compatible", "pi-ai":
		if body.Provider == "fake" || body.Provider == "deterministic" {
			body.Provider = "fake"
		}
		if body.Provider == "openai" {
			body.Provider = "openai-compatible"
		}
		if body.Provider == "openai-compatible" {
			if body.BaseURL == "" || body.Model == "" {
				errorEnvelope(w, http.StatusBadRequest, "AI_INVALID_REQUEST",
					"The openai-compatible provider needs base_url and model.")
				return
			}
		}
		if body.Provider == "pi-ai" {
			if body.Model == "" {
				errorEnvelope(w, http.StatusBadRequest, "AI_INVALID_REQUEST",
					"The pi-ai provider needs a model ref (provider/model, e.g. anthropic/claude-sonnet-4-5) or a base_url and model for a custom server.")
				return
			}
			if body.BaseURL == "" && !strings.Contains(body.Model, "/") {
				errorEnvelope(w, http.StatusBadRequest, "AI_INVALID_REQUEST",
					"The pi-ai provider's model is a provider/model ref (e.g. anthropic/claude-sonnet-4-5), or set base_url and model for a custom server.")
				return
			}
		}
	default:
		errorEnvelope(w, http.StatusBadRequest, "AI_INVALID_REQUEST",
			"provider must be one of: off, fake, openai-compatible.")
		return
	}
	if body.TimeoutMS < 0 {
		errorEnvelope(w, http.StatusBadRequest, "AI_INVALID_REQUEST",
			"timeout_ms must be zero (for the shipped default) or a positive number.")
		return
	}
	if body.TimeoutMS == 0 {
		body.TimeoutMS = defaultAICallTimeoutMS
	}

	// An empty key on a save means "keep what is stored": the key never
	// travels back to a client, so a round-trip through the form cannot
	// require the admin to retype it, and a fresh install has none to keep.
	if body.Key == "" {
		if raw, found, err := s.store.Setting(r.Context(), settingAIKey); err == nil && found {
			var saved aiSetting
			if jsonErr := json.Unmarshal([]byte(raw), &saved); jsonErr == nil {
				body.Key = saved.Key
			}
		}
	}

	persisted, err := json.Marshal(body)
	if err != nil {
		errorEnvelope(w, http.StatusInternalServerError, "AI_INVALID_REQUEST", "The setting could not be encoded.")
		return
	}
	if err := s.store.SaveSetting(r.Context(), settingAIKey, string(persisted)); err != nil {
		errorEnvelope(w, http.StatusInternalServerError, "AI_INVALID_REQUEST", "The setting could not be saved.")
		return
	}

	// The swap builds on the live configuration so the depot, the runner, the
	// logger and the pipeline limits are exactly what booted; only the
	// provider (and its call timeout) is the administrator's to change.
	cfg, ok := s.currentAI()
	if !ok {
		cfg = ai.Config{}
	}
	cfg.Provider = providerFromAISetting(body, s.log)
	// The run budget follows the same derivation the boot path uses: at least
	// one call plus the repair attempts, never shorter than the call itself.
	// Raising the call timeout raises the run budget; lowering it keeps the
	// booted budget, which was sized for the same pipeline.
	if call := time.Duration(body.TimeoutMS) * time.Millisecond; call > 0 {
		limits := cfg.Limits.Merge(ai.DefaultLimits())
		derived := call * time.Duration(1+limits.MaxRepairs)
		if cfg.Limits.Timeout < derived {
			cfg.Limits.Timeout = derived
		}
		if cfg.Limits.Timeout < call {
			cfg.Limits.Timeout = call
		}
	}
	s.SetAIConfig(cfg)

	s.log.Info("AI configuration changed",
		"provider", body.Provider,
		"model", body.Model,
		"base", body.BaseURL,
		"keySet", body.Key != "",
		"timeout_ms", body.TimeoutMS,
	)
	writeJSON(w, http.StatusOK, s.aiConfigView(r.Context()))
}

// providerFromAISetting builds the provider a saved setting names. It is the
// runtime counterpart of the boot path in cmd/api: the same three shapes, the
// same off-by-default rule, and a misconfiguration that degrades to "off"
// rather than failing the gateway.
func providerFromAISetting(s aiSetting, log *slog.Logger) provider.Provider {
	switch s.Provider {
	case "fake":
		return ai.NewFakeProvider()
	case "openai-compatible":
		return provider.NewHTTPProvider(provider.HTTPProviderConfig{
			BaseURL:        s.BaseURL,
			Model:          s.Model,
			APIKey:         s.Key,
			Timeout:        time.Duration(s.TimeoutMS) * time.Millisecond,
			MaxOutputBytes: provider.DefaultMaxOutputBytes,
		})
	case "pi-ai":
		if s.BaseURL != "" {
			// A custom OpenAI-compatible server: the model id is the server's own.
			p, err := piai.New(piai.Config{Model: s.Model, APIKey: s.Key, BaseURL: s.BaseURL, TimeoutMS: s.TimeoutMS, MaxRetries: 2})
			if err != nil {
				log.Warn("AI stays off: the pi-ai provider rejected the configuration", "err", err)
				return nil
			}
			return p
		}
		// A catalog ref: provider/model resolves against the embedded catalog.
		ref := strings.SplitN(s.Model, "/", 2)
		if len(ref) != 2 {
			log.Warn("AI stays off: the pi-ai provider needs a provider/model model ref", "model", s.Model)
			return nil
		}
		p, err := piai.New(piai.Config{Provider: ref[0], Model: ref[1], APIKey: s.Key, TimeoutMS: s.TimeoutMS, MaxRetries: 2})
		if err != nil {
			log.Warn("AI stays off: the pi-ai provider rejected the configuration", "err", err)
			return nil
		}
		return p
	default: // "off"
		log.Info("AI generation is off by configuration")
		return nil
	}
}

// AIConfigFromSetting applies a persisted AI setting to a boot configuration:
// the boot path reads the store's "ai" key through this, so a setting the
// manage surface saved survives a restart. The base configuration keeps the
// depot, runner, logger and pipeline limits; only the provider changes. A
// setting that will not parse is reported rather than applied, so the config
// file's provider stays live and the reason is in the boot log.
func AIConfigFromSetting(raw string, base ai.Config, log *slog.Logger) (ai.Config, error) {
	var saved aiSetting
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		return base, err
	}
	base.Provider = providerFromAISetting(saved, log)
	return base, nil
}
