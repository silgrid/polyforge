package cli

// ops_drain_test.go (polyforge#160, AC3): `pf ops drain --upgrade
// [--force]` — the CLI face of the Host-level upgrade drain. It posts
// POST /ops/drain with no profile (Host-level), renders the daemon's
// answer, passes a busy Host's 409 through to the caller, and refuses
// every shape but --upgrade [--force] locally, --profile included.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestUpgradeDrainVerb(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("POST", "/ops/drain", `{"upgrading":true,"forced":false,"detail":"the upgrade drain is set and the Host is idle: upgrade now"}`)
	env := map[string]string{"PF_DAEMON_SOCKET": d.sock}

	// --upgrade posts the Host-level drain
	code, out, stderr := runPf(t, env, "ops", "drain", "--upgrade")
	if code != 0 {
		t.Fatalf("ops drain --upgrade: exit %d (%s)", code, stderr)
	}
	if !strings.Contains(out, "upgrade drain") || !strings.Contains(out, "idle") {
		t.Fatalf("ops drain --upgrade printed %q, want the answer's detail", out)
	}
	got := d.rec.last()
	if got.Method != "POST" || got.Path != "/ops/drain" {
		t.Fatalf("the request was %s %s, want POST /ops/drain", got.Method, got.Path)
	}
	if strings.TrimSpace(got.Body) != `{"upgrade":true,"force":false}` {
		t.Fatalf("the posted body was %s, want the upgrade drain without force", got.Body)
	}
	if v := got.Header.Get(protocol.ProfileHeader); v != "" {
		t.Fatalf("the drain request carried profile %q — the drain is Host-level", v)
	}

	// --force posts force:true
	if code, _, stderr := runPf(t, env, "ops", "drain", "--upgrade", "--force"); code != 0 {
		t.Fatalf("ops drain --upgrade --force: exit %d (%s)", code, stderr)
	}
	if got := d.rec.last(); strings.TrimSpace(got.Body) != `{"upgrade":true,"force":true}` {
		t.Fatalf("the posted body was %s, want the forced upgrade drain", got.Body)
	}

	// a busy Host's 409 reaches the caller (exit 1, the daemon's words)
	d.answers["POST /ops/drain"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"code":"DAEMON-409","detail":"the upgrade drain is set, but Cell(s) default still hold work — wait or re-run with --force"}`)
	}
	code, _, stderr = runPf(t, env, "ops", "drain", "--upgrade")
	if code == 0 || !strings.Contains(stderr, "DAEMON-409") || !strings.Contains(stderr, "hold work") {
		t.Fatalf("a busy Host's 409 must reach the caller, got exit %d stderr %q", code, stderr)
	}

	// every other shape is refused locally, --profile included: nothing
	// more reaches the daemon
	n := len(d.rec.all())
	for _, bad := range [][]string{
		{"ops", "drain"},                       // no --upgrade: nothing to set
		{"ops", "drain", "--force"},            // --force without --upgrade
		{"ops", "drain", "--upgrade", "extra"}, // stray argument
	} {
		if code, _, _ := runPf(t, env, bad...); code == 0 {
			t.Fatalf("%v accepted", bad)
		}
	}
	if code, _, stderr := runPf(t, env, "--profile", "alpha", "ops", "drain", "--upgrade"); code == 0 || !strings.Contains(stderr, "Host-level") {
		t.Fatalf("--profile must be refused naming Host-level, got exit %d stderr %q", code, stderr)
	}
	if len(d.rec.all()) != n {
		t.Fatal("a refused argument reached the daemon")
	}
	if _, _, stderr := runPf(t, env, "ops", "drain"); !strings.Contains(stderr, "--upgrade") {
		t.Fatalf("the bare drain refusal must name --upgrade: %q", stderr)
	}
}
