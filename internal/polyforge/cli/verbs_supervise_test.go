package cli

// verbs_supervise_test.go (polyforge#160, AC1): `pf supervise`'s launch
// plan — the canonical daemon argv, the daemon's allow-list environment
// with the container marker (never a credential, never a stale tmux
// marker), Pi Web started from its checkout (absent → not started), the
// aihub server only with --with-aihub, and the 45s grace floor — plus
// the usage refusals, taken before anything starts.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervisor"
)

func TestSuperviseVerbPlan(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "cli.toml")
	data := filepath.Join(dir, "data")
	piweb := filepath.Join(data, "release", "current", "pi-web")
	if err := os.MkdirAll(piweb, 0o755); err != nil {
		t.Fatal(err)
	}
	workflows := filepath.Join(data, "release", "current", "workflows")
	base := superviseOpts{pfBin: "/bin/pf", home: "/root", configPath: cfg, dataDir: data,
		workflowsDir: workflows, piWebDir: piweb, withAIHub: true,
		grace: supervisor.MinStopGrace,
		environ: []string{"HOME=/root", "PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=sk-secret",
			"GOPATH=/root/go", supervisor.MarkerEnv + "=tmux:polyforge-daemon"}}

	rs, err := planSupervise(base)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Grace != supervisor.MinStopGrace {
		t.Fatalf("the grace passed through as %s", rs.Grace)
	}

	// the daemon child: the canonical launch argv
	daemon := superviseChild(rs, "daemon")
	if daemon == nil {
		t.Fatal("no daemon child")
	}
	wantArgv := []string{"/bin/pf", "daemon", "--config", cfg, "-data-dir", data, "-workflows-dir", workflows}
	if strings.Join(daemon.Argv, " ") != strings.Join(wantArgv, " ") {
		t.Fatalf("the daemon argv = %v, want %v", daemon.Argv, wantArgv)
	}
	// the daemon's environment: the ensure allow-list with HOME pinned
	// and the container marker — never a credential, never the stale
	// tmux marker
	env := strings.Join(daemon.Env, "\n")
	for _, want := range []string{
		"HOME=/root", "PATH=/usr/bin:/bin", "GOPATH=/root/go",
		supervisor.MarkerEnv + "=" + supervisor.MarkerContainerValue,
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("the daemon environment lacks %q: %s", want, env)
		}
	}
	for _, not := range []string{"ANTHROPIC", "sk-secret", "tmux:polyforge-daemon"} {
		if strings.Contains(env, not) {
			t.Fatalf("the daemon environment must not carry %q: %s", not, env)
		}
	}
	if daemon.Log != filepath.Join(data, "daemon.log") {
		t.Fatalf("the daemon logs to %s, want <data-dir>/daemon.log", daemon.Log)
	}

	// Pi Web: npm start (the LAN form — a container must bind 0.0.0.0)
	// from its checkout, its environment the container's with the marker
	// REPLACED (a stale tmux marker never survives into the children)
	piWeb := superviseChild(rs, "pi-web")
	if piWeb == nil {
		t.Fatal("no pi-web child though its checkout exists")
	}
	if strings.Join(piWeb.Argv, " ") != "npm run start:lan" || piWeb.Dir != piweb {
		t.Fatalf("the pi-web child = %v (dir %s), want npm run start:lan in %s", piWeb.Argv, piWeb.Dir, piweb)
	}
	penv := strings.Join(piWeb.Env, "\n")
	if !strings.Contains(penv, supervisor.MarkerEnv+"="+supervisor.MarkerContainerValue) || strings.Contains(penv, "tmux:polyforge-daemon") {
		t.Fatalf("the pi-web environment must carry the container marker and no stale one: %s", penv)
	}
	// the container's own variables reach Pi Web (it is not behind the
	// daemon's allow-list)
	if !strings.Contains(penv, "ANTHROPIC_API_KEY=sk-secret") {
		t.Fatal("the pi-web environment must be the container's passthrough")
	}

	// the aihub server: only with --with-aihub
	aihub := superviseChild(rs, "aihub")
	if aihub == nil {
		t.Fatal("no aihub child though --with-aihub was given")
	}
	if strings.Join(aihub.Argv, " ") != "/bin/pf aihub" {
		t.Fatalf("the aihub child = %v, want `pf aihub`", aihub.Argv)
	}

	// without --with-aihub there is no aihub child; without a Pi Web
	// checkout there is no pi-web child
	noAIHub := base
	noAIHub.withAIHub = false
	rs, err = planSupervise(noAIHub)
	if err != nil {
		t.Fatal(err)
	}
	if superviseChild(rs, "aihub") != nil || superviseChild(rs, "pi-web") == nil {
		t.Fatal("--with-aihub off must drop only the aihub child")
	}
	noPiWeb := base
	noPiWeb.piWebDir = filepath.Join(data, "absent")
	rs, err = planSupervise(noPiWeb)
	if err != nil {
		t.Fatal(err)
	}
	if superviseChild(rs, "pi-web") != nil || superviseChild(rs, "aihub") == nil {
		t.Fatal("an absent pi-web checkout must drop only the pi-web child")
	}

	// the grace floor: a grace below the 45s minimum is refused
	short := base
	short.grace = 30 * time.Second
	if _, err := planSupervise(short); err == nil || !strings.Contains(err.Error(), "45s") {
		t.Fatalf("a grace below the minimum must be refused naming it, got %v", err)
	}
}

func TestSuperviseMainRefusals(t *testing.T) {
	var out, errb bytes.Buffer
	if code := SuperviseMain([]string{"--grace", "10s"}, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "45s") {
		t.Fatalf("a grace below the minimum: exit %d stderr %q, want 2 naming the floor", code, errb.String())
	}
	if code := SuperviseMain([]string{"--bogus"}, nil, &out, &errb); code != 2 {
		t.Fatalf("an unknown flag: exit %d, want 2", code)
	}
	if code := SuperviseMain([]string{"extra"}, nil, &out, &errb); code != 2 {
		t.Fatalf("a stray argument: exit %d, want 2", code)
	}
}
