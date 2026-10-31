package runtime

// w137_tool_window_env_test.go: reproduction (AC1/AC2) —
// every harness's step environment must carry the daemon's supervision
// tool window Y, so `pf verify-run` can keep its whole-run deadline under
// the daemon's rule 3 (one tool call longer than Y is judged hung and the
// step process tree is killed).
//
// A claude step gets it as BASH_MAX_TIMEOUT_MS = Y - 1m
// (claudeBashTimeouts). The pi step gets neither that variable nor any
// other value derived from Config.ToolWindow, and a pi verification step
// has no other way to learn Y: on 2026-10-29 the agent read no bound in
// its environment and passed 3480s / 55m / 72m / 78m against the default
// 30m window (the run then died at exactly Y with no result.json).
//
// Red on the pre-fix tree: with a 10- or 20-minute window the pi step
// environment still yields verify-run's 25-minute fallback deadline — a
// run that outlives the daemon's tool rule. `pf verify-run`'s own clamp
// behaviour is pinned in internal/polyforge/verifyrun/w137_deadline_clamp_test.go.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/verifyrun"
)

// TestW137PiStepEnvBoundsVerifyRun: for every tool window the daemon can
// be started with, the environment a pi step is spawned with lets `pf
// verify-run` derive a positive deadline strictly below that window.
func TestW137PiStepEnvBoundsVerifyRun(t *testing.T) {
	// the reported pi step carried no shell-call bound at all
	t.Setenv(envBashMaxTimeout, "")
	for _, window := range []time.Duration{
		10 * time.Minute,
		20 * time.Minute,
		30 * time.Minute, // the supervision default
		45 * time.Minute, // an operator's widened window
	} {
		cmd := newPiSpawner(&Config{ToolWindow: window}).buildCmd(SpawnParams{CapsuleDir: "/capsule", SpawnN: 1})
		got := verifyrun.DefaultDeadlineFor(cmd.Env)
		if got <= 0 || got >= window {
			t.Errorf("tool window %s: the pi step environment yields a %s verify-run deadline, want a positive one strictly under the window",
				window, got)
		}
	}
}
