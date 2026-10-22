package plugintest

import (
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// Upstream is a fake upstream service for plugin tests: it answers requests from a script and records every request, so a test can check
// what the plugin sent. Its URL goes into the plugin's base URL setting (or Options.Env).
//
//	up := plugintest.NewUpstream(t)
//	up.Handle("GET /search", plugintest.JSON(200, map[string]any{"results": []any{}}))
//	up.Handle("POST /token", plugintest.Sequence(plugintest.Status(400), plugintest.JSON(200, tokens)))
//	// ... call the plugin with up.URL ...
//	req := up.Requests()[0] // req.Method, req.Path, req.Query, req.Header, req.Body
//
// A request no route matches is answered with 404 and makes the test fail.
type Upstream struct {
	// URL is the base address of the fake service, without a trailing slash: plain HTTP on the loopback address, or HTTPS
	// for NewTLSUpstream.
	URL string
	// CertFile is the PEM file with the certificate of a NewTLSUpstream service (empty for plain HTTP).
	CertFile string

	t    *testing.T
	mux  *http.ServeMux
	mu   sync.Mutex
	reqs []RecordedRequest
}

// RecordedRequest is a request the fake service received.
type RecordedRequest struct {
	Method string
	Path   string
	Query  map[string][]string
	Header http.Header
	Body   []byte
}

// NewUpstream starts a fake service that stops when the test ends.
func NewUpstream(t *testing.T) *Upstream {
	t.Helper()
	u := &Upstream{t: t, mux: http.NewServeMux()}
	srv := httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(srv.Close)
	u.URL = srv.URL
	return u
}

// NewTLSUpstream is NewUpstream over HTTPS, for plugins that only accept an https base URL. Its certificate is self-signed,
// so a plugin trusts it only when its environment names the certificate file: pass Env() in Options.Env, or set the
// variable in Options.Setup. Go reads SSL_CERT_FILE only on Linux and other Unix systems; on macOS and Windows it checks
// certificates with the system and ignores the variable, so NewTLSUpstream skips the test there (t.Skip) instead of
// letting it fail. Run such tests on Linux, for example in CI.
//
//	up := plugintest.NewTLSUpstream(t)
//	plugintest.Run(t, bin, plugintest.Options{Config: map[string]string{"base_url": up.URL}, Env: up.Env()})
func NewTLSUpstream(t *testing.T) *Upstream {
	t.Helper()
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skipf("NewTLSUpstream needs SSL_CERT_FILE, which Go ignores on %s; run this test on Linux", runtime.GOOS)
	}
	u := &Upstream{t: t, mux: http.NewServeMux()}
	srv := httptest.NewTLSServer(http.HandlerFunc(u.serve))
	t.Cleanup(srv.Close)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	u.CertFile = filepath.Join(t.TempDir(), "upstream-cert.pem")
	if err := os.WriteFile(u.CertFile, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	u.URL = srv.URL
	return u
}

// Env is the environment that makes a plugin trust this service's certificate (SSL_CERT_FILE), empty for plain HTTP.
func (u *Upstream) Env() map[string]string {
	if u.CertFile == "" {
		return nil
	}
	return map[string]string{"SSL_CERT_FILE": u.CertFile}
}

func (u *Upstream) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.reqs = append(u.reqs, RecordedRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone(), Body: body})
	u.mu.Unlock()
	if _, pattern := u.mux.Handler(r); pattern == "" {
		u.t.Errorf("upstream: no route for %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	u.mux.ServeHTTP(w, r)
}

// Handle answers requests matching pattern ("GET /path", "POST /path/{id}", the net/http ServeMux syntax) with h.
func (u *Upstream) Handle(pattern string, h http.HandlerFunc) { u.mux.HandleFunc(pattern, h) }

// Requests returns the requests received so far, oldest first.
func (u *Upstream) Requests() []RecordedRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]RecordedRequest(nil), u.reqs...)
}

// JSON answers with the status and v encoded as JSON.
func JSON(status int, v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
}

// Status answers with the status and no body.
func Status(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }
}

// RateLimited answers 429 with a Retry-After header of the given seconds.
func RateLimited(retryAfterSeconds string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", retryAfterSeconds)
		w.WriteHeader(http.StatusTooManyRequests)
	}
}

// Sequence answers the first request with the first handler, the second with the second, and so on; the last handler answers every later request.
func Sequence(hs ...http.HandlerFunc) http.HandlerFunc {
	var mu sync.Mutex
	n := 0
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		h := hs[min(n, len(hs)-1)]
		n++
		mu.Unlock()
		h(w, r)
	}
}
