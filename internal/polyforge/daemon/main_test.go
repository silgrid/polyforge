package daemon

// main_test.go: #171 additions plus the TestMain env isolation. The
// harness-root tests pin the polyforge#171 launch surface: the former
// --harness-sessions-root fails the boot at flag parsing (unknown flag —
// the loud failure, never a silent path move) and the --harness-root
// pass-through into the assembled config (the
// runtime derives the three harness dirs from it; the default stays the
// data dir).

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/testenv"
)

// TestMain isolates HOME/XDG/POLYFORGE_* so no test can touch the real install.
func TestMain(m *testing.M) { testenv.Main(m) }

// TestOldSessionsRootFlagRefuses: the pre-#171 flag name is not parsed —
// an old deployment fails loudly at flag parsing (exit 2), never silently
// moves its harness state under the renamed semantics.
func TestOldSessionsRootFlagRefuses(t *testing.T) {
	oldName := "--harness" + "-sessions" + "-root"
	var stderr bytes.Buffer
	code := Main([]string{oldName, filepath.Join(t.TempDir(), "sessions")}, nil, &bytes.Buffer{}, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (unknown-flag refusal), stderr: %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), oldName) {
		t.Fatalf("refusal %q does not name the old flag", stderr.String())
	}
}

// TestHarnessRootPassThrough: the assembled daemon config carries the
// --harness-root override (and derives the default from the data dir when no
// override is set) — the runtime's three harness dirs are derived off it.
func TestHarnessRootPassThrough(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cli.toml")
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(filepath.Join(ws, "capsules"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "server = \"http://127.0.0.1:1\"\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := mapping.Build([]mapping.Entry{{Workspace: ws, Projects: []string{"e2e"}}})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "harness-root")
	boot := func(cfg Config) Config {
		cfg.Mapping = plan
		cfg.Hub = newFakeHub()
		cfg.SocketPath = filepath.Join(t.TempDir(), "s.sock")
		cfg.AgentStarter = fakeEngineStarter()
		d, err := New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d.cfg
	}
	got := boot(Config{DataDir: t.TempDir(), HarnessRoot: root})
	if got.HarnessRoot != root {
		t.Fatalf("the override did not pass through: %q != %q", got.HarnessRoot, root)
	}
	def := boot(Config{DataDir: t.TempDir()})
	if def.HarnessRoot != def.DataDir {
		t.Fatalf("the default harness root %q is not the data dir %q", def.HarnessRoot, def.DataDir)
	}
}

// TestBareStartMachineID: a daemon started with no -machine-id and no
// recorded daemon-id records the id ensure derives (m-<short host>).
func TestBareStartMachineID(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cli.toml")
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(filepath.Join(ws, "capsules"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "server = \"http://127.0.0.1:1\"\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(t.TempDir(), "data")
	// an explicit operator agent dir without auth.json refuses the start
	// in New, after the id is recorded
	var stderr bytes.Buffer
	code := Main([]string{"--config", cfgPath, "-data-dir", data, "-operator-agent-dir", t.TempDir()}, nil, &bytes.Buffer{}, &stderr)
	if code != ExitNeedsOperator {
		t.Fatalf("exit = %d, want %d; stderr %q", code, ExitNeedsOperator, stderr.String())
	}
	raw, err := os.ReadFile(filepath.Join(data, daemonIDFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != hostMachineID() {
		t.Fatalf("recorded daemon id %q, want ensure's derivation %q", got, hostMachineID())
	}

}

// TestApplyDaemonTablePrecedence (decision 2026-10-16 G2): an explicit
// command-line flag > the cli.toml [daemon] table > the built-in default.
func TestApplyDaemonTablePrecedence(t *testing.T) {
	table := cli.DaemonSettings{StepTimeout: 45 * time.Minute, StallWindow: -time.Second,
		HarnessRoot: "/t/root", OperatorAgentDir: "/t/op", MaxConcurrentJobs: 6}
	parse := func(args ...string) (time.Duration, time.Duration, string, string, int) {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		st := fs.Duration("step-timeout", 0, "")
		sw := fs.Duration("stall-window", 0, "")
		hr := fs.String("harness-root", "", "")
		op := fs.String("operator-agent-dir", "", "")
		mx := fs.Int("max-concurrent-jobs", 0, "")
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		applyDaemonTable(fs, table, st, sw, hr, op, mx)
		return *st, *sw, *hr, *op, *mx
	}
	st, sw, hr, op, mx := parse()
	if st != 45*time.Minute || sw != -time.Second || hr != "/t/root" || op != "/t/op" || mx != 6 {
		t.Fatalf("table not applied: %v %v %q %q %d", st, sw, hr, op, mx)
	}
	st, sw, hr, op, mx = parse("-step-timeout", "5m", "-stall-window", "0", "-harness-root", "/f/root",
		"-operator-agent-dir", "/f/op", "-max-concurrent-jobs", "2")
	if st != 5*time.Minute || sw != 0 || hr != "/f/root" || op != "/f/op" || mx != 2 {
		t.Fatalf("explicit flags must beat the table: %v %v %q %q %d", st, sw, hr, op, mx)
	}
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	st0, sw0 := fs.Duration("step-timeout", 0, ""), fs.Duration("stall-window", 0, "")
	hr0, op0, mx0 := fs.String("harness-root", "", ""), fs.String("operator-agent-dir", "", ""), fs.Int("max-concurrent-jobs", 0, "")
	applyDaemonTable(fs, cli.DaemonSettings{}, st0, sw0, hr0, op0, mx0)
	if *st0 != 0 || *sw0 != 0 || *hr0 != "" || *op0 != "" || *mx0 != 0 {
		t.Fatal("an empty table must leave the built-in defaults")
	}
}

// TestRetiredEnvWarnings (decision 2026-10-16 G2): a retired harness
// variable set to a value differing from the resolved one is named, with
// the [daemon] key that replaces it; unset or equal values are silent.
func TestRetiredEnvWarnings(t *testing.T) {
	env := map[string]string{"POLYFORGE_HARNESS_ROOT": "/old/root", "POLYFORGE_OPERATOR_AGENT_DIR": "/same/op/"}
	got := retiredEnvWarnings(func(k string) string { return env[k] }, "", "/data", "/same/op")
	if len(got) != 1 || !strings.Contains(got[0], "POLYFORGE_HARNESS_ROOT=/old/root") ||
		!strings.Contains(got[0], "the daemon uses /data") || !strings.Contains(got[0], `[daemon] harness_root = "/old/root"`) {
		t.Fatalf("warnings = %q", got)
	}
	if got := retiredEnvWarnings(func(string) string { return "" }, "", "/data", ""); len(got) != 0 {
		t.Fatalf("no env, no warning: %q", got)
	}
	if got := retiredEnvWarnings(func(k string) string { return env[k] }, "/old/root", "/data", "/same/op"); len(got) != 0 {
		t.Fatalf("an env value equal to the resolved one is silent: %q", got)
	}
}

// TestRetiredEnvWarningsReachEventsAndStatus: the boot warnings land a
// machine-level retired_env_ignored event and ride GET /status.
func TestRetiredEnvWarningsReachEventsAndStatus(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) { c.EnvWarnings = []string{"POLYFORGE_HARNESS_ROOT=/x is set but no longer read"} })
	if !hasKind(eventKinds(td.Daemon, ""), observe.EvRetiredEnvIgnored) {
		t.Fatalf("no retired_env_ignored event: %v", eventKinds(td.Daemon, ""))
	}
	raw, err := td.client.Status()
	if err != nil || !strings.Contains(string(raw), "POLYFORGE_HARNESS_ROOT=/x") {
		t.Fatalf("status lacks the warning: %s %v", raw, err)
	}
}
