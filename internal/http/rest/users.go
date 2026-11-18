// Package rest is the forge's GitHub REST v3 API (see types.go).
package rest

import (
	"net/http"
)

// handleRoot serves GET /api/v3 and GET /api/v3/. Like the starter it
// needs no authentication and carries the X-OAuth-Scopes header.
func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("X-OAuth-Scopes", "repo")
	writeJSON(w, http.StatusOK, map[string]any{"message": "forge api"})
}

// handleMeta serves GET /api/v3/meta.
func (h *Handler) handleMeta(w http.ResponseWriter, r *http.Request, segments []string) {
	if len(segments) != 1 || r.Method != http.MethodGet {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installed_version": "1.0.0"})
}

// handleUser serves /api/v3/user and /api/v3/user/repos.
func (h *Handler) handleUser(w http.ResponseWriter, r *http.Request, segments []string) {
	if len(segments) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		user, err := h.svc.GetUser(h.svc.ViewerLogin())
		if writeStatusError(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, user.Public())
		return
	}
	if len(segments) == 2 && segments[1] == "repos" {
		switch r.Method {
		case http.MethodPost:
			h.handleCreateRepo(w, r)
		case http.MethodGet:
			h.handleListUserRepos(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	writeError(w, http.StatusNotFound, "Not Found")
}
