package cli

// submit_output_test.go: the submit human face — the
// acceptance line plus the placement line (the claim's target machine +
// capsule landing path) and the missing-clone warning line, driven
// through the CLI's own render path (Run) against a fake aihub serving
// the canned queued work item and a daemon fixture answering the claim.

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// submitHumanRun runs `pf submit wi_abc` with the daemon's claim answer
// scripted to claimAnswer; the aihub precheck reads the canned queued
// work item.
func submitHumanRun(t *testing.T, claimAnswer string) (string, string, int) {
	t.Helper()
	var rec reqRecorder
	aihub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/claim", claimAnswer)
	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock,
	}, "submit", "wi_abc")
	return stdout, stderr, code
}

// TestSubmitHumanOutputStatesPlacement: a resolved placement prints the
// machine id and the capsule landing path beside the acceptance line.
func TestSubmitHumanOutputStatesPlacement(t *testing.T) {
	stdout, stderr, code := submitHumanRun(t,
		`{"accepted":true,"work_item_id":"wi_abc","machine_id":"m-place","capsule_path":"/ws/capsules/pf-42"}`)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "submitted pf-42 (accepted by the local daemon)") {
		t.Errorf("today's acceptance line must stay: %q", stdout)
	}
	if !strings.Contains(stdout, "m-place") {
		t.Errorf("the placement line must state the target machine: %q", stdout)
	}
	if !strings.Contains(stdout, "/ws/capsules/pf-42") {
		t.Errorf("the placement line must state the capsule landing path: %q", stdout)
	}
}

// TestSubmitHumanOutputWarnsMissingClones: the answer's missing_repos
// print one warning line naming each missing repo — the acceptance line
// stays (the submit is still accepted).
func TestSubmitHumanOutputWarnsMissingClones(t *testing.T) {
	stdout, stderr, code := submitHumanRun(t,
		`{"accepted":true,"work_item_id":"wi_abc","machine_id":"m-place","capsule_path":"/ws/capsules/pf-42","missing_repos":["ra","rb"]}`)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "accepted by the local daemon") {
		t.Errorf("the submit must stay accepted alongside the warning: %q", stdout)
	}
	for _, name := range []string{"ra", "rb"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("the warning line must name the missing repo %s: %q", name, stdout)
		}
	}
}

// TestSubmitHumanOutputUnknownSilent: an answer with no placement fields
// (the daemon could not resolve the placement) prints ONLY today's
// acceptance line — nothing invented.
func TestSubmitHumanOutputUnknownSilent(t *testing.T) {
	stdout, stderr, code := submitHumanRun(t, `{"accepted":true,"work_item_id":"wi_abc"}`)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if got, want := stdout, "submitted pf-42 (accepted by the local daemon)\n"; got != want {
		t.Errorf("an unknown placement prints only today's line: got %q want %q", got, want)
	}
}
