// Package rest is the forge's GitHub REST v3 API (see types.go).
package rest

import (
	"net/http"
)

// handleLabels routes /api/v3/repos/{owner}/{repo}/labels[/{name}].
func (h *Handler) handleLabels(w http.ResponseWriter, r *http.Request, owner, repo string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.handleCreateLabel(w, r, owner, repo)
		case http.MethodGet:
			h.handleListLabels(w, r, owner, repo)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if len(rest) != 1 {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	name := rest[0]
	switch r.Method {
	case http.MethodPatch:
		h.handlePatchLabel(w, r, owner, repo, name)
	case http.MethodDelete:
		h.handleDeleteLabel(w, r, owner, repo, name)
	case http.MethodGet:
		h.handleGetLabel(w, r, owner, repo, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// createLabelRequest is the body of POST /repos/{owner}/{repo}/labels.
type createLabelRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// handleCreateLabel serves POST /repos/{owner}/{repo}/labels: 201. A
// duplicate name is a 422.
func (h *Handler) handleCreateLabel(w http.ResponseWriter, r *http.Request, owner, repo string) {
	var req createLabelRequest
	if !readBody(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	label, err := h.svc.CreateLabel(owner, repo, req.Name, req.Color)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, label.Public())
}

// handleListLabels serves GET /repos/{owner}/{repo}/labels.
func (h *Handler) handleListLabels(w http.ResponseWriter, r *http.Request, owner, repo string) {
	labels, err := h.svc.ListLabels(owner, repo)
	if writeStatusError(w, err) {
		return
	}
	out := make([]any, 0, len(labels))
	for _, l := range labels {
		out = append(out, l.Public())
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetLabel serves GET /repos/{owner}/{repo}/labels/{name}.
func (h *Handler) handleGetLabel(w http.ResponseWriter, r *http.Request, owner, repo, name string) {
	labels, err := h.svc.ListLabels(owner, repo)
	if writeStatusError(w, err) {
		return
	}
	for _, l := range labels {
		if l.Name == name {
			writeJSON(w, http.StatusOK, l.Public())
			return
		}
	}
	writeError(w, http.StatusNotFound, "Not Found")
}

// patchLabelRequest is the body of PATCH /repos/{owner}/{repo}/labels/{name}.
type patchLabelRequest struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

// handlePatchLabel serves PATCH /repos/{owner}/{repo}/labels/{name}.
func (h *Handler) handlePatchLabel(w http.ResponseWriter, r *http.Request, owner, repo, name string) {
	var req patchLabelRequest
	if !readBody(w, r, &req) {
		return
	}
	newName := name
	if req.Name != nil {
		newName = *req.Name
	}
	color := ""
	if req.Color != nil {
		color = *req.Color
	}
	// A PATCH that does not name a colour keeps the current one.
	if req.Color == nil {
		labels, err := h.svc.ListLabels(owner, repo)
		if writeStatusError(w, err) {
			return
		}
		for _, l := range labels {
			if l.Name == name {
				color = l.Color
			}
		}
	}
	label, err := h.svc.UpdateLabel(owner, repo, name, newName, color)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, label.Public())
}

// handleDeleteLabel serves DELETE /repos/{owner}/{repo}/labels/{name}: 204.
func (h *Handler) handleDeleteLabel(w http.ResponseWriter, r *http.Request, owner, repo, name string) {
	if err := h.svc.DeleteLabel(owner, repo, name); writeStatusError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
