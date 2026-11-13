package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"encoding/json"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// capturedRequest is one recorded request against a fake server.
type capturedRequest struct {
	Method string
	Path   string // path + query
	Header http.Header
	Body   string
}

// reqRecorder collects requests behind a mutex.
type reqRecorder struct {
	mu   sync.Mutex
	reqs []capturedRequest
}

func (r *reqRecorder) record(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.reqs = append(r.reqs, capturedRequest{
		Method: req.Method,
		Path:   req.URL.RequestURI(),
		Header: req.Header.Clone(),
		Body:   string(body),
	})
	r.mu.Unlock()
}

func (r *reqRecorder) all() []capturedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]capturedRequest, len(r.reqs))
	copy(out, r.reqs)
	return out
}

func (r *reqRecorder) last() capturedRequest {
	all := r.all()
	if len(all) == 0 {
		return capturedRequest{}
	}
	return all[len(all)-1]
}

// newFakeAihub starts an httptest aihub that records every request and
// answers from the given handler (rec is filled before the handler
// runs). The returned cleanup closes the server.
func newFakeAihub(t *testing.T, rec *reqRecorder, answer func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		answer(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newFakeDaemon starts a REAL unix-socket daemon serving handler; the
// returned string is the socket path for NewDaemonClient / --socket.
func newFakeDaemon(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "polyforge.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen: %v", err)
	}
	// Like a real Host, the fake answers the CLI resolver's GET /profiles
	// (an empty effective index: every cwd resolves to default) — a
	// business command never routes without reading it (profile.go).
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/profiles" {
			_, _ = w.Write([]byte(`{"profiles":[],"index":[]}`))
			return
		}
		handler.ServeHTTP(w, r)
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// deadAihubURL returns a server URL whose listener is already closed:
// dialing it is a guaranteed connection-refused, for transport-failure
// goldens on the aihub face.
func deadAihubURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	return url
}

// runPf drives the full CLI the way main does; returns the exit code.
// env pairs are applied with t.Setenv for the duration of the test.
// XDG_CONFIG_HOME defaults to a fresh scratch dir so no ambient config
// file leaks into a verb run — pass XDG_CONFIG_HOME explicitly (see
// withMappingConfig) to point a run at a fixture.
func runPf(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	clearEnv(t)
	if _, ok := env["XDG_CONFIG_HOME"]; !ok {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
	env = viaCellProxy(t, env)
	for k, v := range env {
		t.Setenv(k, v)
	}
	var stdout, stderr bytes.Buffer
	code := Run(args, bytes.NewReader(nil), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// withMappingConfig writes a scratch cli.toml whose [workspace] section
// maps the project "polyforge" to a scratch workspace dir, and answers
// the XDG_CONFIG_HOME env entry that points a runPf call at it (the
// default config location is the only thing placement verbs' mapping
// validation reads).
func withMappingConfig(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "server = \"http://mapping-config.invalid:1\"\n\n[workspace.\"" + filepath.Join(dir, "ws") + "\"]\nprojects = [\"polyforge\"]\n"
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"XDG_CONFIG_HOME": xdg}
}

// withTwoProjectMapping is withMappingConfig over TWO projects (alpha,
// beta — declaration order matters to the merged-presentation pins).
func withTwoProjectMapping(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "server = \"http://mapping-config.invalid:1\"\n\n" +
		"[workspace.\"" + filepath.Join(dir, "wsA") + "\"]\nprojects = [\"alpha\"]\n\n" +
		"[workspace.\"" + filepath.Join(dir, "wsB") + "\"]\nprojects = [\"beta\"]\n"
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"XDG_CONFIG_HOME": xdg}
}

// jsonBody is a helper for handlers answering canned JSON.
func jsonBody(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// viaCellProxy stands in for the daemon's data-plane proxy in the unit
// tests: a test that names a fake aihub with PF_SERVER (and its key with
// PF_API_KEY) gets a unix-socket front that forwards /aihub/<path> to that
// fake with the key as the Cell credential — exactly what the Host's
// ANY /aihub/{path...} route does with a Cell's connection — and every
// other path to the test's own fake daemon (PF_DAEMON_SOCKET, when set).
// The CLI itself then sees no PF_SERVER/PF_API_KEY (it refuses them) and
// reaches the fake only through the socket. A test asserting the refusal
// itself sets the variables with t.Setenv directly instead.
func viaCellProxy(t *testing.T, env map[string]string) map[string]string {
	t.Helper()
	hub, ok := env["PF_SERVER"]
	if !ok {
		return env
	}
	out := map[string]string{}
	for k, v := range env {
		if k != "PF_SERVER" && k != "PF_API_KEY" {
			out[k] = v
		}
	}
	out["PF_DAEMON_SOCKET"] = cellFront(t, hub, env["PF_API_KEY"], env["PF_DAEMON_SOCKET"])
	return out
}

// cellFront is the unix-socket stand-in for the Host's data-plane proxy:
// /aihub/<path> is forwarded to hub with key as the Cell credential, every
// other path to the inner fake daemon socket ("" = 404, a daemon that
// serves nothing else).
func cellFront(t *testing.T, hub, key, inner string) string {
	t.Helper()
	if inner != "" {
		if _, err := os.Stat(inner); err != nil {
			// the test's daemon is down: no daemon, no proxy — business
			// fails exactly like every other daemon call
			return inner
		}
	}
	hubURL, err := url.Parse(hub)
	if err != nil {
		t.Fatalf("PF_SERVER %q: %v", hub, err)
	}
	toHub := httputil.NewSingleHostReverseProxy(hubURL)
	baseDirector := toHub.Director
	toHub.Director = func(r *http.Request) {
		baseDirector(r)
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/aihub")
		r.URL.RawPath = ""
		r.Header.Del(protocol.ProfileHeader)
		r.Header.Del("Authorization")
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
	}
	toHub.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"DAEMON-503","detail":"Cell \"default\": its AIHub is unreachable: ` + strings.ReplaceAll(err.Error(), `"`, `'`) + `"}`))
	}
	var toDaemon http.Handler = http.NotFoundHandler()
	if inner != "" {
		toDaemon = &httputil.ReverseProxy{
			Director: func(r *http.Request) { r.URL.Scheme, r.URL.Host = "http", "daemon" },
			Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", inner)
			}},
		}
	}
	return newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/aihub/") {
			toHub.ServeHTTP(w, r)
			return
		}
		toDaemon.ServeHTTP(w, r)
	}))
}
