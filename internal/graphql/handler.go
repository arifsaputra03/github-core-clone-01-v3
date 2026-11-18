// Package graphql is the forge's GraphQL API at POST /api/graphql (see
// parse.go and exec.go).
package graphql

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"forge/internal/service"
)

// Handler is the GraphQL endpoint: POST /api/graphql.
type Handler struct {
	svc          *service.Service
	authenticate func(http.ResponseWriter, *http.Request) bool
}

// New builds a GraphQL handler. authenticate is the server's bearer check;
// it writes the 401 itself and reports whether the request may proceed.
func New(svc *service.Service, authenticate func(http.ResponseWriter, *http.Request) bool) *Handler {
	return &Handler{svc: svc, authenticate: authenticate}
}

// request is one GraphQL request document.
type request struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables"`
	OperationName string         `json:"operationName"`
}

// ServeHTTP serves POST /api/graphql. A JSON array body is a batch: the
// responses come back in the same order.
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
	if len(body) > 0 && body[0] == '[' {
		var batch []request
		if err := json.Unmarshal(body, &batch); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		out := make([]map[string]any, 0, len(batch))
		for _, req := range batch {
			out = append(out, h.execute(req))
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	writeJSON(w, http.StatusOK, h.execute(req))
}

// execute runs one request document and returns its response object.
func (h *Handler) execute(req request) map[string]any {
	doc, err := parseDocument(req.Query)
	if err != nil {
		return map[string]any{"errors": []map[string]any{{"message": err.Error()}}}
	}
	op := pickOperation(doc, req.OperationName)
	if op == nil {
		return map[string]any{"errors": []map[string]any{{"message": "Must provide operation name if query contains multiple operations."}}}
	}
	ex := newExecutor(h.svc, h.svc.ViewerLogin(), req.Variables, doc.frags)
	return ex.run(op)
}

// pickOperation selects the operation to run: the named one when
// operationName is given, the only one when the document has one, and
// nil when the choice is ambiguous.
func pickOperation(doc *document, name string) *operation {
	if name != "" {
		for _, op := range doc.ops {
			if op.name == name {
				return op
			}
		}
		return nil
	}
	if len(doc.ops) == 1 {
		return doc.ops[0]
	}
	return nil
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
