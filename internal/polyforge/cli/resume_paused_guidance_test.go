package cli

// resume_paused_guidance_test.go (wi_seWTgHeKtaa8): the operator-guidance
// branches of the paused door — the flag clear at the hub always happens,
// and the CLI's answer depends on what the LOCAL daemon can do with it:
//   - the daemon does not hold the paused work item (DAEMON-404): the
//     holder is another machine (or gone) — the end-session + submit
//     recovery guidance, exit 0 (the flag clear stands);
//   - the daemon socket is unreachable: the holder daemon picks the
//     resume up at its next start (the restart reconciliation fallback),
//     exit 0.

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// filepathInTemp returns a path under a fresh temp dir (a socket path
// nobody listens on, for the unreachable-daemon golden).
func filepathInTemp(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

// TestResumePausedHolderElsewhereGuidance: a daemon that answers
// DAEMON-404 (it holds no paused record for the WI) — the resume is NOT
// an error: the flag is cleared and the guidance names the holder
// machine's moves.
func TestResumePausedHolderElsewhereGuidance(t *testing.T) {
	var aihubRec reqRecorder
	aihub := newFakeAihub(t, &aihubRec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	d := newDaemonFixture(t) // no resume-paused answer scripted: DAEMON-404

	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock,
	}, "resume", "wi_abc")
	if code != 0 {
		t.Fatalf("a not-held resume is guidance, not an error: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "clean_pause cleared") {
		t.Errorf("stdout must say the flag was cleared: %s", stdout)
	}
	if !strings.Contains(stdout, "does not hold the paused session") {
		t.Errorf("stdout must say this daemon is not the holder: %s", stdout)
	}
	if !strings.Contains(stdout, "pf ops end-session") {
		t.Errorf("stdout must name the holder-gone recovery move: %s", stdout)
	}
	if strings.Contains(stdout, "does NOT pick this up") {
		t.Errorf("the stale next-start copy must be gone: %s", stdout)
	}
	// the daemon door WAS tried (exactly once, the live-holder route).
	var posts []string
	for _, r := range d.rec.all() {
		if r.Method == "POST" {
			posts = append(posts, r.Path)
		}
	}
	if strings.Join(posts, ",") != "/jobs/wi_abc/resume-paused" {
		t.Errorf("daemon POSTs = %v, want the one resume-paused call", posts)
	}
}

// TestResumePausedDaemonUnreachableGuidance: no daemon on the socket —
// the flag clear stands and the guidance points at the holder daemon's
// next start (the restart reconciliation fallback that has always
// worked), exit 0.
func TestResumePausedDaemonUnreachableGuidance(t *testing.T) {
	var aihubRec reqRecorder
	aihub := newFakeAihub(t, &aihubRec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	sock := filepathInTemp(t, "dead.sock") // nothing listens here

	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": sock,
	}, "resume", "wi_abc")
	if code != 0 {
		t.Fatalf("an unreachable daemon is guidance, not an error: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "clean_pause cleared") {
		t.Errorf("stdout must say the flag was cleared: %s", stdout)
	}
	if !strings.Contains(stdout, "daemon socket is unreachable") {
		t.Errorf("stdout must say the daemon could not be reached: %s", stdout)
	}
	if !strings.Contains(stdout, "restart reconciliation attaches the paused session") {
		t.Errorf("stdout must name the next-start fallback: %s", stdout)
	}
	// the hub-side effect landed exactly as before: precheck GET + PATCH.
	last := aihubRec.last()
	if last.Method != "PATCH" || !strings.Contains(last.Body, `"clean_pause":false`) {
		t.Errorf("resume must PATCH clean_pause=false; last aihub call = %s %s %q", last.Method, last.Path, last.Body)
	}
}

// TestResumePausedBlockedRefusalSurfaces: a typed refusal from the
// daemon (a missing capsule blocked the WI) surfaces as the CLI error —
// the operator sees BLOCKED and the typed cause, never the restart copy.
func TestResumePausedBlockedRefusalSurfaces(t *testing.T) {
	var aihubRec reqRecorder
	aihub := newFakeAihub(t, &aihubRec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	d := newDaemonFixture(t)
	d.answers["POST /jobs/wi_abc/resume-paused"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"code":"DAEMON-409","detail":"work item wi_abc: mid-flight capsule missing at /ws/capsules/wi_abc — it is NOT recoverable from the remote"}`)
	}

	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock,
	}, "resume", "wi_abc")
	if code != 1 {
		t.Fatalf("a blocked resume must exit 1, got %d (stdout %s stderr %s)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "DAEMON-409") || !strings.Contains(stderr, "capsule missing") {
		t.Errorf("stderr must carry the typed blocked cause: %s", stderr)
	}
	if !strings.Contains(stderr, "hub side succeeded") || !strings.Contains(stderr, "next start") {
		t.Errorf("stderr must say the hub-side clear succeeded and the next daemon start reconciles it: %s", stderr)
	}
}

// TestResumePausedParkedOutcomeGuidance: the holder re-attached the
// session but its recovery is parked on the previous driver Run — the CLI
// says so (it continues by itself once that Run exits) instead of
// claiming the step continues in place or that the work item closed.
func TestResumePausedParkedOutcomeGuidance(t *testing.T) {
	var aihubRec reqRecorder
	aihub := newFakeAihub(t, &aihubRec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/resume-paused", `{"resumed":false,"work_item_id":"wi_abc","outcome":"parked"}`)

	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock,
	}, "resume", "wi_abc")
	if code != 0 {
		t.Fatalf("a parked resume is an answer, not an error: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "recovery is parked") || !strings.Contains(stdout, "once that Run exits") {
		t.Errorf("stdout must name the park and how it ends: %s", stdout)
	}
	if strings.Contains(stdout, "continues the current step in place") || strings.Contains(stdout, "closed the work item") {
		t.Errorf("a parked resume must not read as continued or closed: %s", stdout)
	}
}
