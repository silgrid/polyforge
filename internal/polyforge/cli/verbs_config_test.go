package cli

// verbs_config_test.go: the writer-flock order of `pf config set|unset`
// (workbench#41 F1). The flock serializes the local read-modify-write; the
// daemon notification is a cross-process round trip and must run after the
// release — otherwise a slow daemon holds every other `pf config` writer
// for the client timeout.

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// probeWriterFlock tries to take path's flock exactly as a concurrent
// `pf config` writer would: a separate open plus LOCK_EX|LOCK_NB.
func probeWriterFlock(path string) (state, detail string) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return "missing", err.Error()
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return "held", err.Error()
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return "free", ""
}

// TestConfigSetNotifiesDaemonAfterReleasingTheWriterFlock pins the order:
// the atomic write (and the no-mapping note) finish, the writer flock is
// released, and only then does `pf config set` send POST /config/reload.
// The fake daemon probes the lock from inside the handler — the instant
// the CLI is waiting on the round trip, which is not a timing race.
func TestConfigSetNotifiesDaemonAfterReleasingTheWriterFlock(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cli.toml")
	// A mapping-less file keeps the write on the flat-key path: no
	// mapping change, so no /config/check preflight — the only daemon
	// round trip is /config/reload.
	if err := os.WriteFile(cfgPath, []byte("server = \"http://127.0.0.1:1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The unix socket path stays short: t.TempDir() alone can exceed the
	// sun_path limit on the platform.
	sockDir, err := os.MkdirTemp("", "pfcfg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "daemon.sock")

	var mu sync.Mutex
	state, detail, reloads := "no-reload", "", 0
	mux := http.NewServeMux()
	mux.HandleFunc("/config/reload", func(w http.ResponseWriter, r *http.Request) {
		s, d := probeWriterFlock(cfgPath + ".lock")
		mu.Lock()
		state, detail = s, d
		reloads++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"outcomes":[{"key":"claude_bin","class":"live","status":"applied"}]}`)
	})
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen %s: %v", sock, err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	var out bytes.Buffer
	ctx := &Context{ConfigPath: cfgPath, FlagSocket: sock, Stdout: &out, Stderr: &out}
	if err := runConfigSet(ctx, []string{"claude_bin", "/opt/claude-b"}); err != nil {
		t.Fatalf("pf config set claude_bin: %v (output %q)", err, out.String())
	}

	mu.Lock()
	gotState, gotDetail, gotReloads := state, detail, reloads
	mu.Unlock()
	if gotReloads == 0 {
		t.Fatalf("the daemon never received POST /config/reload (output %q)", out.String())
	}
	switch gotState {
	case "missing":
		t.Fatalf("premise broken: %s did not exist when /config/reload arrived (%s) — "+
			"this test probes the writer flock at the documented sibling path", cfgPath+".lock", gotDetail)
	case "held":
		t.Fatalf("the writer flock (%s) was still held while the CLI waited on POST /config/reload (%s) — "+
			"a concurrent `pf config` writer is blocked behind the daemon round trip", cfgPath+".lock", gotDetail)
	case "free":
	default:
		t.Fatalf("could not determine the writer-flock state at notify time: %s (%s)", gotState, gotDetail)
	}

	// the write itself still landed, and the outcome still comes from the
	// daemon's answer
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `claude_bin = "/opt/claude-b"`) {
		t.Fatalf("the edit was not written:\n%s", raw)
	}
	if !strings.Contains(out.String(), "claude_bin: applied (live)") {
		t.Fatalf("the daemon's reload outcome was not reported: %q", out.String())
	}
}
