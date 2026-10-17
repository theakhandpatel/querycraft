package httpapi

import (
	"encoding/json"
	"net/http"
)

/*
 * Per-account client preferences: the theme, custom theme token sets, and
 * whatever else the client owns next. One JSON document per account under the
 * store's "ui" key.
 *
 * The gateway stores and returns the document; it does not interpret it. That
 * is the honest division: the client knows what a theme id means, and a
 * future preference must not become a gateway schema change. Ownership is
 * enforced the same way progress is — the account comes from the session,
 * never from the body.
 */

// preferencesKey is the user_settings key the client preference document
// persists under.
const preferencesKey = "ui"

// handleGetPreferences implements GET /preferences.
func (s *Server) handleGetPreferences(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	raw, found, err := s.store.UserSetting(r.Context(), u.ID, preferencesKey)
	if err != nil {
		errorEnvelope(w, http.StatusInternalServerError, "PREFERENCES_UNAVAILABLE", "The preferences could not be read.")
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preferences": nil})
		return
	}
	var doc any
	if jsonErr := json.Unmarshal([]byte(raw), &doc); jsonErr != nil {
		// A stored document that will not parse is reported as absent rather
		// than failing the surface: the client falls back to its local
		// defaults, and the next save replaces the row wholesale.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preferences": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preferences": json.RawMessage(raw)})
}

// handlePutPreferences implements PUT /preferences. The body is stored whole
// under the caller's account; the response echoes what was stored.
func (s *Server) handlePutPreferences(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var raw json.RawMessage
	if !readJSON(w, r, &raw) {
		return
	}
	if err := s.store.SaveUserSetting(r.Context(), u.ID, preferencesKey, string(raw)); err != nil {
		errorEnvelope(w, http.StatusInternalServerError, "PREFERENCES_UNAVAILABLE", "The preferences could not be saved.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preferences": json.RawMessage(raw)})
}
