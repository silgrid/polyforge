package daemon

// ensure_git_lowspeed_repro_test.go: `pf daemon ensure` is
// the standard start entry, and the daemon it launches gets exactly the
// filtered copy of the ensure caller's environment. An operator's
// explicit GIT_HTTP_LOW_SPEED_LIMIT / GIT_HTTP_LOW_SPEED_TIME must reach
// that daemon: runtime's taskGitEnv fills the 1000 bytes/sec / 60 s team
// defaults only when the variable is absent, so a value dropped here is
// silently replaced by the team default on every task-repo clone.
//
// Only those two non-credential names may pass; the rest of GIT_* stays
// filtered out of the daemon environment.

import (
	"strings"
	"testing"
)

func TestEnsureCarriesGitLowSpeedOverrides(t *testing.T) {
	t.Setenv("GIT_HTTP_LOW_SPEED_LIMIT", "204800")
	t.Setenv("GIT_HTTP_LOW_SPEED_TIME", "900")
	p := &ensurePlan{home: "/home/u"}
	for _, marker := range []bool{false, true} {
		env := p.daemonEnv(marker)
		joined := "\n" + strings.Join(env, "\n") + "\n"
		for _, want := range []string{
			"\nGIT_HTTP_LOW_SPEED_LIMIT=204800\n",
			"\nGIT_HTTP_LOW_SPEED_TIME=900\n",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("daemon env (marker=%v) lacks %q: %v", marker, strings.TrimSpace(want), env)
			}
		}
	}

	// the tmux loop re-execs through `env -i` with the same assignments
	script := p.loopScript()
	for _, want := range []string{
		"GIT_HTTP_LOW_SPEED_LIMIT='204800'",
		"GIT_HTTP_LOW_SPEED_TIME='900'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("the supervision loop script lacks %q:\n%s", want, script)
		}
	}

	// the detached (no-tmux) start uses the same filtered environment
	detached := "\n" + strings.Join(p.daemonEnv(false), "\n") + "\n"
	if !strings.Contains(detached, "\nGIT_HTTP_LOW_SPEED_LIMIT=204800\n") {
		t.Fatalf("the detached start drops the operator's override: %v", p.daemonEnv(false))
	}
}

func TestEnsureStillFiltersOtherGitEnv(t *testing.T) {
	for _, kv := range []string{
		"GIT_ASKPASS=/tmp/ask",
		"GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=no",
		"GIT_CONFIG_GLOBAL=/tmp/gitconfig",
		"GIT_DIR=/tmp/gitdir",
		"GIT_TRACE=1",
		"GIT_HTTP_LOW_SPEED_BYTES=1",
	} {
		name, val, _ := strings.Cut(kv, "=")
		t.Setenv(name, val)
	}
	p := &ensurePlan{home: "/home/u"}
	env := p.daemonEnv(false)
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, denied := range []string{
		"\nGIT_ASKPASS=",
		"\nGIT_SSH_COMMAND=",
		"\nGIT_CONFIG_GLOBAL=",
		"\nGIT_DIR=",
		"\nGIT_TRACE=",
		"\nGIT_HTTP_LOW_SPEED_BYTES=",
	} {
		if strings.Contains(joined, denied) {
			t.Fatalf("daemon env carries %q: %v", strings.TrimPrefix(denied, "\n"), env)
		}
	}
}
