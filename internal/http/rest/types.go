// Package rest is the forge's GitHub REST v3 API: the routes under
// /api/v3. It translates the service layer's domain objects into the JSON
// shapes GitHub's API serves (field names, structures and status codes),
// and maps the service's errors onto the HTTP statuses the suite expects:
// 404 for a missing record, 422 for a duplicate or an unprocessable
// request, 409 for a stale contents sha, 405 for a conflicted merge.
package rest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"forge/internal/git"
	"forge/internal/service"
	"forge/internal/store"
)

// Handler serves the REST v3 API. It is mounted at /api/v3 by the HTTP
// server, which supplies the bearer authentication and the base URL.
type Handler struct {
	svc          *service.Service
	authenticate func(http.ResponseWriter, *http.Request) bool
	baseURL      func(*http.Request) string
}

// New builds a REST handler over a service.
func New(svc *service.Service, authenticate func(http.ResponseWriter, *http.Request) bool, baseURL func(*http.Request) string) *Handler {
	return &Handler{svc: svc, authenticate: authenticate, baseURL: baseURL}
}

// writeJSON writes a JSON value with the given status. Every REST reply is
// application/json, never HTML, including errors.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error object: {"message": "<string>"}.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"message": message})
}

// writeStatus maps a service/store error onto its HTTP status and writes
// the error body. It returns true when err was non-nil.
func writeStatusError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrNotFound),
		errors.Is(err, git.ErrRefNotFound),
		errors.Is(err, git.ErrObjectNotFound):
		writeError(w, http.StatusNotFound, "Not Found")
	case errors.Is(err, store.ErrAlreadyExists),
		errors.Is(err, git.ErrPathExists),
		errors.Is(err, service.ErrUnknownLabel):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, git.ErrStaleSHA):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, service.ErrConflict),
		errors.Is(err, service.ErrPullClosed):
		writeError(w, http.StatusMethodNotAllowed, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
	return true
}

// readBody reads and decodes a JSON request body. A malformed body is a
// 400; an empty body decodes into the zero value.
func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read request body")
		return false
	}
	if len(raw) == 0 {
		return true
	}
	if err := json.Unmarshal(raw, v); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON: "+err.Error())
		return false
	}
	return true
}

// pagination parses the page and per_page query parameters. GitHub's
// defaults are page 1 and 30 items per page.
func pagination(r *http.Request) (page, perPage int) {
	page = 1
	perPage = 30
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			page = n
		}
	}
	if v := r.URL.Query().Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			perPage = n
		}
	}
	return page, perPage
}

// paginate slices a list to one page and returns the page's items plus the
// total number of pages.
func paginate[T any](items []T, page, perPage int) ([]T, int) {
	total := len(items)
	pages := (total + perPage - 1) / perPage
	if pages == 0 {
		pages = 1
	}
	if page > pages {
		return []T{}, pages
	}
	start := (page - 1) * perPage
	end := start + perPage
	if end > total {
		end = total
	}
	return items[start:end], pages
}

// setLinkHeader writes the RFC 5988 Link header for a paginated list,
// with absolute URLs on this server's origin, as the pagination check
// follows rel="next".
func (h *Handler) setLinkHeader(w http.ResponseWriter, r *http.Request, page, perPage, pages int) {
	if pages <= 1 {
		return
	}
	parts := []string{}
	link := func(p int, rel string) string {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(p))
		q.Set("per_page", strconv.Itoa(perPage))
		u := &url.URL{Scheme: schemeOf(r), Host: r.Host, Path: r.URL.Path, RawQuery: q.Encode()}
		return "<" + u.String() + ">; rel=\"" + rel + "\""
	}
	if page < pages {
		parts = append(parts, link(page+1, "next"))
	}
	if page > 1 {
		parts = append(parts, link(page-1, "prev"))
	}
	parts = append(parts, link(1, "first"))
	parts = append(parts, link(pages, "last"))
	w.Header().Set("Link", strings.Join(parts, ", "))
}

func schemeOf(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		return forwarded
	}
	return "http"
}

// ServeHTTP routes one REST request. The path is the part after /api/v3.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v3")
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		h.handleRoot(w, r)
		return
	}
	if !h.authenticate(w, r) {
		return
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	switch segments[0] {
	case "meta":
		h.handleMeta(w, r, segments)
	case "user":
		h.handleUser(w, r, segments)
	case "repos":
		h.handleRepos(w, r, segments)
	default:
		writeError(w, http.StatusNotFound, "Not Found")
	}
}

// segmentsAfter returns the path segments after a prefix, or nil when the
// path is shorter than the prefix.
func segmentsAfter(segments []string, prefix ...string) []string {
	if len(segments) < len(prefix) {
		return nil
	}
	for i, p := range prefix {
		if segments[i] != p {
			return nil
		}
	}
	return segments[len(prefix):]
}
