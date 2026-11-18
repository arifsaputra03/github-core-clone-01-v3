// Package rest is the forge's GitHub REST v3 API (see types.go).
package rest

import (
	"net/http"
	"strconv"
	"time"

	"forge/internal/service"
)

// handleMilestones routes /api/v3/repos/{owner}/{repo}/milestones[/{n}].
func (h *Handler) handleMilestones(w http.ResponseWriter, r *http.Request, owner, repo string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.handleCreateMilestone(w, r, owner, repo)
		case http.MethodGet:
			h.handleListMilestones(w, r, owner, repo)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	number, err := strconv.Atoi(rest[0])
	if err != nil || number < 1 || len(rest) != 1 {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetMilestone(w, r, owner, repo, number)
	case http.MethodPatch:
		h.handlePatchMilestone(w, r, owner, repo, number)
	case http.MethodDelete:
		h.handleDeleteMilestone(w, r, owner, repo, number)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// createMilestoneRequest is the body of POST /repos/{owner}/{repo}/milestones.
type createMilestoneRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	State       string `json:"state"`
	DueOn       string `json:"due_on"`
}

// handleCreateMilestone serves POST /repos/{owner}/{repo}/milestones: 201.
func (h *Handler) handleCreateMilestone(w http.ResponseWriter, r *http.Request, owner, repo string) {
	var req createMilestoneRequest
	if !readBody(w, r, &req) {
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusUnprocessableEntity, "title is required")
		return
	}
	var dueOn *time.Time
	if req.DueOn != "" {
		t, err := time.Parse(time.RFC3339, req.DueOn)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "due_on is not a valid RFC 3339 date")
			return
		}
		dueOn = &t
	}
	m, err := h.svc.CreateMilestone(owner, repo, service.CreateMilestoneInput{
		Title:       req.Title,
		Description: req.Description,
		State:       req.State,
		DueOn:       dueOn,
	})
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, m.Public(h.baseURL(r)))
}

// handleListMilestones serves GET /repos/{owner}/{repo}/milestones.
func (h *Handler) handleListMilestones(w http.ResponseWriter, r *http.Request, owner, repo string) {
	milestones, err := h.svc.ListMilestones(owner, repo, r.URL.Query().Get("state"))
	if writeStatusError(w, err) {
		return
	}
	out := make([]any, 0, len(milestones))
	for _, m := range milestones {
		out = append(out, m.Public(h.baseURL(r)))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetMilestone serves GET /repos/{owner}/{repo}/milestones/{n}.
func (h *Handler) handleGetMilestone(w http.ResponseWriter, r *http.Request, owner, repo string, number int) {
	m, err := h.svc.GetMilestone(owner, repo, number)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, m.Public(h.baseURL(r)))
}

// patchMilestoneRequest is the body of PATCH /repos/{owner}/{repo}/milestones/{n}.
type patchMilestoneRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	State       *string `json:"state"`
	DueOn       *string `json:"due_on"`
}

// handlePatchMilestone serves PATCH /repos/{owner}/{repo}/milestones/{n}.
func (h *Handler) handlePatchMilestone(w http.ResponseWriter, r *http.Request, owner, repo string, number int) {
	var req patchMilestoneRequest
	if !readBody(w, r, &req) {
		return
	}
	in := service.UpdateMilestoneInput{
		Title:       req.Title,
		Description: req.Description,
		State:       req.State,
	}
	if req.DueOn != nil {
		in.SetDueOn = true
		if *req.DueOn != "" {
			t, err := time.Parse(time.RFC3339, *req.DueOn)
			if err != nil {
				writeError(w, http.StatusUnprocessableEntity, "due_on is not a valid RFC 3339 date")
				return
			}
			in.DueOn = &t
		}
	}
	m, err := h.svc.UpdateMilestone(owner, repo, number, in)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, m.Public(h.baseURL(r)))
}

// handleDeleteMilestone serves DELETE /repos/{owner}/{repo}/milestones/{n}: 204.
func (h *Handler) handleDeleteMilestone(w http.ResponseWriter, r *http.Request, owner, repo string, number int) {
	if err := h.svc.DeleteMilestone(owner, repo, number); writeStatusError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
