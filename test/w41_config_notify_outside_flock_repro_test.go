package test

// w41_config_notify_outside_flock_repro_test.go: the repro for the
// `pf config set` writer-flock defect carried by (F1).
//
// The writer flock (`<cli.toml>.lock`) exists to serialize the local
// read-modify-write: the read, the validation and the atomic write.
// `POST /config/reload` is a cross-process round trip to the daemon and
// must NOT run while that flock is held — a slow or stuck daemon would
// hold every other `pf config` writer for the client timeout. This test
// stands a fake daemon on a real unix socket and, from inside the
// /config/reload handler (i.e. while the CLI is waiting on the round
// trip), tries to take the writer flock the same way a second writer
// would. The observed fact is the assertion.
//
// Baseline posture: on the claim tree `pf config` does not exist yet, so
// this test is an assertion-level red (the verb is unknown) and never a
// build red. Once the sealed sequence is carried, the same
// test compiles unchanged and fails on the flock fact alone.

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// w41FlockProbe is the fake daemon's observation of one /config/reload
// round trip: how many arrived and whether the writer flock was free at
// that instant.
type w41FlockProbe struct {
	mu     sync.Mutex
	reload int
	lock   string // "free" | "held" | "missing" | "error:<msg>"
	detail string
	done   chan struct{}
}

func (p *w41FlockProbe) record() {
	p.mu.Lock()
	p.reload++
	p.mu.Unlock()
	select {
	case p.done <- struct{}{}:
	default:
	}
}

func (p *w41FlockProbe) snapshot() (int, string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reload, p.lock, p.detail
}

// w41ProbeFlock tries to take the writer flock exactly as a concurrent
// `pf config` writer would: a separate open plus LOCK_EX|LOCK_NB.
func w41ProbeFlock(path string) (state, detail string) {
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

// w41FakeDaemon stands a unix-socket HTTP server that answers
// /config/reload with a live-class outcome and probes the writer flock
// as the request arrives.
func w41FakeDaemon(t *testing.T, sock, lockPath string) *w41FlockProbe {
	t.Helper()
	p := &w41FlockProbe{done: make(chan struct{}, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("/config/reload", func(w http.ResponseWriter, r *http.Request) {
		state, detail := w41ProbeFlock(lockPath)
		p.mu.Lock()
		p.lock, p.detail = state, detail
		p.mu.Unlock()
		p.record()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"outcomes":[{"key":"claude_bin","class":"live","status":"applied"}]}`)
	})
	if err := os.Remove(sock); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen %s: %v", sock, err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return p
}

// TestW41ConfigSetNotifiesDaemonAfterReleasingTheWriterFlock is F1:
// `pf config set claude_bin …` must have released the `<cli.toml>.lock`
// writer flock before it asks the daemon to reload. Observed fact:
// the /config/reload handler cannot take the flock (it is still held by
// the CLI waiting on the answer).
func TestW41ConfigNotifyOutsideWriterFlock(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "cli.toml")
	// A mapping-less file keeps the write on the flat-key path: no
	// mapping change, so no /config/check preflight — the only daemon
	// round trip is /config/reload.
	if err := os.WriteFile(cfg, []byte("server = \"http://127.0.0.1:1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(shortTmp(t, "w41sock"), "daemon.sock")
	probe := w41FakeDaemon(t, sock, cfg+".lock")

	out, code := w113Pf(t, nil, w113Env{home: t.TempDir(), config: cfg, socket: sock},
		"config", "set", "claude_bin", "/opt/w41-new-claude")
	if code != 0 {
		t.Fatalf("pf config set claude_bin: exit %d (output %q)", code, out)
	}
	select {
	case <-probe.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the daemon never received POST /config/reload (output %q)", out)
	}
	reloads, state, detail := probe.snapshot()
	if reloads == 0 {
		t.Fatalf("the daemon never received POST /config/reload (output %q)", out)
	}
	switch state {
	case "missing":
		t.Fatalf("premise broken: %s did not exist when /config/reload arrived (%s) — "+
			"this test probes the writer flock at the documented sibling path", cfg+".lock", detail)
	case "held":
		t.Fatalf("F1 reproduced: the CLI still held the writer flock (%s: %s) while it waited on "+
			"POST /config/reload — a concurrent `pf config` writer is blocked behind the daemon round trip",
			cfg+".lock", detail)
	case "free":
		// the required order: write + note, release, then notify
	default:
		t.Fatalf("could not determine the writer-flock state at notify time: %s (%s)", state, detail)
	}
	if detail != "" && state == "free" {
		t.Logf("flock probe detail: %s", detail)
	}
}
