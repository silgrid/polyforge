package cli

// w350_resume_refusal_test.go (D5): a `pf resume` refused by
// an execution-disabled Cell changes nothing — the CLI reads the Cell's
// execution_disabled from GET /status (an existing route) before it
// touches the hub, so clean_pause stays set, the work item stays paused
// and no resume-paused call is made.

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestW350ResumeRefusedInADisabledCellChangesNothing(t *testing.T) {
	var aihubRec reqRecorder
	aihub := newFakeAihub(t, &aihubRec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	d := newDaemonFixture(t)
	d.answer("GET", "/status", `{"execution_disabled":"no model source"}`)
	d.answer("POST", "/jobs/wi_abc/resume-paused", `{"resumed":true,"work_item_id":"wi_abc","outcome":"attached"}`)

	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock,
	}, "resume", "wi_abc")
	if code != 1 || !strings.Contains(stderr, "DAEMON-409") || !strings.Contains(stderr, "execution is disabled on this Cell: no model source") {
		t.Fatalf("a resume in a disabled Cell must be refused with the reason: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "nothing was changed") || !strings.Contains(stderr, "clean_pause stays set") {
		t.Errorf("the refusal must say nothing changed: %s", stderr)
	}
	if strings.Contains(stderr+stdout, "next start") || strings.Contains(stderr+stdout, "clean_pause cleared") {
		t.Errorf("the refusal must not promise a later resume: %s %s", stdout, stderr)
	}
	for _, r := range aihubRec.all() {
		if r.Method == "PATCH" {
			t.Errorf("the refused resume touched the hub flag: %s %s %q", r.Method, r.Path, r.Body)
		}
	}
	for _, r := range d.rec.all() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/resume-paused") {
			t.Errorf("the refused resume still called the resume door: %s", r.Path)
		}
	}
}
