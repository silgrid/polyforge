package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// clearEnv removes the PF_* overrides for a hermetic config test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"PF_SERVER", "PF_API_KEY", "PF_DAEMON_SOCKET", "PF_USER_ID", "PF_CHAT_ID", "PF_WI_ID"} {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PI_CODING_AGENT_DIR", "") // doctor's pi check reads it
	t.Setenv("HOME", t.TempDir())       // no ambient ~/.polyforge / ~/.config leaks in
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cli.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestConfigPriorityMatrix walks one field (Server) down the whole
// precedence chain — flag > env > toml > default — plus the other flat
// fields at toml level. There is no `project` key anywhere in the chain
// (polyforge#113): the mapping lives in [workspace] sections.
func TestConfigPriorityMatrix(t *testing.T) {
	clearEnv(t)

	// Level 0: no file, no env → documented defaults, EMPTY mapping.
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if cfg.Server != defaultServer {
		t.Errorf("default Server = %q, want %q", cfg.Server, defaultServer)
	}
	if want := filepath.Join(DataDir(os.Getenv("HOME")), "daemon.sock"); cfg.DaemonSocket != want {
		t.Errorf("default DaemonSocket = %q, want %q", cfg.DaemonSocket, want)
	}
	if cfg.Mapping == nil || !cfg.Mapping.Empty() {
		t.Errorf("missing file must yield an empty (non-nil) mapping, got %+v", cfg.Mapping)
	}

	// Level 1: toml beats defaults.
	path := writeTempConfig(t, `
# pf CLI configuration (flat TOML subset + [workspace] sections)
server = "http://aihub.internal:8420"
api_key = "k_toml"
daemon_socket = "/tmp/pfd.sock"  # trailing comment
user_id = "usr_toml"

[workspace."`+t.TempDir()+`"]
projects = ["polyforge", "demo-ops"]
`)
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(toml): %v", err)
	}
	if cfg.Server != "http://aihub.internal:8420" || cfg.APIKey != "k_toml" ||
		cfg.DaemonSocket != "/tmp/pfd.sock" || cfg.UserID != "usr_toml" {
		t.Fatalf("toml level: got %+v", cfg)
	}
	if got := cfg.Mapping.Projects(); len(got) != 2 || got[0] != "polyforge" || got[1] != "demo-ops" {
		t.Fatalf("toml mapping projects = %v", got)
	}

	// Level 2: env beats toml.
	t.Setenv("PF_SERVER", "http://env:8420")
	t.Setenv("PF_API_KEY", "k_env")
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(env): %v", err)
	}
	if cfg.Server != "http://env:8420" || cfg.APIKey != "k_env" {
		t.Errorf("env must beat toml: got %+v", cfg)
	}
	// Untouched keys still come from the file.
	if cfg.DaemonSocket != "/tmp/pfd.sock" {
		t.Errorf("non-overridden keys must stay from toml: got %+v", cfg)
	}

	// Level 3: flag beats env — applied by Run; verified end-to-end in
	// TestFlagBeatsEnvEndToEnd (verbs_test.go) since only a verb call
	// proves the resolved value reached the wire.
}

// TestConfigMappingTildeAndAbsoluteKeys: the mapping loads with ~
// expanded and absolute keys literal — the same view any reader gets
// through LoadConfig (the daemon reads the same function).
func TestConfigMappingTildeAndAbsoluteKeys(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	wsAbs := t.TempDir()
	path := writeTempConfig(t, `
server = "http://x:1"

[workspace."~/w113home"]
projects = ["e2e"]

[workspace."`+wsAbs+`"]
projects = ["demo-ops"]
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	wantTilde := filepath.Join(home, "w113home")
	got := cfg.Mapping.Workspaces()
	if len(got) != 2 || got[0] != wantTilde || got[1] != wsAbs {
		t.Fatalf("workspaces = %v, want [%s %s]", got, wantTilde, wsAbs)
	}
	for proj, wantWS := range map[string]string{"e2e": wantTilde, "demo-ops": wsAbs} {
		ws, err := cfg.Mapping.WorkspaceFor(proj)
		if err != nil || ws != wantWS {
			t.Fatalf("WorkspaceFor(%q) = %q, %v", proj, ws, err)
		}
	}
}

// TestConfigMalformedMappingRejected: the loud-parse posture over the
// extended grammar — each malformed shape is a line-named error.
func TestConfigMalformedMappingRejected(t *testing.T) {
	clearEnv(t)
	cases := []struct {
		name string
		toml string
		want string
	}{
		{"bare workspace table", "[workspace]\nprojects = [\"a\"]\n", `workspace`},
		{"unquoted section key", "[workspace.~/ws]\nprojects = [\"a\"]\n", "QUOTED"},
		{"unknown section", "[other]\nkey = \"v\"\n", "other"},
		{"non-array projects", "[workspace.\"/tmp/x\"]\nprojects = \"a\"\n", "array of quoted strings"},
		{"unquoted array element", "[workspace.\"/tmp/x\"]\nprojects = [a]\n", "quoted strings"},
		{"non-string array element", "[workspace.\"/tmp/x\"]\nprojects = [1]\n", "quoted strings"},
		{"top-level projects key", "projects = [\"a\"]\n", "[workspace"},
		{"other key in section", "[workspace.\"/tmp/x\"]\ngoal = \"nope\"\n", "projects"},
		{"flat key after a workspace section", "[workspace.\"/tmp/x\"]\nprojects = [\"a\"]\nfoo = \"bar\"\n", `unknown key "foo" in section [workspace."/tmp/x"]`},
		{"duplicate section", "[workspace.\"/tmp/x\"]\nprojects = [\"a\"]\n[workspace.\"/tmp/x\"]\nprojects = [\"b\"]\n", "duplicate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeTempConfig(t, c.toml)
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatalf("malformed mapping must be a load error (%s)", c.name)
			}
			if !strings.Contains(err.Error(), "line ") {
				t.Errorf("error %q must be line-named", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q misses %q", err, c.want)
			}
		})
	}
}

// TestConfigMappingValidationRefusals surface the typed mapping errors
// through the loader (the daemon's S04/S05 bars ride the same path).
func TestConfigMappingValidationRefusals(t *testing.T) {
	clearEnv(t)
	wsA := t.TempDir()
	wsB := t.TempDir()
	t.Run("ambiguous inverse", func(t *testing.T) {
		path := writeTempConfig(t,
			"[workspace.\""+wsA+"\"]\nprojects = [\"demo-ops\"]\n[workspace.\""+wsB+"\"]\nprojects = [\"demo-ops\"]\n")
		_, err := LoadConfig(path)
		if err == nil {
			t.Fatal("ambiguous inverse must refuse loading")
		}
		var ae *mapping.AmbiguityError
		if !errorsAs(err, &ae) {
			t.Fatalf("want the typed AmbiguityError, got %T: %v", err, err)
		}
		for _, tok := range []string{"demo-ops", wsA, wsB} {
			if !strings.Contains(err.Error(), tok) {
				t.Errorf("error %q misses %q", err, tok)
			}
		}
	})
	t.Run("duplicate in one list", func(t *testing.T) {
		path := writeTempConfig(t, "[workspace.\""+wsA+"\"]\nprojects = [\"e2e\", \"e2e\"]\n")
		_, err := LoadConfig(path)
		if err == nil {
			t.Fatal("duplicate project must refuse loading")
		}
		var de *mapping.DuplicateError
		if !errorsAs(err, &de) {
			t.Fatalf("want the typed DuplicateError, got %T: %v", err, err)
		}
		for _, tok := range []string{"e2e", wsA} {
			if !strings.Contains(err.Error(), tok) {
				t.Errorf("error %q misses %q", err, tok)
			}
		}
	})
	t.Run("relative key", func(t *testing.T) {
		path := writeTempConfig(t, "[workspace.\"rel/ws\"]\nprojects = [\"e2e\"]\n")
		_, err := LoadConfig(path)
		if err == nil || !strings.Contains(err.Error(), "rel/ws") {
			t.Fatalf("relative key must refuse naming it, got %v", err)
		}
	})
	t.Run("trailing separator", func(t *testing.T) {
		path := writeTempConfig(t, "[workspace.\""+wsA+"/\"]\nprojects = [\"e2e\"]\n")
		_, err := LoadConfig(path)
		if err == nil || !strings.Contains(err.Error(), "trailing") {
			t.Fatalf("trailing separator must refuse loudly, got %v", err)
		}
	})
}

// TestConfigLegacyFallbackDropped: ~/.polyforge/config.toml is not read
// when the XDG cli.toml is absent (polyforge#113) — the command proceeds
// on the documented defaults, never the legacy file's values.
func TestConfigLegacyFallbackDropped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	xdg := filepath.Join(home, "xdg-config")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(home, ".polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "server = \"http://127.0.0.1:59999\"\napi_key = \"k_legacy\"\nproject = \"e2e\"\n"
	if err := os.WriteFile(filepath.Join(home, ".polyforge", "config.toml"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("a missing XDG file is not an error: %v", err)
	}
	if cfg.Server != defaultServer || cfg.APIKey != "" {
		t.Fatalf("legacy file leaked in: %+v", cfg)
	}
	if !cfg.Mapping.Empty() {
		t.Fatalf("legacy mapping leaked in: %+v", cfg.Mapping)
	}
	if got := DefaultConfigPath(); got != filepath.Join(xdg, "polyforge", "cli.toml") {
		t.Fatalf("DefaultConfigPath = %q, want the XDG location", got)
	}
}

// TestConfigMalformedTomlIsAnError pins the loud-failure ruling.
func TestConfigMalformedTomlIsAnError(t *testing.T) {
	clearEnv(t)
	path := writeTempConfig(t, "server = \"http://x\nbroken line without equals\n")
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("malformed toml must be an error")
	}
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }

// The watch summarizer is gone and nothing tolerates its config: a [watch.*]
// section is an unknown section and refuses like any other.
func TestConfigWatchSectionIsRefused(t *testing.T) {
	clearEnv(t)
	ws := t.TempDir()
	base := "[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"
	for _, body := range []string{
		base + "\n[watch.summarize_model.pi]\nmodel = \"prov/pi-m\"\n",
		base + "\n[other.thing]\nx = \"1\"\n",
	} {
		if _, err := LoadConfig(writeTempConfig(t, body)); err == nil || !strings.Contains(err.Error(), "unknown section") {
			t.Fatalf("an unknown section must refuse: %v\n%s", err, body)
		}
	}
}

// TestConfigProductionShapeLoads pins that a cli.toml shaped like the
// deployed one (server, api_key, daemon_socket, user_id + workspace
// sections) still loads under the strict top-level key rule.
func TestConfigProductionShapeLoads(t *testing.T) {
	clearEnv(t)
	ws := t.TempDir()
	path := writeTempConfig(t, "server = \"http://hub:8420\"\n"+
		"api_key = \"k_secret\"\n"+
		"daemon_socket = \"/run/pf/daemon.sock\"\n"+
		"user_id = \"usr_1\"\n\n"+
		"[workspace.\""+ws+"\"]\nprojects = [\"polyforge\"]\n")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("production-shaped config must load: %v", err)
	}
	if cfg.Server != "http://hub:8420" || cfg.APIKey != "k_secret" ||
		cfg.DaemonSocket != "/run/pf/daemon.sock" || cfg.UserID != "usr_1" {
		t.Fatalf("flat keys not applied: %+v", cfg)
	}
}

// TestConfigUnknownTopLevelKeyRefused: an unknown top-level key is an
// error (not silently ignored); `url`, the retired alias of `server`, is
// one of them.
func TestConfigUnknownTopLevelKeyRefused(t *testing.T) {
	clearEnv(t)
	for _, k := range []string{"url", "project", "foo"} {
		path := writeTempConfig(t, "server = \"http://x\"\n"+k+" = \"v\"\n")
		_, err := LoadConfig(path)
		want := `unknown key "` + k + `" in ` + path
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("key %q: err = %v, want it to contain %q", k, err, want)
		}
	}
}

// TestConfigResidualAuthTokenRefused: polyforge#171 hard-delete — the
// former anthropic_auth_token carries NO deprecation alias. An old
// deployment's cli.toml with the residual key fails the startup parse
// with the unknown-key error, naming the key (never silently accepted,
// never a migrate path): the credential authority is
// <harness-root>/claude-config/settings.json from now on.
func TestConfigResidualAuthTokenRefused(t *testing.T) {
	clearEnv(t)
	path := writeTempConfig(t, "anthropic_auth_token = \"sk-old-SENTINEL\"\n")
	_, err := LoadConfig(path)
	want := `unknown key "anthropic_auth_token" in ` + path
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("residual token key: err = %v, want it to contain %q", err, want)
	}
	if strings.Contains(err.Error(), "sk-old-SENTINEL") {
		t.Errorf("the error must name the KEY, never its value")
	}
}

// TestConfigDaemonTable (decision 2026-10-16 G2): the [daemon] table
// loads its five knobs; every invalid value is a typed
// DaemonKeyError naming the key and the line; a flat key after the
// header belongs to the table (TOML) and is refused there.
func TestConfigDaemonTable(t *testing.T) {
	clearEnv(t)
	ws := t.TempDir()
	base := "server = \"http://hub:8420\"\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"

	cfg, err := LoadConfig(writeTempConfig(t, base+"\n[daemon]\nstep_timeout = \"45m\"\nstall_window = \"10m\"\n"+
		"harness_root = \"/srv/h\"\noperator_agent_dir = '/srv/pi/agent'\nmax_concurrent_jobs = 6 # comment\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := DaemonSettings{StepTimeout: 45 * time.Minute, StallWindow: 10 * time.Minute, HarnessRoot: "/srv/h", OperatorAgentDir: "/srv/pi/agent", MaxConcurrentJobs: 6}
	if cfg.Daemon != want {
		t.Fatalf("daemon table = %+v, want %+v", cfg.Daemon, want)
	}
	if cfg.Server != "http://hub:8420" || cfg.Mapping.Empty() {
		t.Fatalf("the table disturbed the rest of the config: %+v", cfg)
	}
	// no table: the zero value
	if cfg, err := LoadConfig(writeTempConfig(t, base)); err != nil || cfg.Daemon != (DaemonSettings{}) {
		t.Fatalf("no table: %+v %v", cfg.Daemon, err)
	}

	for _, tc := range []struct{ name, body, key string }{
		{"duration unparsable", "step_timeout = \"soon\"", "step_timeout"},
		{"duration unquoted", "step_timeout = 45m", "step_timeout"},
		{"step timeout zero", "step_timeout = \"0s\"", "step_timeout"},
		{"step timeout negative", "step_timeout = \"-5m\"", "step_timeout"},
		{"stall zero", "stall_window = \"0\"", "stall_window"},
		{"harness root relative", "harness_root = \"rel/dir\"", "harness_root"},
		{"operator dir tilde", "operator_agent_dir = \"~/.pi/agent\"", "operator_agent_dir"},
		{"max quoted", "max_concurrent_jobs = \"6\"", "max_concurrent_jobs"},
		{"max zero", "max_concurrent_jobs = 0", "max_concurrent_jobs"},
		{"unknown key", "silence_window = \"5m\"", "silence_window"},
		{"flat key inside the table", "claude_bin = \"/x/claude\"", "claude_bin"},
		{"duplicate key", "max_concurrent_jobs = 2\nmax_concurrent_jobs = 3", "max_concurrent_jobs"},
	} {
		_, err := LoadConfig(writeTempConfig(t, base+"\n[daemon]\n"+tc.body+"\n"))
		var dk *DaemonKeyError
		if !errors.As(err, &dk) || dk.Key != tc.key || dk.Line == 0 {
			t.Fatalf("%s: err %v, want a DaemonKeyError for %q", tc.name, err, tc.key)
		}
		if !strings.Contains(err.Error(), "[daemon] "+tc.key) {
			t.Fatalf("%s: message %q does not name the key", tc.name, err)
		}
	}
	if _, err := LoadConfig(writeTempConfig(t, base+"\n[daemon]\nclaude_bin = \"/x\"\n")); err == nil || !strings.Contains(err.Error(), "move it above the [daemon] header") {
		t.Fatalf("a flat key under [daemon] needs the move-it-up hint: %v", err)
	}
	if _, err := LoadConfig(writeTempConfig(t, base+"\n[daemon]\n[daemon]\n")); err == nil || !strings.Contains(err.Error(), "duplicate [daemon]") {
		t.Fatalf("a second [daemon] table must refuse: %v", err)
	}
}
