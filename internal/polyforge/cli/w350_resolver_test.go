package cli

// w350_resolver_test.go ( D2): the cwd step of the profile
// resolver reads the Host's effective index and nothing else — a Cell file
// missing on disk never demotes a mapped cwd — and a business command
// whose effective index cannot be read fails closed (DAEMON-410 naming the
// index) before any request leaves the CLI; an explicit route (a chat
// WorkRef, --profile) is unaffected.

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

func TestW350EffectiveIndexIsTheOnlyCwdSource(t *testing.T) {
	clearEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no cli-alpha.toml on disk at all
	wsA := t.TempDir()
	sock := fakeProfilesHost(t, []cellcfg.IndexEntry{{Profile: "alpha", Workspace: wsA}})
	t.Setenv("PF_DAEMON_SOCKET", sock)
	if r := profileOf(t, wsA); r.Profile != "alpha" || r.Source != ProfileSourceCwd {
		t.Fatalf("the Host's effective mapping must win over the files on disk: %+v", r)
	}
	if r := profileOf(t, t.TempDir()); r.Profile != "default" || r.Source != ProfileSourceDefault {
		t.Fatalf("an unmapped cwd with a readable index answers default: %+v", r)
	}
}

// w350BrokenHost serves GET /profiles 500 and counts every other request.
func w350BrokenHost(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	sock := filepath.Join(shortTempDir(t), "b.sock")
	lst, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var other atomic.Int32
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/profiles" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":"DAEMON-500","detail":"index unreadable"}`))
			return
		}
		other.Add(1)
		_, _ = w.Write([]byte(`{}`))
	})}
	go srv.Serve(lst)
	t.Cleanup(func() { srv.Close() })
	return sock, &other
}

func TestW350UnreadableIndexFailsBusinessClosed(t *testing.T) {
	sock, other := w350BrokenHost(t)
	for _, argv := range [][]string{
		{"--json", "projects", "list"}, // the AIHub proxy client
		{"submit", "wi_x"},             // the daemon client
	} {
		code, stdout, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": sock}, argv...)
		out := stdout + stderr
		if code != 1 || !strings.Contains(out, "DAEMON-410") || !strings.Contains(out, "effective workspace index") ||
			!strings.Contains(out, "nothing was sent") || !strings.Contains(out, "pf daemon ensure") {
			t.Fatalf("pf %v with an unreadable index: exit %d (%q)", argv, code, out)
		}
	}
	if n := other.Load(); n != 0 {
		t.Fatalf("%d business request(s) left the CLI after the index read failed", n)
	}
	// --profile is a resolution of its own: the index is not needed
	if code, _, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": sock}, "--profile", "alpha", "submit", "wi_x"); strings.Contains(stderr, "effective workspace index") {
		t.Fatalf("an explicit --profile must not need the index: exit %d (%q)", code, stderr)
	}
	// a local verb keeps its on-disk fallback
	if code, _, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": sock}, "profile"); code != 0 {
		t.Fatalf("pf profile with an unreadable index: exit %d (%q)", code, stderr)
	}
}

func TestW350ExplicitRouteReplacesTheRefusal(t *testing.T) {
	sock, other := w350BrokenHost(t)
	clearEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx := &Context{Cfg: Config{DaemonSocket: sock}, FlagSocket: sock, Stdout: os.Stdout, Stderr: os.Stderr}
	d := ctx.daemon()
	if _, err := d.Status(); err == nil || !strings.Contains(err.Error(), "effective workspace index") {
		t.Fatalf("an unresolved client must refuse: %v", err)
	}
	if other.Load() != 0 {
		t.Fatal("the refusal dialed the daemon")
	}
	d.Profile = "alpha" // e.g. a chat subscription's WorkRef.profile
	if _, err := d.Status(); err != nil && strings.Contains(err.Error(), "effective workspace index") {
		t.Fatalf("an explicit route must replace the refusal: %v", err)
	}
	if other.Load() != 1 {
		t.Fatalf("the explicitly routed request did not reach the daemon (%d)", other.Load())
	}
}
