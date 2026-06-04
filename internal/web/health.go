package web

import (
	"net/http"
	"strings"
)

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if s.Manager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok":    false,
			"error": "manager is not configured",
		})
		return
	}

	database := "not_configured"
	if strings.TrimSpace(s.Manager.DatabaseURL) != "" {
		database = "ready"
	}
	if err := s.Manager.Ensure(); err != nil {
		if strings.TrimSpace(s.Manager.DatabaseURL) != "" {
			database = "unavailable"
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok":       false,
			"database": database,
			"error":    err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"database": database,
	})
}
