package cli

// wrap_nobundle_test.go: `pf wrap` on a work item with no bundle. The
// daemon has no job for it, so the CLI reads the work item at aihub and,
// when it is open with no bundle, aihub's wrap door records the verdict.
// A running work item keeps the daemon path alone; an older hub answers
// a clear upgrade message; a workflow step never reaches aihub.

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// noBundleHub answers GET with the queued no-bundle cannedWI and the wrap
// POST with the record moved to status.
func noBundleHub(t *testing.T, rec *reqRecorder, status string) string {
	t.Helper()
	srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			io.WriteString(w, strings.Replace(cannedWI, `"queued"`, `"`+status+`"`, 1))
			return
		}
		io.WriteString(w, cannedWI)
	})
	return srv.URL
}

func TestWrapNoBundleClosesAtHub(t *testing.T) {
	var rec reqRecorder
	d := newDaemonFixture(t) // no local job -> DAEMON-404
	env := map[string]string{"PF_SERVER": noBundleHub(t, &rec, "wrapped"), "PF_DAEMON_SOCKET": d.sock}

	code, stdout, stderr := runPf(t, env, "wrap", "wi_abc", "--note", "shipped in v2.15")
	if code != 0 {
		t.Fatalf("wrap: code=%d stderr=%s", code, stderr)
	}
	reqs := rec.all()
	if len(reqs) != 2 || reqs[0].Method != "GET" || reqs[0].Path != "/api/v1/work-items/wi_abc" ||
		reqs[1].Method != "POST" || reqs[1].Path != "/api/v1/work-items/wi_abc/wrap" ||
		reqs[1].Body != `{"note":"shipped in v2.15"}` {
		t.Fatalf("hub requests = %+v", reqs)
	}
	if stdout != "pf-42 closed at aihub (no bundle): status wrapped\n" {
		t.Errorf("stdout = %q", stdout)
	}

	// --fail records the failed verdict; --json passes the record through
	var frec reqRecorder
	env["PF_SERVER"] = noBundleHub(t, &frec, "failed")
	code, stdout, stderr = runPf(t, env, "--json", "wrap", "wi_abc", "--fail")
	if code != 0 {
		t.Fatalf("wrap --fail: code=%d stderr=%s", code, stderr)
	}
	if got := frec.last(); got.Method != "POST" || got.Body != `{"fail":true}` {
		t.Errorf("fail request = %+v", got)
	}
	if want := strings.Replace(cannedWI, `"queued"`, `"failed"`, 1) + "\n"; stdout != want {
		t.Errorf("json stdout = %q", stdout)
	}

	// no daemon running here: a no-bundle work item still closes
	var nrec reqRecorder
	env = map[string]string{"PF_SERVER": noBundleHub(t, &nrec, "wrapped"),
		"PF_DAEMON_SOCKET": filepath.Join(t.TempDir(), "gone.sock")}
	if code, _, stderr := runPf(t, env, "wrap", "wi_abc"); code != 0 || nrec.last().Path != "/api/v1/work-items/wi_abc/wrap" {
		t.Fatalf("daemon down: code=%d stderr=%s reqs=%+v", code, stderr, nrec.all())
	}
}

// TestWrapRunningStaysOnTheDaemon: the daemon wraps a running work item
// exactly as before — aihub is not contacted; a --note it cannot keep is
// said on stderr (in --json mode too).
func TestWrapRunningStaysOnTheDaemon(t *testing.T) {
	var rec reqRecorder
	hub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the hub must not be contacted: %s %s", r.Method, r.URL.Path)
	})
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/wrap", `{"work_item_id":"wi_abc","slug":"pf-42","outcome":"sealed","reason":"wrapped"}`)
	code, stdout, stderr := runPf(t, map[string]string{"PF_SERVER": hub.URL, "PF_DAEMON_SOCKET": d.sock},
		"wrap", "wi_abc", "--note", "done")
	if code != 0 || stdout != "wrap requested for pf-42\n" || !strings.Contains(stderr, "note not recorded") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runPf(t, map[string]string{"PF_SERVER": hub.URL, "PF_DAEMON_SOCKET": d.sock},
		"--json", "wrap", "wi_abc", "--note", "done")
	if code != 0 || !strings.Contains(stdout, `"outcome":"sealed"`) || !strings.Contains(stderr, "note not recorded") {
		t.Fatalf("json: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("hub contacted %d time(s)", n)
	}
}

// TestWrapDaemonRefusalStands: a daemon answer other than "no local job"
// (a conflict, a server failure) is returned as before — same exit code,
// and aihub is not contacted.
func TestWrapDaemonRefusalStands(t *testing.T) {
	var rec reqRecorder
	hub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the hub must not be contacted: %s %s", r.Method, r.URL.Path)
	})
	for _, tc := range []struct {
		status int
		code   string
		exit   int
	}{{409, "DAEMON-409", 1}, {500, "DAEMON-500", 2}} {
		d := newDaemonFixture(t)
		d.answers["POST /jobs/wi_abc/wrap"] = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			io.WriteString(w, `{"code":"`+tc.code+`","detail":"refused"}`)
		}
		code, _, stderr := runPf(t, map[string]string{"PF_SERVER": hub.URL, "PF_DAEMON_SOCKET": d.sock}, "wrap", "wi_abc")
		if code != tc.exit || !strings.Contains(stderr, tc.code) {
			t.Fatalf("%s: code=%d stderr=%s", tc.code, code, stderr)
		}
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("hub contacted %d time(s)", n)
	}
}

// TestWrapNoBundleOldHub: a hub without the wrap door answers Go's bare
// 404; the CLI says to upgrade aihub instead of a raw HTTP line.
func TestWrapNoBundleOldHub(t *testing.T) {
	var rec reqRecorder
	hub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, cannedWI)
	})
	d := newDaemonFixture(t)
	code, _, stderr := runPf(t, map[string]string{"PF_SERVER": hub.URL, "PF_DAEMON_SOCKET": d.sock}, "wrap", "wi_abc")
	if code != 1 || !strings.Contains(stderr, "upgrade aihub first") || strings.Contains(stderr, "404 page not found") {
		t.Fatalf("old hub: code=%d stderr=%s", code, stderr)
	}
}

// TestWrapNoBundleNotOpen: a no-bundle work item that is
// not open (here: already canceled) is not sent to the hub door; the
// daemon's answer stands.
func TestWrapNoBundleNotOpen(t *testing.T) {
	var rec reqRecorder
	hub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("the hub may only be read: %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, strings.Replace(cannedWI, `"queued"`, `"canceled"`, 1))
	})
	d := newDaemonFixture(t)
	code, _, stderr := runPf(t, map[string]string{"PF_SERVER": hub.URL, "PF_DAEMON_SOCKET": d.sock}, "wrap", "wi_abc")
	if code != 1 || !strings.Contains(stderr, "DAEMON-404") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

// TestStepWrapNeverReachesAihub: even with no daemon to answer, a step's
// wrap does not take the hub path (step agents hold no aihub access).
func TestStepWrapNeverReachesAihub(t *testing.T) {
	env, _, arec := stepEnv(t)
	env["PF_DAEMON_SOCKET"] = filepath.Join(t.TempDir(), "gone.sock")
	if code, _, _ := runPf(t, env, "wrap", "wi_st"); code == 0 {
		t.Fatal("a step wrap with no daemon job must fail")
	}
	if n := len(arec.all()); n != 0 {
		t.Fatalf("a step wrap reached aihub %d time(s)", n)
	}
}
