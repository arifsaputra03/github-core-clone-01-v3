// Package http is the forge's transport layer: the router, the bearer
// authentication, the request-id header, and the JSON helpers. The git
// protocol lives in internal/git (smart.go routes to it); the REST
// endpoints live in rest.go.
package http

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"forge/internal/config"
	"forge/internal/service"
)

// Server is the forge's HTTP server.
type Server struct {
	cfg *config.Config
	svc *service.Service
	mux *http.ServeMux
}

// New builds a Server. The routes are registered in rest.go and smart.go.
func New(cfg *config.Config, svc *service.Service) *Server {
	s := &Server{cfg: cfg, svc: svc, mux: http.NewServeMux()}
	s.registerRoutes()
	return s
}

// Handler returns the server's handler, wrapped in the request-id and
// JSON-content-type middleware.
func (s *Server) Handler() http.Handler {
	return s.withRequestID(s.mux)
}

// withRequestID sets X-GitHub-Request-Id on every response, including
// errors and 401s, as the protocol group requires.
func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-GitHub-Request-Id", newRequestID())
		next.ServeHTTP(w, r)
	})
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// writeJSON writes a JSON object with the given status. Every REST reply
// is application/json, never HTML, including errors.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error object: {"message": "<string>"}.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"message": message})
}

// authenticate checks the Authorization header. A missing or wrong bearer
// token is a 401 with {"message": "Bad credentials"}. When no token is
// configured (development mode) any non-empty bearer token is accepted;
// a missing token is still a 401.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) bool {
	token := ""
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimSpace(auth[len("Bearer "):])
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "Bad credentials")
		return false
	}
	if s.cfg.Token != "" {
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) != 1 {
			writeError(w, http.StatusUnauthorized, "Bad credentials")
			return false
		}
	}
	return true
}

// baseURL returns the forge's own base URL, used to fill in the
// repository object's URLs. It comes from the request, so the forge works
// behind any host and port.
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

// Listen starts listening (on an ephemeral port when cfg.Port is 0) and
// serves. It returns the port the server is actually on.
func (s *Server) Listen() (int, error) {
	addr := "127.0.0.1:0"
	if s.cfg.Port != 0 {
		addr = "127.0.0.1:" + itoa(s.cfg.Port)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	go func() {
		_ = http.Serve(listener, s.Handler())
	}()
	return port, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
