package cli

// verbs_session_test.go: the v1.5 session-model verb goldens —
// the pause clean_pause flag ordering, ops end-session,
// and the resume flag-clear semantics.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestResumeFlagClearSemantics: the v1.5 resume clears clean_pause on
// the hub (PATCH, flag=false) and then makes the resume observable by
// the ONLINE holder: the CLI calls the daemon's resume-paused route
// (wi_seWTgHeKtaa8, v2-converge §7) — before that fix the hub-side flag
// clear was the resume's ONLY effect (no daemon call), so a live holder
// never picked the resume up. The old v1 behavior (fresh local claim
// via POST /jobs/{id}/resume) stays gone: this is the in-place
// continuation door, not a claim.
func TestResumeFlagClearSemantics(t *testing.T) {
	var aihubRec reqRecorder
	aihub := newFakeAihub(t, &aihubRec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/resume-paused", `{"resumed":true,"work_item_id":"wi_abc","outcome":"attached"}`)

	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock,
	}, "resume", "wi_abc")
	if code != 0 {
		t.Fatalf("resume: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "clean_pause cleared") {
		t.Errorf("stdout must say the flag was cleared: %s", stdout)
	}
	if !strings.Contains(stdout, "continues the current step in place") {
		t.Errorf("stdout must say the holder continues in place: %s", stdout)
	}
	// Precheck (GET) then the flag PATCH; both against aihub only.
	last := aihubRec.last()
	if last.Method != "PATCH" || !strings.Contains(last.Body, `"clean_pause":false`) {
		t.Errorf("resume must PATCH clean_pause=false; last aihub call = %s %s %q",
			last.Method, last.Path, last.Body)
	}
	for _, r := range aihubRec.all() {
		if strings.HasPrefix(r.Path, "/jobs/") {
			t.Errorf("the daemon routes must go to the daemon socket, not aihub; saw %s", r.Path)
		}
	}
	// The live-holder door: exactly one daemon call, the resume-paused
	// route (never the old v1 claim door).
	var daemonPosts []string
	for _, r := range d.rec.all() {
		if r.Method == "POST" {
			daemonPosts = append(daemonPosts, r.Path)
		}
	}
	if strings.Join(daemonPosts, ",") != "/jobs/wi_abc/resume-paused" {
		t.Errorf("resume must call the daemon's resume-paused route once; daemon POSTs = %v", daemonPosts)
	}
}

// TestWrapFailOnBlockedPrintsResumeOrCancelHint: `pf wrap --fail` on an
// execution ticket with no local job (the parked-blocked shape — the
// daemon answers the wrap probe with DAEMON-404) prints the dedicated
// resume-or-cancel hint, exits non-zero (the wrap did not happen) and
// touches NOTHING else: the hub is only read (the work item has a
// bundle, so the hub wrap door is not taken), no termination — the owner
// continues with `pf resume` or abandons with `pf cancel`. A plain wrap
// on the same shape stays the typed error.
func TestWrapFailOnBlockedPrintsResumeOrCancelHint(t *testing.T) {
	var rec reqRecorder
	execWI := strings.Replace(strings.Replace(cannedWI, `"queued"`, `"blocked"`, 1),
		`"labels"`, `"bundle_name":"demo-bundle","labels"`, 1)
	hub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("the hub may only be read: %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, execWI)
	})
	d := newDaemonFixture(t) // wrap route unmatched -> DAEMON-404
	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER": hub.URL, "PF_DAEMON_SOCKET": d.sock, "PF_API_KEY": "human-key",
	}, "wrap", "wi_blocked", "--fail")
	// the wrap did not happen: non-zero exit with the typed error, the
	// hint alongside it on stderr
	if code != 1 || !strings.Contains(stderr, "DAEMON-404") {
		t.Fatalf("wrap --fail on no local job must fail with DAEMON-404: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{"blocked", "pf resume", "pf cancel"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the hint must name %q: %s", want, stderr)
		}
	}
	if got := d.rec.last(); got.Path != "/jobs/wi_blocked/wrap" || !strings.Contains(got.Body, `"reason":"fail"`) {
		t.Errorf("only the wrap probe may reach the daemon: %+v", d.rec.all())
	}
	for _, r := range rec.all() {
		if r.Method != "GET" {
			t.Errorf("the hub may only be read: %v", rec.all())
		}
	}

	// plain wrap (no --fail) on the same no-local-job shape: the typed error
	code, _, stderr = runPf(t, map[string]string{
		"PF_SERVER": hub.URL, "PF_DAEMON_SOCKET": d.sock, "PF_API_KEY": "human-key",
	}, "wrap", "wi_blocked")
	if code != 1 || !strings.Contains(stderr, "DAEMON-404") {
		t.Errorf("plain wrap must stay the typed error: code=%d stderr=%s", code, stderr)
	}
}

// TestResumeBlockedDoorWiring (polyforge#116): a workflow parked blocked
// answers the §6.4 owner move, not the pause copy — the hub status picks
// the door, and the blocked door contacts the daemon's claim
// route (the paused door now contacts the daemon's resume-paused route —
// the live-holder continuation of wi_seWTgHeKtaa8; the test above pins
// that door's wire shape).
func TestResumeBlockedDoorWiring(t *testing.T) {
	blockedWI := `{"id":"wi_blocked","slug":"pf-43","seq":43,"project":"polyforge","goal":"the goal",` +
		`"status":"blocked","priority":"normal","labels":["a"],"reporter":"usr_1","created_at":"2026-10-01T00:00:00Z",` +
		`"updated_at":"2026-10-01T00:00:00Z"}`
	var aihubRec reqRecorder
	aihub := newFakeAihub(t, &aihubRec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, blockedWI)
	})
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_blocked/claim", `{"resumed":true,"work_item_id":"wi_blocked"}`)
	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock, "PF_API_KEY": "human-key",
	}, "resume", "wi_blocked")
	if code != 0 {
		t.Fatalf("resume: code=%d stderr=%s", code, stderr)
	}
	if strings.Contains(stdout, "clean_pause cleared") {
		t.Errorf("a parked workflow must never answer the pause copy: %s", stdout)
	}
	if !strings.Contains(stdout, "resumed pf-43") || !strings.Contains(stdout, "parked blocked") {
		t.Errorf("stdout must carry the §6.4 owner-move copy: %s", stdout)
	}
	// The hub door is landed by the CLI with the HUMAN's key, before the
	// daemon is contacted.
	var door *capturedRequest
	for _, r := range aihubRec.all() {
		r := r
		if r.Method == "POST" && r.Path == "/api/v1/work-items/wi_blocked/resume-blocked" {
			door = &r
		}
	}
	if door == nil {
		t.Fatalf("the CLI must call the hub resume-blocked door itself: %+v", aihubRec.all())
	}
	if got := door.Header.Get("Authorization"); !strings.Contains(got, "human-key") {
		t.Errorf("the door must carry the human's api key, got %q", got)
	}
	// The daemon's owner-move route is contacted exactly once.
	calls := d.rec.all()
	if len(calls) != 1 || calls[0].Path != "/jobs/wi_blocked/claim" || !strings.Contains(calls[0].Body, `"resume":true`) {
		t.Errorf("resume must contact the daemon's claim route once, asking for the synchronous start: %+v", calls)
	}
	// A refused door (non-owner member) stops before the daemon is asked.
	d2 := newDaemonFixture(t)
	d2.answer("POST", "/jobs/wi_blocked/claim", `{"resumed":true,"work_item_id":"wi_blocked"}`)
	deny := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"code":"AUTH-403","detail":"resume-blocked requires the work item's reporter, a project maintainer or admin"}`)
			return
		}
		io.WriteString(w, blockedWI)
	})
	code, _, stderr = runPf(t, map[string]string{
		"PF_SERVER": deny.URL, "PF_DAEMON_SOCKET": d2.sock, "PF_API_KEY": "plain-member-key",
	}, "resume", "wi_blocked")
	if code != 1 || !strings.Contains(stderr, "AUTH-403") {
		t.Errorf("a non-owner member must be refused: code=%d stderr=%s", code, stderr)
	}
	if n := len(d2.rec.all()); n != 0 {
		t.Errorf("a refused door must not contact the daemon: %v", d2.rec.all())
	}
	// No clean_pause PATCH lands for a blocked WI (the pause copy's door).
	for _, r := range aihubRec.all() {
		if r.Method == "PATCH" {
			t.Errorf("a parked WI must not PATCH clean_pause: %s %s", r.Method, r.Path)
		}
	}
}

// TestResumeWireErrorClasses: with the daemon socket deliberately dead
// (resume never dials it), the exit code rides the aihub face alone.
func TestResumeWireErrorClasses(t *testing.T) {
	// Typed 4xx on the PATCH → 1.
	aihub := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"code":"CORE-403","detail":"no write access"}`)
			return
		}
		io.WriteString(w, cannedWI)
	})
	code, _, stderr := runPf(t, map[string]string{"PF_SERVER": aihub.URL}, "resume", "wi_abc")
	if code != 1 || !strings.Contains(stderr, "CORE-403") {
		t.Errorf("typed 4xx: exit=%d stderr=%s", code, stderr)
	}

	// Typed 5xx → 2.
	aihub5 := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"code":"CORE-500","detail":"boom"}`)
			return
		}
		io.WriteString(w, cannedWI)
	})
	code, _, _ = runPf(t, map[string]string{"PF_SERVER": aihub5.URL}, "resume", "wi_abc")
	if code != 2 {
		t.Errorf("typed 5xx: exit=%d, want 2", code)
	}

	// Terminal work item → the precheck refuses before any PATCH.
	aihubT := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"wi_abc","slug":"pf-1","status":"wrapped","project":"polyforge","goal":"g","created_at":"t"}`)
	})
	code, _, stderr = runPf(t, map[string]string{"PF_SERVER": aihubT.URL}, "resume", "wi_abc")
	if code != 1 || !strings.Contains(stderr, "terminal") {
		t.Errorf("terminal precheck: exit=%d stderr=%s", code, stderr)
	}
}

// TestPauseNeedsNoHub: the atomic pause (merge amend 724c5e4) touches
// ONLY the daemon route — the clean_pause flag rides the daemon's own
// fenced SessionEnd, so a CLI with no hub configured pauses fine, and
// nothing is written anywhere before the daemon accepts.
func TestPauseNeedsNoHub(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/pause", `{}`)
	code, _, stderr := runPf(t, map[string]string{
		// No PF_SERVER: nothing outside the daemon route is dialed.
		"PF_DAEMON_SOCKET": d.sock,
	}, "pause", "wi_abc")
	if code != 0 {
		t.Fatalf("pause: code=%d stderr=%s", code, stderr)
	}
	got := d.rec.last()
	if got.Method != "POST" || got.Path != "/jobs/wi_abc/pause" {
		t.Errorf("pause request = %s %s %q", got.Method, got.Path, got.Body)
	}
}

// TestPauseRefusalWritesNothing: a refused pause makes NO hub write at
// all — the flag lands atomically with the pause transition, so there
// is no early write to undo and no stale marker can survive a refusal.
func TestPauseRefusalWritesNothing(t *testing.T) {
	var aihubRec reqRecorder
	aihub := newFakeAihub(t, &aihubRec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	d := newDaemonFixture(t)
	// No pause answer → the fixture answers DAEMON-404.
	code, _, stderr := runPf(t, map[string]string{
		"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock,
	}, "pause", "wi_abc")
	if code != 1 || !strings.Contains(stderr, "DAEMON-404") {
		t.Fatalf("pause refusal: code=%d stderr=%s", code, stderr)
	}
	if n := len(aihubRec.all()); n != 0 {
		t.Errorf("the refused pause must write nothing to the hub; saw %d calls: %v", n, aihubRec.all())
	}
}

// TestResumeDispatchByWIState pins the two-way `pf resume` order: blocked ->
// human-key door then the daemon's synchronous claim; otherwise (paused) -> the plain
// clean_pause PATCH and the daemon's resume-paused route (the live-holder
// continuation, wi_seWTgHeKtaa8).
func TestResumeDispatchByWIState(t *testing.T) {
	wiWith := func(status string, clean bool) string {
		return `{"id":"wi_abc","slug":"pf-42","seq":42,"project":"polyforge","goal":"g","status":"` + status +
			`","clean_pause":` + map[bool]string{true: "true", false: "false"}[clean] +
			`,"priority":"normal","labels":["a"],"reporter":"usr_1","created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-01T00:00:00Z"}`
	}
	cases := []struct {
		name         string
		hubStatus    string
		clean        bool
		wantPatch    bool
		wantDoor     bool
		wantDaemon   []string // POST paths expected on the daemon
		wantStdoutIn string
	}{
		{"paused", "paused", true, true, false, []string{"/jobs/wi_abc/resume-paused"}, "clean_pause cleared"},
		{"blocked", "blocked", false, false, true, []string{"/jobs/wi_abc/claim"}, "parked blocked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec reqRecorder
			aihub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, wiWith(tc.hubStatus, tc.clean))
			})
			d := newDaemonFixture(t)
			d.answer("POST", "/jobs/wi_abc/claim", `{"resumed":true,"work_item_id":"wi_abc"}`)
			d.answer("POST", "/jobs/wi_abc/resume-paused", `{"resumed":true,"work_item_id":"wi_abc","outcome":"attached"}`)
			d.answer("POST", "/jobs/wi_abc/resume", `{"accepted":true,"work_item_id":"wi_abc"}`)
			code, stdout, stderr := runPf(t, map[string]string{
				"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock, "PF_API_KEY": "human-key",
			}, "resume", "wi_abc")
			if code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr)
			}
			if !strings.Contains(stdout, tc.wantStdoutIn) {
				t.Errorf("stdout missing %q: %s", tc.wantStdoutIn, stdout)
			}
			var patched, door bool
			for _, r := range rec.all() {
				if r.Method == "PATCH" {
					patched = true
				}
				if r.Method == "POST" && strings.HasSuffix(r.Path, "/resume-blocked") {
					door = true
				}
			}
			if patched != tc.wantPatch || door != tc.wantDoor {
				t.Errorf("patch=%v (want %v) door=%v (want %v)", patched, tc.wantPatch, door, tc.wantDoor)
			}
			var posts []string
			for _, r := range d.rec.all() {
				if r.Method == "POST" {
					posts = append(posts, r.Path)
					if r.Path == "/jobs/wi_abc/claim" && !strings.Contains(r.Body, `"resume":true`) {
						t.Errorf("the blocked resume's claim body %s does not ask for the synchronous start", r.Body)
					}
				}
			}
			if strings.Join(posts, ",") != strings.Join(tc.wantDaemon, ",") {
				t.Errorf("daemon POSTs = %v, want %v", posts, tc.wantDaemon)
			}
		})
	}
}

// TestOpsEndSessionHappyPath: `pf ops end-session <wi> --reason ...` POSTs
// the force-end-session route with the {reason} body on the caller's key
// and tells the operator the WI is queued and claimable via `pf submit`.
func TestOpsEndSessionHappyPath(t *testing.T) {
	var rec reqRecorder
	aihub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	code, stdout, stderr := runPf(t, map[string]string{"PF_SERVER": aihub.URL},
		"ops", "end-session", "wi_abc", "--reason", "holder machine died")
	if code != 0 {
		t.Fatalf("end-session: code=%d stderr=%s", code, stderr)
	}
	got := rec.last()
	if got.Method != "POST" || got.Path != "/api/v1/work-items/wi_abc/force-end-session" {
		t.Fatalf("request = %s %s", got.Method, got.Path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.Body), &body); err != nil {
		t.Fatalf("body must decode: %v (%s)", err, got.Body)
	}
	if body["reason"] != "holder machine died" || len(body) != 1 {
		t.Errorf("body = %v, want only {reason}", body)
	}
	if !strings.Contains(stdout, "queued") || !strings.Contains(stdout, "pf submit wi_abc") {
		t.Errorf("stdout must mention queued and pf submit: %s", stdout)
	}
}

// TestOpsEndSessionJSON: --json emits the work item id + queued status.
func TestOpsEndSessionJSON(t *testing.T) {
	aihub := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	code, stdout, stderr := runPf(t, map[string]string{"PF_SERVER": aihub.URL},
		"--json", "ops", "end-session", "wi_abc", "--reason", "drill")
	if code != 0 {
		t.Fatalf("end-session --json: code=%d stderr=%s", code, stderr)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("json output must decode: %v (%s)", err, stdout)
	}
	if out["work_item_id"] != "wi_abc" || out["status"] != "queued" {
		t.Errorf("json = %v", out)
	}
}

// TestOpsEndSessionRefusals: a missing/blank --reason or a missing id is
// refused before any request is sent.
func TestOpsEndSessionRefusals(t *testing.T) {
	var rec reqRecorder
	aihub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	for _, args := range [][]string{
		{"ops", "end-session", "wi_abc"},
		{"ops", "end-session", "wi_abc", "--reason", "   "},
		{"ops", "end-session", "--reason", "drill"},
	} {
		code, _, stderr := runPf(t, map[string]string{"PF_SERVER": aihub.URL}, args...)
		if code == 0 {
			t.Errorf("%v: must be refused", args)
		}
		if stderr == "" {
			t.Errorf("%v: refusal must explain itself", args)
		}
	}
	if n := len(rec.all()); n != 0 {
		t.Errorf("refused invocations must send nothing, saw %d requests", n)
	}
}

// TestOpsEndSessionForbidden: a 403 from aihub surfaces as an error with
// the typed code and the standard 4xx exit code (1).
func TestOpsEndSessionForbidden(t *testing.T) {
	aihub := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"code":"AUTH-403","detail":"force-end-session requires admin or a project maintainer"}`)
	})
	code, _, stderr := runPf(t, map[string]string{"PF_SERVER": aihub.URL},
		"ops", "end-session", "wi_abc", "--reason", "drill")
	if code != 1 || !strings.Contains(stderr, "AUTH-403") {
		t.Fatalf("403: exit=%d stderr=%s", code, stderr)
	}
}
