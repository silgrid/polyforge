// carries: internal/polyforge/cli/verbs_task.go

package cli

// w72_task_show_attempts_test.go (polyforge#72, u12 — AC9): the human
// `pf task show` names every candidate the stuck detection ended, with its
// cause and evidence — the judgement is visible, not only in --json.

import (
	"net/http"
	"strings"
	"testing"
)

func TestW72TaskShowRendersStuckAttempts(t *testing.T) {
	sock := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"task_00000000000000a1","spec":{"project":"p"},"state":"completed","candidate":"pi:m/second",` +
			`"attempts":[{"candidate":"pi:m/first","cause":"harness_silent","evidence":"no output for 10m0s"}],"report":"the draft"}`))
	}))
	env := map[string]string{"PF_DAEMON_SOCKET": sock}
	_, out, _ := runPf(t, env, "task", "show", "task_00000000000000a1")
	if !strings.Contains(out, "stuck: pi:m/first (harness_silent): no output for 10m0s") {
		t.Fatalf("show must render the ended candidate, its cause and evidence:\n%s", out)
	}
	if !strings.Contains(out, "candidate: pi:m/second") || !strings.Contains(out, "the draft") {
		t.Fatalf("show keeps its other lines:\n%s", out)
	}
}
