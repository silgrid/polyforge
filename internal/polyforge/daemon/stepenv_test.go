package daemon

// stepenv_test.go: the daemon's step env (stepenv.go stepEnv) is the one
// source of the step processes' PATH and of the daemon's own in-process
// command PATH. With command_path set, the daemon's in-process git and gh
// run under it and a step under it behind the daemon's own pf directory
// (and its harness's, runtime.StepEnv.StepPath); unset, both keep the
// daemon process's own PATH — a step adds no PATH of its own. A reload of
// command_path moves both at once. The step env is also the source of the
// step's Cell facts (polyforge#75): every step process carries its Cell's
// name, its server and the deploy configuration file that Cell's deploys
// read — PF_CELL, PF_CELL_SERVER, PF_DEPLOY_ENV.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

// envPath answers the last PATH value of env.
func envPath(env []string) string {
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	return path
}

func TestStepEnvIsTheCommandPathSource(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	td := newTestDaemon(t, func(c *Config) {
		c.StartupFile.Daemon.CommandPath = "/opt/pf/bin:/usr/bin"
		c.Python = "/usr/bin/python3.12"
	})
	d := td.Daemon
	env := d.stepEnv()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	if env.CommandPath != "/opt/pf/bin:/usr/bin" || env.PfDir != filepath.Dir(exe) || env.PfDir == "" {
		t.Fatalf("the step env is the live command path and this daemon's own executable's directory: %+v", env)
	}
	if env.Python != "/usr/bin/python3.12" {
		t.Fatalf("the step env carries the interpreter Main resolved (Config.Python): %+v", env)
	}
	if got, want := env.Vars(), []string{spawn.EnvPython + "=/usr/bin/python3.12",
		"PF_CELL=default", "PF_CELL_SERVER=", "PF_DEPLOY_ENV=" + filepath.Join(xdg, "polyforge", "deploy.env")}; !slices.Equal(got, want) {
		t.Fatalf("step env vars %q, want %q", got, want)
	}
	// a step: its pf directory first, then the command path
	if got, want := d.stepPathNow(), env.PfDir+":/opt/pf/bin:/usr/bin"; got != want {
		t.Fatalf("the step PATH %q, want %q", got, want)
	}
	// its in-process commands run under the command path itself
	if got := envPath(d.commandEnv()); got != "/opt/pf/bin:/usr/bin" {
		t.Fatalf("the command env PATH %q is not the command path", got)
	}
	// a reload of command_path moves the step env with it (read live)
	d.reloadMu.Lock()
	d.commandPath = "/moved/bin"
	d.reloadMu.Unlock()
	if got := d.stepEnv().CommandPath; got != "/moved/bin" {
		t.Fatalf("the step env reads the live command path: %q", got)
	}
	if got := envPath(d.commandEnv()); got != "/moved/bin" {
		t.Fatalf("the command env reads the live command path: %q", got)
	}
}

// TestCommandPathUnsetIsTheDaemonsOwnPATH: with no command_path the
// daemon's in-process commands run under the daemon process's own PATH
// (commandPathNow's fallback), and a step adds no PATH: it keeps the one it
// inherits, the same.
func TestCommandPathUnsetIsTheDaemonsOwnPATH(t *testing.T) {
	td := newTestDaemon(t, nil)
	d := td.Daemon
	t.Setenv("PATH", "/daemon/own/bin:"+os.Getenv("PATH"))
	if got := d.commandPathNow(); got != os.Getenv("PATH") {
		t.Fatalf("commandPathNow %q, want the daemon's own PATH %q", got, os.Getenv("PATH"))
	}
	if got := envPath(d.commandEnv()); got != os.Getenv("PATH") {
		t.Fatalf("the command env PATH %q, want the daemon's own", got)
	}
	if env := d.stepEnv(); env.CommandPath != "" || env.StepPath("pi") != "" {
		t.Fatalf("unset: the step env sets no PATH (the step inherits): %+v / %q", env, env.StepPath("pi"))
	}
	if got := d.stepPathNow(); got != os.Getenv("PATH") {
		t.Fatalf("unset: the reported step PATH %q, want the daemon's own", got)
	}
}

// TestStepEnvCarriesCellFacts (polyforge#75): every step process carries
// the facts of the Cell its work item belongs to — its name (PF_CELL,
// "default" for an unnamed Cell), its AIHub server as configured
// (PF_CELL_SERVER) and the deploy configuration file that Cell's deploys
// read (PF_DEPLOY_ENV, an absolute path in the Host configuration
// directory: deploy.env for the default Cell, deploy-<profile>.env for a
// named one, the Cell file naming). The deploy configuration's location
// follows the Cell's own config file; a daemon that states no config path
// falls back to the documented default (XDG-aware).
func TestStepEnvCarriesCellFacts(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name     string
		profile  string
		server   string
		cfgFile  string
		wantCell string
		wantEnv  string
	}{
		{"named cell", "public", "http://hub.example:8080", filepath.Join(dir, "cli-public.toml"), "public", filepath.Join(dir, "deploy-public.env")},
		{"default cell", "", "http://hub.example:8080", filepath.Join(dir, "cli.toml"), "default", filepath.Join(dir, "deploy.env")},
		{"no config path: the documented default", "", "http://hub.example:8080", "", "default", filepath.Join(dir, "polyforge", "deploy.env")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", dir)
			td := newTestDaemon(t, func(c *Config) {
				c.Profile = tc.profile
				c.Server = tc.server
				c.ConfigPath = tc.cfgFile
			})
			env := td.Daemon.stepEnv()
			if env.Cell != tc.wantCell || env.CellServer != tc.server || env.DeployEnv != tc.wantEnv {
				t.Fatalf("step env %+v, want Cell %q, CellServer %q, DeployEnv %q", env, tc.wantCell, tc.server, tc.wantEnv)
			}
			for _, kv := range []string{"PF_CELL=" + tc.wantCell, "PF_CELL_SERVER=" + tc.server, "PF_DEPLOY_ENV=" + tc.wantEnv} {
				if !slices.Contains(env.Vars(), kv) {
					t.Fatalf("step env vars %q lack %q", env.Vars(), kv)
				}
			}
		})
	}
}
