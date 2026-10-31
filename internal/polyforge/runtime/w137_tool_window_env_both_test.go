package runtime

// w137_tool_window_env_both_test.go: AC1 — the tool window
// must reach every harness, not only claude's BASH_MAX_TIMEOUT_MS. The pi
// half is pinned in w137_tool_window_env_test.go; this one keeps the
// harness-neutral binding on both spawners and checks that verify-run can
// derive a deadline under the same window for each.

import (
	"strconv"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/verifyrun"
)

func TestW137BothHarnessesCarryToolWindow(t *testing.T) {
	for _, window := range []time.Duration{10 * time.Minute, 30 * time.Minute, 45 * time.Minute} {
		cfg := &Config{ToolWindow: window}
		claude, err := (&claudeSpawner{cfg: cfg}).buildCmd(SpawnParams{CapsuleDir: "/capsule", HarnessSession: testSession})
		if err != nil {
			t.Fatal(err)
		}
		envs := map[string][]string{
			"pi":     newPiSpawner(cfg).buildCmd(SpawnParams{CapsuleDir: "/capsule", SpawnN: 1}).Env,
			"claude": claude.Env,
		}
		for name, env := range envs {
			want := strconv.FormatInt(window.Milliseconds(), 10)
			if got := envEntries(env, spawn.EnvToolWindow); len(got) != 1 || got[0] != want {
				t.Errorf("%s step, window %s: %s = %q, want [%s]", name, window, spawn.EnvToolWindow, got, want)
			}
			if d := verifyrun.DefaultDeadlineFor(env); d <= 0 || d >= window {
				t.Errorf("%s step, window %s: verify-run derives %s, want a positive deadline under the window", name, window, d)
			}
		}
	}
}
