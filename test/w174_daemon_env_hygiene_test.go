package test

// w174 env hygiene: `pf daemon ensure` launches the daemon with exactly
// the supervisor allow-list taken from the ENSURE CALLER, never the tmux
// server's environment. The private tmux server here is started first,
// from an environment carrying junk ANTHROPIC_* / credential-looking
// variables (what a server started by a human's Claude session carries);
// the daemon the loop starts must carry none of them, and its allow-listed
// values must be the caller's. Values are compared, never printed.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/supervisor"
)

func TestW174EnsureDaemonEnvIsTheCallersAllowList(t *testing.T) {
	e := w174NewEnv(t, true)
	junk := []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL",
		"FOO_TOKEN", "POLYFORGE_V1_API_KEY", "CLAUDECODE", "PF_CHAT_ID"}
	// the polluted tmux server: its global environment holds the junk,
	// and a TZ that differs from the caller's
	srv := exec.Command("tmux", "new-session", "-d", "-s", "polluted", "sleep 600")
	srv.Env = append(slices.Clone(e.env()), "TZ=Junk/Server")
	for _, k := range junk {
		srv.Env = append(srv.Env, k+"=junk-from-tmux-server")
	}
	if out, err := srv.CombinedOutput(); err != nil {
		t.Fatalf("start the polluted tmux server: %v %s", err, out)
	}

	// the caller: no junk (stripped from whatever this test process
	// inherited), its own TZ and a locale variable
	var caller []string
	for _, kv := range e.env() {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(junk, name) && name != "TZ" && name != "LC_MESSAGES" {
			caller = append(caller, kv)
		}
	}
	caller = append(caller, "TZ=UTC0", "LC_MESSAGES=C")
	cmd := exec.Command(e.pf, "daemon", "ensure", "--config", e.cfg)
	cmd.Env = caller
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pf daemon ensure: %v %s", err, out)
	}
	pid := e.oneDaemon(t, 10*time.Second)

	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, kv := range strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00") {
		name, val, _ := strings.Cut(kv, "=")
		got[name] = val
	}
	// exactly: the caller's allow-listed names, HOME, the shell's PWD,
	// the supervision marker
	want := map[string]bool{"HOME": true, "PWD": true, supervisor.MarkerEnv: true}
	for _, kv := range caller {
		if name, _, _ := strings.Cut(kv, "="); supervisor.DaemonEnvAllowed(name) && name != "PWD" {
			want[name] = true
		}
	}
	names := func(m map[string]string) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	var wantNames []string
	for k := range want {
		wantNames = append(wantNames, k)
	}
	sort.Strings(wantNames)
	if !slices.Equal(names(got), wantNames) {
		t.Fatalf("daemon env names:\n got %v\nwant %v", names(got), wantNames)
	}
	for _, k := range junk {
		if _, ok := got[k]; ok {
			t.Fatalf("the daemon inherited %s from the tmux server", k)
		}
	}
	if got["TZ"] != "UTC0" || got["LC_MESSAGES"] != "C" {
		t.Fatal("the daemon's TZ / LC_MESSAGES are not the ensure caller's")
	}
	if got["HOME"] != e.home || got[supervisor.MarkerEnv] != "tmux:polyforge-daemon" {
		t.Fatal("the daemon's HOME / supervision marker are wrong")
	}

	// GET /status reports the names (never values), and they match
	rawSt, err := cli.NewDaemonClient(e.sock).Status()
	if err != nil {
		t.Fatal(err)
	}
	var st cli.StatusResponse
	if err := json.Unmarshal(rawSt, &st); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.EnvNames, names(got)) {
		t.Fatalf("/status env_names %v, want %v", st.EnvNames, names(got))
	}
	if strings.Contains(string(rawSt), "junk-from-tmux-server") || strings.Contains(string(rawSt), "UTC0") {
		t.Fatal("/status carries an environment value")
	}

	// and pf doctor's "daemon env" check passes on the clean daemon
	doc := exec.Command(e.pf, "--json", "doctor")
	doc.Env = e.env()
	var derr strings.Builder
	doc.Stderr = &derr
	out, _ := doc.Output()
	var rep struct {
		Checks []struct{ Name, Status, Detail string } `json:"checks"`
	}
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("pf doctor --json: %v\nstderr: %s", err, derr.String())
	}
	found := false
	for _, c := range rep.Checks {
		if c.Name == "daemon env" {
			found = true
			if c.Status != "pass" {
				t.Fatalf("daemon env check = %s: %s", c.Status, c.Detail)
			}
		}
	}
	if !found {
		t.Fatal("pf doctor --json has no daemon env check")
	}
}
