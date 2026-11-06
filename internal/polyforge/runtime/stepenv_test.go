package runtime

// stepenv_test.go: the per-step tool window and the daemon-owned step
// environment (workflow contract v2 O1 / O-2). A step whose lane declares
// its own tool window runs under it — PF_TOOL_WINDOW_MS carries it on
// both harnesses, and a claude step's Bash default timeout equals the max
// (red before the change: the default stayed 10m under any window) —
// while a step that declares none keeps the daemon's global window and
// the 10m default exactly as before. Every step process also carries the
// daemon's step env (PF_PYTHON, and with command_path set a PATH of the
// daemon's pf directory, the harness binary's directory and the command
// path) and its round (PF_ROUND); a bare Config adds none of them.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

// The claude Bash default timeout equals the max when the step declares
// its tool window, and stays 10m (clamped to the max) when it does not.
func TestClaudeBashDefaultEqualsMaxForDeclaredToolWindow(t *testing.T) {
	clearBashTimeoutEnv(t)
	for _, tc := range []struct {
		name     string
		global   time.Duration
		declared time.Duration
		def, max string
	}{
		{"none declared, global default", 0, 0, "600000", "1740000"},
		{"none declared, global 60m", 60 * time.Minute, 0, "600000", "3540000"},
		{"declared 120m under the default global", 0, 120 * time.Minute, "7140000", "7140000"},
		{"declared 120m under a 60m global", 60 * time.Minute, 120 * time.Minute, "7140000", "7140000"},
		{"declared 30m: the max, not 10m", 0, 30 * time.Minute, "1740000", "1740000"},
		{"declared 5m: below 10m either way", 0, 5 * time.Minute, "240000", "240000"},
	} {
		cfg := &Config{ToolWindow: tc.global}
		cmd, err := (&claudeSpawner{cfg: cfg}).buildCmd(SpawnParams{CapsuleDir: "/c", HarnessSession: testSession, ToolWindow: tc.declared})
		if err != nil {
			t.Fatal(err)
		}
		if got := envEntries(cmd.Env, envBashDefaultTimeout); !slices.Equal(got, []string{tc.def}) {
			t.Errorf("%s: %s %q, want [%s]", tc.name, envBashDefaultTimeout, got, tc.def)
		}
		if got := envEntries(cmd.Env, envBashMaxTimeout); !slices.Equal(got, []string{tc.max}) {
			t.Errorf("%s: %s %q, want [%s]", tc.name, envBashMaxTimeout, got, tc.max)
		}
	}
}

// PF_TOOL_WINDOW_MS is the step's own window on both harnesses when one
// is declared, the daemon's global one otherwise.
func TestToolWindowEnvFollowsDeclaredWindow(t *testing.T) {
	clearBashTimeoutEnv(t)
	cfg := &Config{ToolWindow: 20 * time.Minute}
	for _, tc := range []struct {
		declared time.Duration
		want     string
	}{
		{0, "1200000"},
		{90 * time.Minute, "5400000"},
	} {
		pi := newPiSpawner(cfg).buildCmd(SpawnParams{CapsuleDir: "/c", SpawnN: 1, ToolWindow: tc.declared})
		if got := envEntries(pi.Env, spawn.EnvToolWindow); !slices.Equal(got, []string{tc.want}) {
			t.Errorf("pi, declared %v: %s %q, want [%s]", tc.declared, spawn.EnvToolWindow, got, tc.want)
		}
		cl, err := (&claudeSpawner{cfg: cfg}).buildCmd(SpawnParams{CapsuleDir: "/c", HarnessSession: testSession, ToolWindow: tc.declared})
		if err != nil {
			t.Fatal(err)
		}
		if got := envEntries(cl.Env, spawn.EnvToolWindow); !slices.Equal(got, []string{tc.want}) {
			t.Errorf("claude, declared %v: %s %q, want [%s]", tc.declared, spawn.EnvToolWindow, got, tc.want)
		}
	}
}

// Every step process carries the daemon's step env, the places its
// scripts run from and its round, each name exactly once, on both
// harnesses; an inherited copy never leaks in — the composed step PATH
// replaces the inherited PATH ( added PF_PYTHON, PF_CAPSULE,
// PF_LANE_DIR and PF_SHARED_DIR).
func TestStepEnvInjectedOnBothHarnesses(t *testing.T) {
	clearBashTimeoutEnv(t)
	t.Setenv("PATH", "/inherited/bin")
	t.Setenv(spawn.EnvRound, "99")
	for _, k := range []string{spawn.EnvPython, spawn.EnvCapsule, spawn.EnvLaneDir, spawn.EnvSharedDir} {
		t.Setenv(k, "/inherited/"+k)
	}
	cfg := &Config{StepEnv: func() StepEnv {
		return StepEnv{CommandPath: "/opt/pf/bin:/usr/bin", PfDir: "/srv/pf", Python: "/usr/bin/python3.12"}
	}}
	// neither harness binary resolves on the inherited PATH: no harness
	// directory goes in
	want := map[string]string{
		"PATH":             "/srv/pf:/opt/pf/bin:/usr/bin",
		spawn.EnvRound:     "3",
		spawn.EnvPython:    "/usr/bin/python3.12",
		spawn.EnvCapsule:   "/c",
		spawn.EnvLaneDir:   "/c/workflows/develop",
		spawn.EnvSharedDir: "/c/workflows/_shared",
	}
	lane := "/c/workflows/develop"
	pi := newPiSpawner(cfg).buildCmd(SpawnParams{CapsuleDir: "/c", LaneDir: lane, SpawnN: 1, Round: 3})
	cl, err := (&claudeSpawner{cfg: cfg}).buildCmd(SpawnParams{CapsuleDir: "/c", LaneDir: lane, HarnessSession: testSession, Round: 3})
	if err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][]string{"pi": pi.Env, "claude": cl.Env} {
		for k, v := range want {
			if got := envEntries(env, k); !slices.Equal(got, []string{v}) {
				t.Errorf("%s: %s entries %q, want exactly [%s]", name, k, got, v)
			}
		}
	}
}

// Negative control: a bare Config (no step env source) and a derivation
// of no stated round and no named lane add none of these names (the
// capsule root is the derivation's own: PF_CAPSULE stays) and keep the
// inherited PATH.
func TestStepEnvAbsentWithoutSource(t *testing.T) {
	clearBashTimeoutEnv(t)
	t.Setenv("PATH", "/inherited/bin")
	cfg := &Config{}
	pi := newPiSpawner(cfg).buildCmd(SpawnParams{CapsuleDir: "/c", SpawnN: 1})
	cl, err := (&claudeSpawner{cfg: cfg}).buildCmd(SpawnParams{CapsuleDir: "/c", HarnessSession: testSession})
	if err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][]string{"pi": pi.Env, "claude": cl.Env} {
		if got := envEntries(env, spawn.EnvCapsule); !slices.Equal(got, []string{"/c"}) {
			t.Errorf("%s: %s %q, want [/c]", name, spawn.EnvCapsule, got)
		}
		if got := envEntries(env, "PATH"); !slices.Equal(got, []string{"/inherited/bin"}) {
			t.Errorf("%s: PATH %q, want the inherited [/inherited/bin]", name, got)
		}
		for _, k := range []string{"PF_COMMAND_PATH", "PF_VERIFY_DAEMON", "PF_VERIFY_BOOT", spawn.EnvRound,
			spawn.EnvPython, spawn.EnvLaneDir, spawn.EnvSharedDir} {
			if got := envEntries(env, k); got != nil {
				t.Errorf("%s: %s %q present with no source", name, k, got)
			}
		}
	}
}

// TestStepPathKeepsPfAndTheHarness: a command_path that holds neither pf
// nor the harness's interpreter (config.md's own example) costs a step
// neither — its PATH is the daemon's pf directory, the harness binary's
// directory (resolved on the daemon's PATH, where an nvm install keeps its
// node) and the command path, each entry once — and an absolute harness
// binary brings its own directory.
func TestStepPathKeepsPfAndTheHarness(t *testing.T) {
	clearBashTimeoutEnv(t)
	stub := func(dir, name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pfDir, nvm, other := t.TempDir(), t.TempDir(), t.TempDir()
	stub(pfDir, "pf")
	for _, n := range []string{"node", "pi", "claude"} {
		stub(nvm, n)
	}
	stub(other, "pi")
	t.Setenv("PATH", nvm+":/usr/bin:/bin") // the daemon's own PATH
	const commandPath = "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
	cfg := &Config{StepEnv: func() StepEnv { return StepEnv{CommandPath: commandPath, PfDir: pfDir} }}
	pi := newPiSpawner(cfg).buildCmd(SpawnParams{CapsuleDir: "/c", SpawnN: 1})
	cl, err := (&claudeSpawner{cfg: cfg}).buildCmd(SpawnParams{CapsuleDir: "/c", HarnessSession: testSession})
	if err != nil {
		t.Fatal(err)
	}
	want := pfDir + ":" + nvm + ":" + commandPath
	for name, env := range map[string][]string{"pi": pi.Env, "claude": cl.Env} {
		got := envEntries(env, "PATH")
		if !slices.Equal(got, []string{want}) {
			t.Fatalf("%s: PATH %q, want exactly [%s]", name, got, want)
		}
		for tool, dir := range map[string]string{"pf": pfDir, "node": nvm} {
			if p := lookIn(tool, got[0]); p != filepath.Join(dir, tool) {
				t.Errorf("%s: %s resolves to %q on the step PATH, want %s's", name, tool, p, dir)
			}
		}
	}
	// an absolute harness binary: its own directory, whatever the PATH
	cfg.PiBin = filepath.Join(other, "pi")
	pi = newPiSpawner(cfg).buildCmd(SpawnParams{CapsuleDir: "/c", SpawnN: 1})
	if got := envEntries(pi.Env, "PATH"); !slices.Equal(got, []string{pfDir + ":" + other + ":" + commandPath}) {
		t.Fatalf("absolute pi: PATH %q", got)
	}
	// each entry once, order kept
	if got := JoinPath(pfDir, pfDir+":/usr/bin", "/usr/bin::/bin"); got != pfDir+":/usr/bin:/bin" {
		t.Fatalf("JoinPath = %q", got)
	}
}

// lookIn answers where name resolves on the PATH value path ("" = nowhere).
func lookIn(name, path string) string {
	for _, d := range filepath.SplitList(path) {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}
