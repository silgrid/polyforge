package runtime

// spawn_equiv_test.go: the started exec.Cmd of both step harnesses — the
// argv, cwd, env names, process-group attribute and pipe wiring a Spawn
// actually starts — pinned per harness, so the shared start path behind
// piSpawner.Spawn and claudeSpawner.Spawn cannot drift from what each
// harness's builder produces. No real pi/claude runs: the binary is a
// shell stand-in that drains stdin.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
)

func envNames(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		out = append(out, name)
	}
	return out
}

func TestSpawnStartedCmdMatchesBuilder(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "stand-in")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{PiBin: bin, ClaudeBin: bin, MachineID: "m-eq"}
	base := SpawnParams{
		WorkItemID: "wi-eq", CapsuleDir: dir, InitialPrompt: "p",
		Model: "x/y", HarnessSession: testSession,
		OnRecord: func(string) {},
	}
	piP := base
	piP.Harness = harness.Pi
	clP := base
	clP.Harness = harness.Claude
	clWant, err := (&claudeSpawner{cfg: cfg}).buildCmd(clP)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		p         SpawnParams
		want      []string // argv after argv[0]
		wantEnv   []string
		procStdin bool // the handle keeps stdin (pi RPC) or closed it (claude)
	}{
		{"pi", piP, []string{"--mode", "rpc", "--no-skills", "--no-context-files",
			"--no-prompt-templates", "--model", "x/y"},
			newPiSpawner(cfg).buildCmd(piP).Env, true},
		{"claude", clP, clWant.Args[1:], clWant.Env, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := DefaultSpawner(cfg).Spawn(context.Background(), tc.p)
			if err != nil {
				t.Fatal(err)
			}
			pr := h.(*proc)
			defer func() {
				_ = pr.Terminate(time.Second)
				<-pr.exited
			}()
			c := pr.cmd
			if c.Path != bin || c.Args[0] != bin || !slices.Equal(c.Args[1:], tc.want) {
				t.Fatalf("argv %q", c.Args)
			}
			if c.Dir != dir {
				t.Fatalf("dir %q", c.Dir)
			}
			if !slices.Equal(envNames(c.Env), envNames(tc.wantEnv)) {
				t.Fatalf("env names drifted:\n got %q\nwant %q",
					envNames(c.Env), envNames(tc.wantEnv))
			}
			if !slices.Equal(c.Env, tc.wantEnv) {
				t.Fatal("env values drifted from the builder")
			}
			if c.SysProcAttr == nil || !c.SysProcAttr.Setpgid {
				t.Fatal("not its own process group")
			}
			if _, ok := c.Stdout.(*os.File); !ok {
				t.Fatalf("stdout is %T, want an own *os.File pipe", c.Stdout)
			}
			if _, ok := c.Stderr.(*os.File); !ok {
				t.Fatalf("stderr is %T, want an own *os.File pipe", c.Stderr)
			}
			if c.Stdin == nil {
				t.Fatal("no stdin pipe")
			}
			if (pr.stdin != nil) != tc.procStdin {
				t.Fatalf("handle stdin kept = %v, want %v", pr.stdin != nil, tc.procStdin)
			}
			if len(pr.taps) != 1 {
				t.Fatalf("taps = %d, want the OnRecord tap", len(pr.taps))
			}
		})
	}
}

// standIn writes an executable shell stand-in into dir and returns it.
func standIn(t *testing.T, dir, script string) string {
	t.Helper()
	bin := filepath.Join(dir, "stand-in")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// spawnedEnv spawns p through DefaultSpawner and returns the started env.
func spawnedEnv(t *testing.T, cfg *Config, p SpawnParams) []string {
	t.Helper()
	h, err := DefaultSpawner(cfg).Spawn(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	pr := h.(*proc)
	t.Cleanup(func() {
		_ = pr.Terminate(time.Second)
		<-pr.exited
	})
	return slices.Clone(pr.cmd.Env)
}

// The started env obeys the env-hygiene rules on its own terms — not just
// "equals buildCmd": a claude step inherits no ANTHROPIC_* / CLAUDECODE /
// CLAUDE_CODE_* / CLAUDE_CONFIG_DIR (only the adapter's own switches and
// config dir), a pi step keeps them; both lose POLYFORGE_* / PF_* except
// the adapter's markers. Only names are reported, never values.
func TestSpawnStartedEnvHygiene(t *testing.T) {
	inherited := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "junk-eq", "ANTHROPIC_BASE_URL": "junk-eq",
		"CLAUDECODE": "junk-eq", "CLAUDE_CODE_ENTRYPOINT": "junk-eq",
		"CLAUDE_CONFIG_DIR": "junk-eq", "POLYFORGE_EQ_KEY": "junk-eq",
		"PF_CHAT_ID": "junk-eq", "EQ_KEEP_ME": "kept-eq",
	}
	for k, v := range inherited {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	bin := standIn(t, dir, "cat >/dev/null\n")
	cfg := &Config{PiBin: bin, ClaudeBin: bin, MachineID: "m-eq", HarnessRoot: t.TempDir()}
	p := SpawnParams{
		WorkItemID: "wi-eq", CapsuleDir: dir, InitialPrompt: "p", HarnessSession: testSession,
	}
	ownPF := []string{"PF_WI_ID", "PF_DAEMON_ID", "PF_TOOL_WINDOW_MS"}
	ownClaude := []string{
		"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS", "CLAUDE_CODE_STARTUP_FAILURE_RESULTS",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "CLAUDE_CONFIG_DIR",
	}
	check := func(t *testing.T, env []string, claude bool) {
		t.Helper()
		seen := map[string]int{}
		for _, kv := range env {
			name, val, _ := strings.Cut(kv, "=")
			seen[name]++
			if strings.HasPrefix(name, "POLYFORGE_") ||
				(strings.HasPrefix(name, "PF_") && !slices.Contains(ownPF, name)) {
				t.Errorf("%s reached the step", name)
			}
			if !claude {
				continue
			}
			if strings.HasPrefix(name, "ANTHROPIC_") || name == "CLAUDECODE" {
				t.Errorf("inherited %s reached the claude step", name)
			}
			if (strings.HasPrefix(name, "CLAUDE_CODE_") || name == "CLAUDE_CONFIG_DIR") &&
				(!slices.Contains(ownClaude, name) || val == "junk-eq") {
				t.Errorf("inherited %s reached the claude step", name)
			}
		}
		for name, n := range seen {
			if n > 1 {
				t.Errorf("%s set %d times", name, n)
			}
		}
		if seen["EQ_KEEP_ME"] != 1 {
			t.Error("an ordinary inherited variable was dropped")
		}
		for _, k := range ownPF {
			if seen[k] != 1 {
				t.Errorf("adapter marker %s missing", k)
			}
		}
		if claude {
			for _, k := range ownClaude {
				if seen[k] != 1 {
					t.Errorf("adapter variable %s missing", k)
				}
			}
			return
		}
		for _, k := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDECODE",
			"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CONFIG_DIR"} {
			if !slices.Contains(env, k+"="+inherited[k]) {
				t.Errorf("the pi step lost the inherited %s", k)
			}
		}
	}
	t.Run("pi", func(t *testing.T) {
		pp := p
		pp.Harness = harness.Pi
		check(t, spawnedEnv(t, cfg, pp), false)
	})
	t.Run("claude", func(t *testing.T) {
		cp := p
		cp.Harness = harness.Claude
		check(t, spawnedEnv(t, cfg, cp), true)
	})
}

// waitGone polls until pid no longer exists (reaped by the proc's Wait).
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d still alive after the teardown", pid)
}

// A failing spawn journal tears the fresh process down for both harnesses.
// claude closes its stdin first: the stand-in ignores SIGTERM (the journal
// fails only once that trap is set) and writes its marker on stdin EOF, so
// the marker proves the close came before the group kill (without it cat
// would block until the SIGKILL and the marker would never be written).
func TestSpawnJournalFailureTearsDown(t *testing.T) {
	for _, h := range []string{harness.Pi, harness.Claude} {
		t.Run(h, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "eof")
			ready := filepath.Join(dir, "ready")
			bin := standIn(t, dir,
				"trap '' TERM\n: > "+ready+"\ncat >/dev/null\necho eof > "+marker+"\n")
			if h == harness.Pi {
				bin = standIn(t, dir, ": > "+ready+"\ncat >/dev/null\n")
			}
			var pid int
			journal := func(p int, _ string) error {
				pid = p
				// fail only once the stand-in's trap is in place
				for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
					if _, err := os.Stat(ready); err == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				return errors.New("journal down")
			}
			sp := DefaultSpawner(&Config{PiBin: bin, ClaudeBin: bin})
			_, err := sp.Spawn(context.Background(),
				SpawnParams{
					CapsuleDir: dir, InitialPrompt: "p", Harness: h, HarnessSession: testSession,
					Journal: journal,
				})
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			if !strings.Contains(msg, "spawn journal failed (process torn down)") ||
				!strings.Contains(msg, "journal down") {
				t.Fatalf("err = %v", err)
			}
			if pid <= 0 {
				t.Fatal("journal never called with a pid")
			}
			waitGone(t, pid)
			if h == harness.Claude {
				if _, err := os.Stat(marker); err != nil {
					t.Fatal("claude stdin was not closed before the teardown")
				}
			}
		})
	}
}

// A harness that dies at startup never reads the prompt: the prompt write
// fails (the prompt outgrows the pipe buffer, so the write blocks until
// the reader is gone and then EPIPEs — no timing race) and the spawn error
// carries the stand-in's stderr for the driver's classification.
func TestSpawnPromptFailureCarriesStderr(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	for _, h := range []string{harness.Pi, harness.Claude} {
		t.Run(h, func(t *testing.T) {
			dir := t.TempDir()
			bin := standIn(t, dir, "exec 0<&-\necho stand-in-died-at-startup >&2\nexit 3\n")
			var pid int
			sp := DefaultSpawner(&Config{PiBin: bin, ClaudeBin: bin})
			_, err := sp.Spawn(context.Background(),
				SpawnParams{
					CapsuleDir: dir, InitialPrompt: big, Harness: h, HarnessSession: testSession,
					Journal: func(p int, _ string) error { pid = p; return nil },
				})
			if err == nil {
				t.Fatal("spawn succeeded against a dead harness")
			}
			msg := err.Error()
			if !strings.Contains(msg, "initial prompt injection failed (process torn down)") ||
				!strings.Contains(msg, "harness stderr: stand-in-died-at-startup") {
				t.Fatalf("err = %v", err)
			}
			waitGone(t, pid)
		})
	}
}
