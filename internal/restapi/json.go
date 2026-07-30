// Package restapi is the JSON API and terminal-stream endpoint the web UI runs
// on. It is mounted under /api and requires a human session throughout.
package restapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// The status line and headers are already on the wire, so there is no
		// error response left to send — log it and let the client see a
		// truncated body.
		slog.Error("encode response body", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
