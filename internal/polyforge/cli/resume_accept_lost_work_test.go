package cli

// `pf resume <id> --accept-lost-work`: on a blocked work item the flag
// rides the human-key resume-blocked door (accept_lost_work) and the CLI
// names the abandoned seal; without it the door body is the plain one; on
// a work item that is not blocked the flag is refused before anything
// reaches aihub's write doors or the daemon.

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

const blockedSealedWI = `{"id":"wi_abc","slug":"pf-42","seq":42,"project":"polyforge","goal":"the goal",
"status":"blocked","priority":"normal","labels":[],"reporter":"usr_1","created_at":"2026-10-01T00:00:00Z",
"updated_at":"2026-10-01T00:00:00Z","attrs":{"blocked_seal":{"head":"abc123","machine":"m-a"}}}`

// runResume drives `pf resume` against a fake aihub answering wiJSON on
// every GET and doorReply on the resume-blocked door, plus a daemon fixture that
// accepts the blocked resume. It answers the door bodies seen.
func runResume(t *testing.T, wiJSON string, args ...string) (code int, stdout, stderr string, doors []string, d *daemonFixture, rec *reqRecorder) {
	return runResumeDoor(t, wiJSON, `{}`, args...)
}

func runResumeDoor(t *testing.T, wiJSON, doorReply string, args ...string) (code int, stdout, stderr string, doors []string, d *daemonFixture, rec *reqRecorder) {
	t.Helper()
	rec = &reqRecorder{}
	aihub := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			io.WriteString(w, wiJSON)
			return
		}
		io.WriteString(w, doorReply)
	})
	d = newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/claim", `{"resumed":true,"work_item_id":"wi_abc"}`)
	code, stdout, stderr = runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock,
	}, append([]string{"resume"}, args...)...)
	for _, r := range rec.all() {
		if strings.HasSuffix(r.Path, "/resume-blocked") {
			doors = append(doors, r.Body)
		}
	}
	return code, stdout, stderr, doors, d, rec
}

func TestResumeAcceptLostWorkRidesTheDoor(t *testing.T) {
	code, stdout, stderr, doors, d, _ := runResume(t, blockedSealedWI, "wi_abc", "--accept-lost-work")
	if code != 0 {
		t.Fatalf("resume --accept-lost-work: code=%d stderr=%s", code, stderr)
	}
	if len(doors) != 1 || !strings.Contains(doors[0], `"accept_lost_work":true`) {
		t.Fatalf("the door must carry accept_lost_work: %q", doors)
	}
	if !strings.Contains(stderr, "abc123") || !strings.Contains(stderr, "m-a") {
		t.Errorf("the CLI must name the abandoned head and machine: %s", stderr)
	}
	if !strings.Contains(stdout, "resumed pf-42") {
		t.Errorf("stdout: %s", stdout)
	}
	if last := d.rec.last(); last.Path != "/jobs/wi_abc/claim" {
		t.Errorf("the daemon must claim after the door; last call %s", last.Path)
	}
}

func TestResumeWithoutAcceptLostWorkSendsThePlainDoor(t *testing.T) {
	code, _, stderr, doors, _, _ := runResume(t, blockedSealedWI, "wi_abc")
	if code != 0 {
		t.Fatalf("resume: code=%d stderr=%s", code, stderr)
	}
	if len(doors) != 1 || strings.TrimSpace(doors[0]) != `{}` {
		t.Fatalf("the plain door body must stay {}: %q", doors)
	}
	if strings.Contains(stderr, "abc123") {
		t.Errorf("no override, no abandon notice: %s", stderr)
	}
}

func TestResumeAcceptLostWorkRefusedWhenNotBlocked(t *testing.T) {
	code, _, stderr, doors, d, rec := runResume(t, cannedWI, "wi_abc", "--accept-lost-work")
	if code == 0 || !strings.Contains(stderr, "only to a blocked work item") {
		t.Fatalf("--accept-lost-work on a queued WI must be refused: code=%d stderr=%s", code, stderr)
	}
	for _, r := range rec.all() {
		if r.Method != "GET" {
			t.Errorf("nothing may be written at aihub: %s %s", r.Method, r.Path)
		}
	}
	if len(doors) != 0 || len(d.rec.all()) != 0 {
		t.Errorf("no door, no daemon call: doors=%q daemon=%v", doors, d.rec.all())
	}
}

// TestResumeAcceptLostWorkSealSurvivesIsAnError: a hub that ignores
// accept_lost_work answers the work item with the seal still on it; the
// CLI must not claim success and must not start the claim.
func TestResumeAcceptLostWorkSealSurvivesIsAnError(t *testing.T) {
	queuedSealed := strings.Replace(blockedSealedWI, `"status":"blocked"`, `"status":"queued"`, 1)
	code, _, stderr, doors, d, _ := runResumeDoor(t, blockedSealedWI, queuedSealed, "wi_abc", "--accept-lost-work")
	if code == 0 || !strings.Contains(stderr, "did not clear the blocked seal") {
		t.Fatalf("a surviving seal must fail the resume: code=%d stderr=%s", code, stderr)
	}
	if strings.Contains(stderr, "is cleared") {
		t.Errorf("no success claim when the seal survives: %s", stderr)
	}
	if len(doors) != 1 || len(d.rec.all()) != 0 {
		t.Errorf("door once, no daemon claim: doors=%q daemon=%v", doors, d.rec.all())
	}
}
