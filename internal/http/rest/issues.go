// Package rest is the forge's GitHub REST v3 API (see types.go).
package rest

import (
	"net/http"
	"strconv"

	"forge/internal/service"
)

// handleIssues routes /api/v3/repos/{owner}/{repo}/issues[/{n}[/comments]].
func (h *Handler) handleIssues(w http.ResponseWriter, r *http.Request, owner, repo string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.handleCreateIssue(w, r, owner, repo)
		case http.MethodGet:
			h.handleListIssues(w, r, owner, repo)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	number, err := strconv.Atoi(rest[0])
	if err != nil || number < 1 {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	if len(rest) == 1 {
		switch r.Method {
		case http.MethodGet:
			h.handleGetIssue(w, r, owner, repo, number)
		case http.MethodPatch:
			h.handlePatchIssue(w, r, owner, repo, number)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if len(rest) == 2 && rest[1] == "comments" {
		switch r.Method {
		case http.MethodPost:
			h.handleCreateComment(w, r, owner, repo, number)
		case http.MethodGet:
			h.handleListComments(w, r, owner, repo, number)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	writeError(w, http.StatusNotFound, "Not Found")
}

// createIssueRequest is the body of POST /repos/{owner}/{repo}/issues.
type createIssueRequest struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels"`
}

// handleCreateIssue serves POST /repos/{owner}/{repo}/issues: 201. An
// unknown label is a 422.
func (h *Handler) handleCreateIssue(w http.ResponseWriter, r *http.Request, owner, repo string) {
	var req createIssueRequest
	if !readBody(w, r, &req) {
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusUnprocessableEntity, "title is required")
		return
	}
	issue, err := h.svc.CreateIssue(owner, repo, service.CreateIssueInput{
		Title:  req.Title,
		Body:   req.Body,
		Labels: req.Labels,
		Author: h.svc.ViewerLogin(),
	})
	if writeStatusError(w, err) {
		return
	}
	colors, err := h.svc.LabelColors(owner, repo)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, issue.Public(h.baseURL(r), colors))
}

// handleListIssues serves GET /repos/{owner}/{repo}/issues, filtered by
// state and ordered by sort/direction, paginated.
func (h *Handler) handleListIssues(w http.ResponseWriter, r *http.Request, owner, repo string) {
	q := r.URL.Query()
	issues, err := h.svc.ListIssues(owner, repo, service.IssueListOptions{
		State:     q.Get("state"),
		Sort:      q.Get("sort"),
		Direction: q.Get("direction"),
	})
	if writeStatusError(w, err) {
		return
	}
	colors, err := h.svc.LabelColors(owner, repo)
	if writeStatusError(w, err) {
		return
	}
	page, perPage := pagination(r)
	items, pages := paginate(issues, page, perPage)
	h.setLinkHeader(w, r, page, perPage, pages)
	out := make([]any, 0, len(items))
	for _, issue := range items {
		out = append(out, issue.Public(h.baseURL(r), colors))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetIssue serves GET /repos/{owner}/{repo}/issues/{n}.
func (h *Handler) handleGetIssue(w http.ResponseWriter, r *http.Request, owner, repo string, number int) {
	issue, err := h.svc.GetIssue(owner, repo, number)
	if writeStatusError(w, err) {
		return
	}
	colors, err := h.svc.LabelColors(owner, repo)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, issue.Public(h.baseURL(r), colors))
}

// patchIssueRequest is the body of PATCH /repos/{owner}/{repo}/issues/{n}.
type patchIssueRequest struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
	State *string `json:"state"`
}

// handlePatchIssue serves PATCH /repos/{owner}/{repo}/issues/{n}.
func (h *Handler) handlePatchIssue(w http.ResponseWriter, r *http.Request, owner, repo string, number int) {
	var req patchIssueRequest
	if !readBody(w, r, &req) {
		return
	}
	issue, err := h.svc.UpdateIssue(owner, repo, number, service.UpdateIssueInput{
		Title: req.Title,
		Body:  req.Body,
		State: req.State,
	})
	if writeStatusError(w, err) {
		return
	}
	colors, err := h.svc.LabelColors(owner, repo)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, issue.Public(h.baseURL(r), colors))
}

// createCommentRequest is the body of POST .../issues/{n}/comments.
type createCommentRequest struct {
	Body string `json:"body"`
}

// handleCreateComment serves POST /repos/{owner}/{repo}/issues/{n}/comments: 201.
func (h *Handler) handleCreateComment(w http.ResponseWriter, r *http.Request, owner, repo string, number int) {
	var req createCommentRequest
	if !readBody(w, r, &req) {
		return
	}
	comment, err := h.svc.CreateComment(owner, repo, number, req.Body, h.svc.ViewerLogin())
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, comment.Public(h.baseURL(r)))
}

// handleListComments serves GET /repos/{owner}/{repo}/issues/{n}/comments.
func (h *Handler) handleListComments(w http.ResponseWriter, r *http.Request, owner, repo string, number int) {
	comments, err := h.svc.ListComments(owner, repo, number)
	if writeStatusError(w, err) {
		return
	}
	out := make([]any, 0, len(comments))
	for _, c := range comments {
		out = append(out, c.Public(h.baseURL(r)))
	}
	writeJSON(w, http.StatusOK, out)
}
