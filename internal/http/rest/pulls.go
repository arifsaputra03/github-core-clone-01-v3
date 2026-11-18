// Package rest is the forge's GitHub REST v3 API (see types.go).
package rest

import (
	"net/http"
	"strconv"

	"forge/internal/service"
)

// handlePulls routes /api/v3/repos/{owner}/{repo}/pulls[/{n}[/files|/merge]].
func (h *Handler) handlePulls(w http.ResponseWriter, r *http.Request, owner, repo string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.handleCreatePull(w, r, owner, repo)
		case http.MethodGet:
			h.handleListPulls(w, r, owner, repo)
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
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.handleGetPull(w, r, owner, repo, number)
		return
	}
	if len(rest) == 2 && rest[1] == "files" && r.Method == http.MethodGet {
		h.handleListPullFiles(w, r, owner, repo, number)
		return
	}
	if len(rest) == 2 && rest[1] == "merge" && r.Method == http.MethodPut {
		h.handleMergePull(w, r, owner, repo, number)
		return
	}
	writeError(w, http.StatusNotFound, "Not Found")
}

// createPullRequest is the body of POST /repos/{owner}/{repo}/pulls.
type createPullRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
}

// handleCreatePull serves POST /repos/{owner}/{repo}/pulls: 201. A missing
// head or base branch, or an already-open pull request from the same head,
// is a 422.
func (h *Handler) handleCreatePull(w http.ResponseWriter, r *http.Request, owner, repo string) {
	var req createPullRequest
	if !readBody(w, r, &req) {
		return
	}
	pr, err := h.svc.CreatePull(owner, repo, service.CreatePullInput{
		Title:  req.Title,
		Body:   req.Body,
		Head:   req.Head,
		Base:   req.Base,
		Author: h.svc.ViewerLogin(),
	})
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, pr.Public(h.baseURL(r), h.svc.BaseSHAFor(pr)))
}

// handleListPulls serves GET /repos/{owner}/{repo}/pulls, filtered by state
// (open by default), paginated.
func (h *Handler) handleListPulls(w http.ResponseWriter, r *http.Request, owner, repo string) {
	pulls, err := h.svc.ListPulls(owner, repo, r.URL.Query().Get("state"))
	if writeStatusError(w, err) {
		return
	}
	page, perPage := pagination(r)
	items, pages := paginate(pulls, page, perPage)
	h.setLinkHeader(w, r, page, perPage, pages)
	out := make([]any, 0, len(items))
	for _, pr := range items {
		out = append(out, pr.Public(h.baseURL(r), h.svc.BaseSHAFor(pr)))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetPull serves GET /repos/{owner}/{repo}/pulls/{n}.
func (h *Handler) handleGetPull(w http.ResponseWriter, r *http.Request, owner, repo string, number int) {
	pr, err := h.svc.GetPull(owner, repo, number)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, pr.Public(h.baseURL(r), h.svc.BaseSHAFor(pr)))
}

// handleListPullFiles serves GET /repos/{owner}/{repo}/pulls/{n}/files.
func (h *Handler) handleListPullFiles(w http.ResponseWriter, r *http.Request, owner, repo string, number int) {
	changes, err := h.svc.ListPullFiles(owner, repo, number)
	if writeStatusError(w, err) {
		return
	}
	out := make([]any, 0, len(changes))
	for _, c := range changes {
		out = append(out, map[string]any{
			"filename":  c.Filename,
			"status":    c.Status,
			"additions": c.Additions,
			"deletions": c.Deletions,
			"changes":   c.Changes,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// mergePullRequest is the body of PUT .../pulls/{n}/merge.
type mergePullRequest struct {
	MergeMethod string `json:"merge_method"`
}

// handleMergePull serves PUT /repos/{owner}/{repo}/pulls/{n}/merge: 200
// {merged:true, sha, message} on success, 404 for an unknown pull request,
// 405 for a conflicted or already-closed one.
func (h *Handler) handleMergePull(w http.ResponseWriter, r *http.Request, owner, repo string, number int) {
	var req mergePullRequest
	if !readBody(w, r, &req) {
		return
	}
	mergeSHA, err := h.svc.MergePull(owner, repo, number, req.MergeMethod)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"merged":  true,
		"sha":     mergeSHA,
		"message": "Pull request successfully merged",
	})
}
