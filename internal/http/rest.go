// Package http is the forge's transport layer (see server.go).
package http

import (
	"net/http"

	"forge/internal/graphql"
	"forge/internal/http/rest"
	"forge/internal/mcp"
)

// registerRoutes wires the Phase 2 routes: the boot probe, the REST v3
// API, the GraphQL API, the MCP endpoint, and the git smart-HTTP
// endpoints. Everything else is a 404 JSON object.
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	restHandler := rest.New(s.svc, s.authenticate, baseURL)
	s.mux.Handle("/api/v3", restHandler)
	s.mux.Handle("/api/v3/", restHandler)
	s.mux.Handle("/api/graphql", graphql.New(s.svc, s.authenticate))
	s.mux.Handle("/mcp", mcp.New(s.svc, s.authenticate))
	s.mux.HandleFunc("/", s.handleGit)
}

// handleGit serves the git smart-HTTP endpoints (everything that is not
// /healthz or an API route). A path that is not a git URL is a 404 JSON
// object.
func (s *Server) handleGit(w http.ResponseWriter, r *http.Request) {
	owner, repo, rest, ok := splitGitPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	switch {
	case r.Method == http.MethodGet && rest == "info/refs":
		s.handleInfoRefs(w, r, owner, repo, r.URL.Query().Get("service"))
	case r.Method == http.MethodPost && rest == "git-upload-pack":
		s.handleUploadPack(w, r, owner, repo)
	case r.Method == http.MethodPost && rest == "git-receive-pack":
		s.handleReceivePack(w, r, owner, repo)
	default:
		writeError(w, http.StatusNotFound, "Not Found")
	}
}
