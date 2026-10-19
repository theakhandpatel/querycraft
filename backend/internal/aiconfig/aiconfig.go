// Package aiconfig turns a persisted AI setting into the provider the gateway
// runs and the boot configuration that carries it. The manage surface writes
// the setting and swaps the running provider; the boot path in cmd/api reads
// the same setting back. One package owns both mappings, so the two paths
// cannot drift. The mapping is not HTTP, so it lives outside httpapi.
package aiconfig

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/ai/provider/piai"
)

// Setting is the persisted shape.
type Setting struct {
	Provider  string `json:"provider"`
	BaseURL   string `json:"base_url,omitempty"`
	Model     string `json:"model,omitempty"`
	Key       string `json:"key,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// ProviderFromAISetting builds the provider a saved setting names. It is the
// runtime counterpart of the boot path in cmd/api: the same three shapes, the
// same off-by-default rule, and a misconfiguration that degrades to "off"
// rather than failing the gateway.
func ProviderFromAISetting(s Setting, log *slog.Logger) provider.Provider {
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
	var saved Setting
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		return base, err
	}
	base.Provider = ProviderFromAISetting(saved, log)
	return base, nil
}
