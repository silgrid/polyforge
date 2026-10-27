package runtime

// task_git_env_lowspeed_repro_test.go (workbench#2): taskGitEnv's contract
// is "team defaults only when the operator did not set the variable".
// Every task-repo git call (clone, fetch, restore) runs with it, so an
// explicit GIT_HTTP_LOW_SPEED_LIMIT / GIT_HTTP_LOW_SPEED_TIME that reached
// the daemon must survive verbatim; with neither set the team defaults
// 1000 bytes/sec and 60 s must still be supplied.

import (
	"os"
	"slices"
	"testing"
)

// unsetenv removes name for this test and restores it afterwards
// (t.Setenv("", ...) would set an empty value, which taskGitEnv treats as
// an operator setting).
func unsetenv(t *testing.T, name string) {
	t.Helper()
	old, ok := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(name, old)
			return
		}
		_ = os.Unsetenv(name)
	})
}

func TestTaskGitEnvKeepsGitLowSpeedDefaults(t *testing.T) {
	unsetenv(t, "GIT_HTTP_LOW_SPEED_LIMIT")
	unsetenv(t, "GIT_HTTP_LOW_SPEED_TIME")
	env := taskGitEnv()
	for _, want := range []string{"GIT_HTTP_LOW_SPEED_LIMIT=1000", "GIT_HTTP_LOW_SPEED_TIME=60", "GIT_TERMINAL_PROMPT=0"} {
		if !slices.Contains(env, want) {
			t.Fatalf("taskGitEnv lacks the team default %q", want)
		}
	}
}

func TestTaskGitEnvKeepsGitLowSpeedOverrides(t *testing.T) {
	t.Setenv("GIT_HTTP_LOW_SPEED_LIMIT", "204800")
	t.Setenv("GIT_HTTP_LOW_SPEED_TIME", "900")
	env := taskGitEnv()
	for _, want := range []string{"GIT_HTTP_LOW_SPEED_LIMIT=204800", "GIT_HTTP_LOW_SPEED_TIME=900"} {
		if !slices.Contains(env, want) {
			t.Fatalf("taskGitEnv dropped the operator's %q", want)
		}
	}
	for _, unwanted := range []string{"GIT_HTTP_LOW_SPEED_LIMIT=1000", "GIT_HTTP_LOW_SPEED_TIME=60"} {
		if slices.Contains(env, unwanted) {
			t.Fatalf("taskGitEnv appended the team default %q next to the operator's value", unwanted)
		}
	}
}
