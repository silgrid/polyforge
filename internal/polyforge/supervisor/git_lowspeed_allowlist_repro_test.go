package supervisor

// git_lowspeed_allowlist_repro_test.go (workbench#2): `pf daemon ensure`
// starts the daemon from a filtered copy of the ensure caller's
// environment (DaemonEnv). An operator's explicit
// GIT_HTTP_LOW_SPEED_LIMIT / GIT_HTTP_LOW_SPEED_TIME override must survive
// that filter: runtime's taskGitEnv fills the 1000 bytes/sec / 60 s team
// defaults only when the variable is ABSENT, so a filter that drops them
// silently forces the team default onto every task-repo clone.
//
// Opening the whole GIT_* family is not the fix: only these two
// non-credential names may pass; credential and config knobs
// (GIT_ASKPASS, GIT_SSH_COMMAND, GIT_CONFIG_*, GIT_DIR, ...) stay out.

import (
	"slices"
	"testing"
)

func TestDaemonEnvKeepsGitLowSpeedOverrides(t *testing.T) {
	in := []string{
		"PATH=/bin",
		"GIT_HTTP_LOW_SPEED_LIMIT=204800",
		"GIT_HTTP_LOW_SPEED_TIME=900",
	}
	got := DaemonEnv(in)
	if !slices.Contains(got, "GIT_HTTP_LOW_SPEED_LIMIT=204800") {
		t.Fatalf("DaemonEnv dropped the operator's GIT_HTTP_LOW_SPEED_LIMIT: %v", got)
	}
	if !slices.Contains(got, "GIT_HTTP_LOW_SPEED_TIME=900") {
		t.Fatalf("DaemonEnv dropped the operator's GIT_HTTP_LOW_SPEED_TIME: %v", got)
	}
	for _, name := range []string{"GIT_HTTP_LOW_SPEED_LIMIT", "GIT_HTTP_LOW_SPEED_TIME"} {
		if !DaemonEnvAllowed(name) {
			t.Fatalf("%s is not allowed through the daemon env filter", name)
		}
	}
}

func TestDaemonEnvOpensOnlyTheTwoGitLowSpeedNames(t *testing.T) {
	for _, name := range []string{
		"GIT_HTTP_LOW_SPEED_BYTES", // not one of the two names: no prefix opening
		"GIT_HTTP_LOW_SPEED",       // nor the bare stem
		"GIT_ASKPASS",
		"GIT_SSH_COMMAND",
		"GIT_CONFIG_GLOBAL",
		"GIT_CONFIG_NOSYSTEM",
		"GIT_DIR",
		"GIT_WORK_TREE",
		"GIT_TRACE",
		"GIT_TERMINAL_PROMPT",
	} {
		if DaemonEnvAllowed(name) {
			t.Fatalf("the daemon env filter opened %s", name)
		}
	}
	if got := DaemonEnv([]string{"GIT_ASKPASS=/tmp/ask", "GIT_TRACE=1", "GIT_CONFIG_GLOBAL=/tmp/cfg"}); len(got) != 0 {
		t.Fatalf("DaemonEnv carried non-allowed GIT_* variables: %v", got)
	}
}
