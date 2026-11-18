// Package mcp is the forge's Model Context Protocol endpoint at POST /mcp:
// JSON-RPC 2.0 over streamable HTTP, in both the modern era (2026-07-28,
// server/discover) and the legacy era (initialize). Plain JSON responses
// are served (no SSE stream is needed). The tools let an AI agent drive
// the forge: create and read repositories, issues and pull requests.
package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"forge/internal/service"
)

// Handler is the MCP endpoint: POST /mcp.
type Handler struct {
	svc          *service.Service
	authenticate func(http.ResponseWriter, *http.Request) bool
}

// New builds an MCP handler. authenticate is the server's bearer check;
// it writes the 401 itself and reports whether the request may proceed.
func New(svc *service.Service, authenticate func(http.ResponseWriter, *http.Request) bool) *Handler {
	return &Handler{svc: svc, authenticate: authenticate}
}

// rpcRequest is one JSON-RPC request. The id is any JSON value: a string
// or a number for a request, absent for a notification.
type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

// rpcError is one JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// rpcResponse is one JSON-RPC response. The id echoes the request's id
// exactly (a string or a number), so concurrent calls answer under their
// own ids.
type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

// supportedVersions are the protocol revisions the server speaks, newest
// first.
var supportedVersions = []string{
	"2026-07-28",
	"2025-11-25",
	"2025-06-18",
	"2025-03-26",
	"2024-11-05",
}

// serverName is the serverInfo the discover and initialize answers carry.
var serverName = map[string]any{"name": "forge", "version": "1.0.0"}

// ServeHTTP serves POST /mcp. The bearer check runs before the body is
// parsed, so a call without a token is a 401. A body that is not JSON is
// HTTP 400 with a JSON-RPC parse error (-32700) that still carries an id.
// A notification (no id) gets HTTP 202 with an empty body.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authenticate(w, r) {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read request body")
		return
	}
	body = bytes.TrimSpace(body)
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &rpcError{Code: -32700, Message: "Parse error"},
		})
		return
	}
	if req.Method == "" {
		writeJSON(w, http.StatusBadRequest, rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32600, Message: "Invalid Request"},
		})
		return
	}
	if req.ID == nil {
		// A notification: no answer is owed. 202 with an empty body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, rpcErr := h.dispatch(r, req)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
	writeJSON(w, http.StatusOK, resp)
}

// dispatch runs one JSON-RPC method and returns its result or its error.
func (h *Handler) dispatch(r *http.Request, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "server/discover":
		return map[string]any{
			"supportedVersions": supportedVersions,
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"serverInfo":        serverName,
			"_meta":             map[string]any{"io.modelcontextprotocol/serverInfo": serverName},
		}, nil
	case "initialize":
		version := "2025-11-25"
		if offered, ok := req.Params["protocolVersion"].(string); ok {
			for _, v := range supportedVersions {
				if v == offered {
					version = offered
					break
				}
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      serverName,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefinitions()}, nil
	case "tools/call":
		return h.callTool(r, req.Params)
	}
	return nil, &rpcError{Code: -32601, Message: "Method not found: " + req.Method}
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error object: {"message": "<string>"}.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"message": message})
}

// baseURL returns the forge's own base URL, used to fill in the objects
// the tools return. It comes from the request, so the forge works behind
// any host and port.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host
}
