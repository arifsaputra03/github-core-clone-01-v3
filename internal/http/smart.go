// Package http is the forge's transport layer (see server.go).
package http

import (
	"io"
	"log"
	"net/http"
	"strings"

	"forge/internal/git"
)

// smartHTTP routes the git smart-HTTP endpoints. The URL shape is
// /{owner}/{repo}.git/info/refs and /{owner}/{repo}.git/git-upload-pack
// (or git-receive-pack). Every one of them requires authentication, and a
// repository that does not exist is a 404 that leaves the server running.
func (s *Server) handleInfoRefs(w http.ResponseWriter, r *http.Request, owner, repo, service string) {
	if !s.authenticate(w, r) {
		return
	}
	if !s.svc.RepoExists(owner, repo) {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	bare, err := s.svc.OpenRepo(owner, repo)
	if err != nil {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	switch service {
	case "git-upload-pack":
		if r.Header.Get("Git-Protocol") == "version=2" {
			w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
			if err := git.AdvertiseUploadPackV2(w); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to advertise")
			}
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		if err := git.AdvertiseUploadPackV0(w, bare); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to advertise")
		}
	case "git-receive-pack":
		w.Header().Set("Content-Type", "application/x-git-receive-pack-advertisement")
		if err := git.AdvertiseReceivePackV0(w, bare); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to advertise")
		}
	default:
		writeError(w, http.StatusBadRequest, "unknown service "+service)
	}
}

// handleUploadPack serves POST /{owner}/{repo}.git/git-upload-pack. The
// protocol is v2 when the client asks for it (Git-Protocol: version=2),
// v0 otherwise.
func (s *Server) handleUploadPack(w http.ResponseWriter, r *http.Request, owner, repo string) {
	if !s.authenticate(w, r) {
		return
	}
	if !s.svc.RepoExists(owner, repo) {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	bare, err := s.svc.OpenRepo(owner, repo)
	if err != nil {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read request body")
		return
	}
	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	if r.Header.Get("Git-Protocol") == "version=2" {
		if err := git.ServeUploadPackV2(w, bare, body); err != nil {
			// The response may already be partly written; log and stop.
			// A protocol error here is a server error, not a client one.
			s.logError(r, err)
		}
		return
	}
	if err := git.ServeUploadPackV0(w, bare, body); err != nil {
		s.logError(r, err)
	}
}

// handleReceivePack serves POST /{owner}/{repo}.git/git-receive-pack
// (protocol v0).
func (s *Server) handleReceivePack(w http.ResponseWriter, r *http.Request, owner, repo string) {
	if !s.authenticate(w, r) {
		return
	}
	if !s.svc.RepoExists(owner, repo) {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	bare, err := s.svc.OpenRepo(owner, repo)
	if err != nil {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read request body")
		return
	}
	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	if err := git.ServeReceivePack(w, bare, body); err != nil {
		s.logError(r, err)
	}
}

// logError records a protocol error. The response is already in flight by
// then, so there is nothing to send back; the message goes to the server
// log (stderr) for diagnosis.
func (s *Server) logError(r *http.Request, err error) {
	log.Printf("forge: %s %s: %v", r.Method, r.URL.Path, err)
}

// splitGitPath splits a request path of the form
// /{owner}/{repo}.git/<rest> into owner, repo and rest. It returns false
// when the path is not a git URL.
func splitGitPath(path string) (owner, repo, rest string, ok bool) {
	path = strings.TrimPrefix(path, "/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	owner = parts[0]
	repoDotGit := parts[1]
	rest = parts[2]
	if !strings.HasSuffix(repoDotGit, ".git") {
		return "", "", "", false
	}
	repo = strings.TrimSuffix(repoDotGit, ".git")
	if owner == "" || repo == "" {
		return "", "", "", false
	}
	return owner, repo, rest, true
}
