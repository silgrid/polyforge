package runtime

// w137_tool_window_env_test.go: reproduction (AC1/AC2) —
// every harness's step environment must carry the daemon's supervision
// tool window Y, so a step script can keep its whole-run deadline under
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
// environment still yields the scripts' 25-minute fallback deadline — a
// run that outlives the daemon's tool rule. The scripts' own rule is
// pinned in workflows/_shared/scripts/tests/test_deadline.py.

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

// scriptDeadlineFor is the deadline a step script derives from its
// environment (workflows/_shared/scripts/pfstep/deadline.py): the tightest
// of BASH_MAX_TIMEOUT_MS and PF_TOOL_WINDOW_MS less two minutes, or less
// half the bound when that is smaller; 25 minutes without either.
func scriptDeadlineFor(env []string) time.Duration {
	bound := time.Duration(0)
	for _, name := range []string{envBashMaxTimeout, spawn.EnvToolWindow} {
		raw, found := "", false
		for i := len(env) - 1; i >= 0; i-- {
			if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
				raw, found = v, true
				break
			}
		}
		ms, err := strconv.Atoi(strings.TrimSpace(raw))
		if !found || err != nil || ms <= 0 {
			continue
		}
		if d := time.Duration(ms) * time.Millisecond; bound == 0 || d < bound {
			bound = d
		}
	}
	if bound <= 0 {
		return 25 * time.Minute
	}
	if d := bound - min(2*time.Minute, bound/2); d > 0 {
		return d
	}
	return time.Millisecond
}

// TestW137PiStepEnvBoundsVerifyRun: for every tool window the daemon can
// be started with, the environment a pi step is spawned with lets a step
// script derive a positive deadline strictly below that window.
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
		got := scriptDeadlineFor(cmd.Env)
		if got <= 0 || got >= window {
			t.Errorf("tool window %s: the pi step environment yields a %s script deadline, want a positive one strictly under the window",
				window, got)
		}
	}
}
