package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/ai/provider/piai"
	"github.com/querycraft/sqlquery/backend/internal/aiconfig"
)

/*
 * The AI configuration an administrator can change while the gateway runs.
 *
 * The config file decides what the gateway boots with; the manage surface
 * decides what it runs with. A persisted setting (the store's "ai" key) is the
 * live truth once it exists. The boot path applies it over the file's provider,
 * and PUT /admin/ai writes it and swaps the running configuration. The API key
 * travels one way: the gateway stores it, never logs it and never returns it in
 * a body. That is why every read answers key_set, a boolean.
 *
 * requireAdmin gates every route here, because the configuration is shared by
 * the whole gateway rather than owned by one account.
 */

// settingAIKey is the store key the AI configuration persists under.
const settingAIKey = "ai"

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
		var saved aiconfig.Setting
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

// handleAdminAIModels implements GET /admin/ai/models: the embedded model
// catalog as a flat list, so the manage surface can offer every provider and
// model the customer could name. Chat models only.
func (s *Server) handleAdminAIModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "models": piai.Catalog()})
}

// badAIRequest writes the one 400 an unusable AI setting gets: the code an AI
// route answers an unreadable body with, and the sentence that says what to fix.
func badAIRequest(w http.ResponseWriter, message string) {
	errorEnvelope(w, http.StatusBadRequest, "AI_INVALID_REQUEST", message)
}

// handleAdminAISave implements PUT /admin/ai: validate, persist, and swap the
// running configuration. An invalid request changes nothing.
func (s *Server) handleAdminAISave(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var body aiconfig.Setting
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
				badAIRequest(w, "The openai-compatible provider needs base_url and model.")
				return
			}
		}
		if body.Provider == "pi-ai" {
			if body.Model == "" {
				badAIRequest(w, "The pi-ai provider needs a model ref (provider/model, e.g. anthropic/claude-sonnet-4-5) or a base_url and model for a custom server.")
				return
			}
			if body.BaseURL == "" && !strings.Contains(body.Model, "/") {
				badAIRequest(w, "The pi-ai provider's model is a provider/model ref (e.g. anthropic/claude-sonnet-4-5), or set base_url and model for a custom server.")
				return
			}
		}
	default:
		badAIRequest(w, "provider must be one of: off, fake, openai-compatible, pi-ai.")
		return
	}
	if body.TimeoutMS < 0 {
		badAIRequest(w, "timeout_ms must be zero (for the shipped default) or a positive number.")
		return
	}
	if body.TimeoutMS == 0 {
		body.TimeoutMS = ai.DefaultCallTimeoutMS
	}

	// An empty key on a save means "keep what is stored": the key never
	// travels back to a client, so a round-trip through the form cannot
	// require the admin to retype it, and a fresh install has none to keep.
	if body.Key == "" {
		if raw, found, err := s.store.Setting(r.Context(), settingAIKey); err == nil && found {
			var saved aiconfig.Setting
			if jsonErr := json.Unmarshal([]byte(raw), &saved); jsonErr == nil {
				body.Key = saved.Key
			}
		}
	}

	persisted, err := json.Marshal(body)
	if err != nil {
		persistenceFailed(w, err)
		return
	}
	if err := s.store.SaveSetting(r.Context(), settingAIKey, string(persisted)); err != nil {
		persistenceFailed(w, err)
		return
	}

	// The swap builds on the live configuration so the depot, the runner, the
	// logger and the pipeline limits are exactly what booted; only the
	// provider (and its call timeout) is the administrator's to change.
	cfg, ok := s.currentAI()
	if !ok {
		cfg = ai.Config{}
	}
	cfg.Provider = aiconfig.ProviderFromAISetting(body, s.log)
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
