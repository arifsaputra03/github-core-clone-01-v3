// Package rest is the forge's GitHub REST v3 API (see types.go).
package rest

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"forge/internal/git"
	"forge/internal/service"
	"forge/internal/store"
)

// handleRepos routes /api/v3/repos/{owner}/{repo}[/...].
func (h *Handler) handleRepos(w http.ResponseWriter, r *http.Request, segments []string) {
	if len(segments) < 3 {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	owner, name := segments[1], segments[2]
	rest := segments[3:]
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			h.handleGetRepo(w, r, owner, name)
		case http.MethodPatch:
			h.handlePatchRepo(w, r, owner, name)
		case http.MethodDelete:
			h.handleDeleteRepo(w, r, owner, name)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	switch rest[0] {
	case "branches":
		h.handleBranches(w, r, owner, name, rest[1:])
	case "commits":
		h.handleCommits(w, r, owner, name, rest[1:])
	case "git":
		h.handleGitRefs(w, r, owner, name, rest[1:])
	case "contents":
		h.handleContents(w, r, owner, name, rest[1:])
	case "labels":
		h.handleLabels(w, r, owner, name, rest[1:])
	case "issues":
		h.handleIssues(w, r, owner, name, rest[1:])
	case "pulls":
		h.handlePulls(w, r, owner, name, rest[1:])
	case "milestones":
		h.handleMilestones(w, r, owner, name, rest[1:])
	default:
		writeError(w, http.StatusNotFound, "Not Found")
	}
}

// createRepoRequest is the body of POST /api/v3/user/repos.
type createRepoRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Private     bool   `json:"private"`
	AutoInit    bool   `json:"auto_init"`
}

// handleCreateRepo serves POST /api/v3/user/repos. The repository belongs
// to the viewer (the login the bearer token names). A duplicate name is a
// 422, which the suite accepts as idempotent.
func (h *Handler) handleCreateRepo(w http.ResponseWriter, r *http.Request) {
	var req createRepoRequest
	if !readBody(w, r, &req) {
		return
	}
	repo, err := h.svc.CreateRepository(service.CreateRepositoryInput{
		Name:        req.Name,
		Description: req.Description,
		Private:     req.Private,
		AutoInit:    req.AutoInit,
	})
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusUnprocessableEntity, "name already exists on this account")
			return
		}
		writeStatusError(w, err)
		return
	}
	w.Header().Set("Location", h.baseURL(r)+"/api/v3/repos/"+repo.Owner+"/"+repo.Name)
	writeJSON(w, http.StatusCreated, repo.Public(h.baseURL(r)))
}

// handleListUserRepos serves GET /api/v3/user/repos, paginated through the
// Link header.
func (h *Handler) handleListUserRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := h.svc.ListRepositories(h.svc.ViewerLogin())
	if writeStatusError(w, err) {
		return
	}
	page, perPage := pagination(r)
	items, pages := paginate(repos, page, perPage)
	h.setLinkHeader(w, r, page, perPage, pages)
	out := make([]any, 0, len(items))
	for _, repo := range items {
		out = append(out, repo.Public(h.baseURL(r)))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetRepo serves GET /api/v3/repos/{owner}/{repo}. A repository under
// another owner is a 404, never a 403.
func (h *Handler) handleGetRepo(w http.ResponseWriter, r *http.Request, owner, repo string) {
	got, err := h.svc.GetRepository(owner, repo)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, got.Public(h.baseURL(r)))
}

// patchRepoRequest is the body of PATCH /api/v3/repos/{owner}/{repo}.
type patchRepoRequest struct {
	Description *string `json:"description"`
	Private     *bool   `json:"private"`
}

// handlePatchRepo serves PATCH /api/v3/repos/{owner}/{repo}.
func (h *Handler) handlePatchRepo(w http.ResponseWriter, r *http.Request, owner, repo string) {
	var req patchRepoRequest
	if !readBody(w, r, &req) {
		return
	}
	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	// A PATCH that does not name a description keeps the current one.
	if req.Description == nil {
		current, err := h.svc.GetRepository(owner, repo)
		if writeStatusError(w, err) {
			return
		}
		description = current.Description
	}
	updated, err := h.svc.UpdateRepository(owner, repo, description, req.Private)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated.Public(h.baseURL(r)))
}

// handleDeleteRepo serves DELETE /api/v3/repos/{owner}/{repo}: 204, and a
// later GET is 404.
func (h *Handler) handleDeleteRepo(w http.ResponseWriter, r *http.Request, owner, repo string) {
	if err := h.svc.DeleteRepository(owner, repo); writeStatusError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBranches serves GET /api/v3/repos/{owner}/{repo}/branches and
// GET .../branches/{branch}.
func (h *Handler) handleBranches(w http.ResponseWriter, r *http.Request, owner, repo string, rest []string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if len(rest) == 0 {
		branches, err := h.svc.ListBranches(owner, repo)
		if writeStatusError(w, err) {
			return
		}
		out := make([]any, 0, len(branches))
		for _, b := range branches {
			out = append(out, map[string]any{
				"name":   b.Name,
				"commit": map[string]any{"sha": b.SHA},
			})
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	branch := strings.Join(rest, "/")
	b, err := h.svc.GetBranch(owner, repo, branch)
	if writeStatusError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":   b.Name,
		"commit": map[string]any{"sha": b.SHA},
	})
}

// handleCommits serves GET /api/v3/repos/{owner}/{repo}/commits[?sha=].
func (h *Handler) handleCommits(w http.ResponseWriter, r *http.Request, owner, repo string, rest []string) {
	if r.Method != http.MethodGet || len(rest) != 0 {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	commits, err := h.svc.ListCommits(owner, repo, r.URL.Query().Get("sha"))
	if writeStatusError(w, err) {
		return
	}
	out := make([]any, 0, len(commits))
	for _, c := range commits {
		out = append(out, map[string]any{
			"sha": c.SHA,
			"commit": map[string]any{
				"message": c.Message,
			},
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// createRefRequest is the body of POST /api/v3/repos/{owner}/{repo}/git/refs.
type createRefRequest struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// handleGitRefs serves the Git Data API's refs: POST /git/refs to create a
// branch, GET /git/refs/heads/{branch} to read one.
func (h *Handler) handleGitRefs(w http.ResponseWriter, r *http.Request, owner, repo string, rest []string) {
	if len(rest) == 1 && rest[0] == "refs" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req createRefRequest
		if !readBody(w, r, &req) {
			return
		}
		sha, err := h.svc.CreateRef(owner, repo, req.Ref, req.SHA)
		if writeStatusError(w, err) {
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"ref":    req.Ref,
			"object": map[string]any{"sha": sha},
		})
		return
	}
	if len(rest) >= 2 && rest[0] == "refs" && r.Method == http.MethodGet {
		// The ref name is everything after /git/refs/ (a branch may hold
		// slashes), prefixed with "refs/".
		ref := "refs/" + strings.Join(rest[1:], "/")
		sha, err := h.svc.GetRef(owner, repo, ref)
		if writeStatusError(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ref":    ref,
			"object": map[string]any{"sha": sha},
		})
		return
	}
	writeError(w, http.StatusNotFound, "Not Found")
}

// putContentRequest is the body of PUT /api/v3/repos/{owner}/{repo}/contents/{path}.
type putContentRequest struct {
	Message string `json:"message"`
	Content string `json:"content"` // base64
	Branch  string `json:"branch"`
	SHA     string `json:"sha"`
}

// deleteContentRequest is the body of DELETE .../contents/{path}.
type deleteContentRequest struct {
	Message string `json:"message"`
	SHA     string `json:"sha"`
	Branch  string `json:"branch"`
}

// handleContents serves the contents API: PUT/GET/DELETE on a path. The
// path is every segment after /contents/, so nested paths work.
func (h *Handler) handleContents(w http.ResponseWriter, r *http.Request, owner, repo string, rest []string) {
	if len(rest) == 0 {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	path := strings.Join(rest, "/")
	switch r.Method {
	case http.MethodPut:
		var req putContentRequest
		if !readBody(w, r, &req) {
			return
		}
		content, err := base64.StdEncoding.DecodeString(req.Content)
		if err != nil {
			writeError(w, http.StatusBadRequest, "content is not base64")
			return
		}
		result, err := h.svc.PutContent(owner, repo, path, content, req.Message, req.Branch, req.SHA)
		if writeStatusError(w, err) {
			return
		}
		status := http.StatusCreated
		if !result.Created {
			status = http.StatusOK
		}
		writeJSON(w, status, map[string]any{
			"content": map[string]any{
				"name":     lastSegment(path),
				"path":     path,
				"sha":      result.BlobSHA,
				"size":     len(content),
				"encoding": "base64",
			},
			"commit": map[string]any{
				"sha":     result.CommitSHA,
				"message": req.Message,
			},
		})
	case http.MethodGet:
		result, err := h.svc.GetContent(owner, repo, path, r.URL.Query().Get("ref"))
		if writeStatusError(w, err) {
			return
		}
		switch result.Kind {
		case git.PathFile:
			writeJSON(w, http.StatusOK, map[string]any{
				"type":     "file",
				"name":     lastSegment(path),
				"path":     path,
				"sha":      result.BlobSHA,
				"size":     len(result.Content),
				"encoding": "base64",
				"content":  base64.StdEncoding.EncodeToString(result.Content),
			})
		case git.PathDir:
			entries := make([]any, 0, len(result.Entries))
			for _, e := range result.Entries {
				kind := "file"
				if e.Kind == git.PathDir {
					kind = "dir"
				}
				entries = append(entries, map[string]any{
					"type": kind,
					"name": e.Name,
					"path": e.Path,
					"sha":  e.OID,
				})
			}
			writeJSON(w, http.StatusOK, entries)
		default:
			writeError(w, http.StatusNotFound, "Not Found")
		}
	case http.MethodDelete:
		var req deleteContentRequest
		if !readBody(w, r, &req) {
			return
		}
		commitSHA, err := h.svc.DeleteContent(owner, repo, path, req.Message, req.Branch, req.SHA)
		if writeStatusError(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"commit": map[string]any{
				"sha":     commitSHA,
				"message": req.Message,
			},
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func lastSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
