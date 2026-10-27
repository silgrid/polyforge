package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// daemonFixture serves the daemon UDS surface with a recording mux plus
// a per-route answer function.
type daemonFixture struct {
	rec     *reqRecorder
	sock    string
	answers map[string]func(w http.ResponseWriter, r *http.Request)
}

// newDaemonFixture starts a fake daemon on a REAL unix socket. The mux
// answers /jobs/... and /status routes from d.answers (keyed by
// "<METHOD> <path>"); unmatched routes answer 404 DAEMON-404.
func newDaemonFixture(t *testing.T) *daemonFixture {
	t.Helper()
	d := &daemonFixture{rec: &reqRecorder{}, answers: map[string]func(w http.ResponseWriter, r *http.Request){}}
	mux := http.NewServeMux()
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		d.rec.record(r)
		key := r.Method + " " + r.URL.Path
		if h, ok := d.answers[key]; ok {
			h(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"code":"DAEMON-404","detail":"no local capsule for this work item"}`)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		d.rec.record(r)
		key := r.Method + " " + r.URL.Path
		if h, ok := d.answers[key]; ok {
			h(w, r)
			return
		}
		io.WriteString(w, `{"machine_id":"m1","auto":false,"workspaces":["/srv/pf-ws","/srv/pf-ws2"],"shutting_down":false,"jobs":[],"queue":{"pending":0,"running":0,"max_concurrent":2}}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		d.rec.record(r)
		key := r.Method + " " + r.URL.Path
		if h, ok := d.answers[key]; ok {
			h(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"code":"DAEMON-404","detail":"no such route"}`)
	})
	d.sock = newFakeDaemon(t, mux)
	return d
}

// roundtrip: one canned route answer.
func (d *daemonFixture) answer(method, path string, body string) {
	d.answers[method+" "+path] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}
}

func TestDaemonClientSubmitFamilyRoundtrip(t *testing.T) {
	d := newDaemonFixture(t)
	for _, intent := range []string{"claim"} {
		d.answer("POST", "/jobs/wi_abc/"+intent, `{"accepted":true,"work_item_id":"wi_abc"}`)
	}
	c := NewDaemonClient(d.sock)

	cases := []struct {
		call func() (json.RawMessage, error)
		want string
	}{
		// The plain Claim/Resume client arms (and the plain resume route)
		// were deleted with the audit's dead-code sweep: the claim face is
		// ClaimWithModels, the parked-blocked face is ResumeBlocked.
		{func() (json.RawMessage, error) { return c.ClaimWithModels("wi_abc", "high", nil) }, "/jobs/wi_abc/claim"},
	}
	for i, tc := range cases {
		raw, err := tc.call()
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		got := d.rec.all()[i]
		if got.Method != "POST" || got.Path != tc.want {
			t.Errorf("request = %s %s, want POST %s", got.Method, got.Path, tc.want)
		}
		wantPriority := "normal"
		if i == 0 {
			wantPriority = "high"
		}
		if !strings.Contains(got.Body, `"priority":"`+wantPriority+`"`) {
			t.Errorf("body = %s, want priority %q", got.Body, wantPriority)
		}
		var sr SubmitResponse
		if err := json.Unmarshal(raw, &sr); err != nil || !sr.Accepted || sr.WorkItemID != "wi_abc" {
			t.Errorf("SubmitResponse mismatch: %s err=%v", raw, err)
		}
	}
}

func TestDaemonClientAnswerRoundtrip(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/answer", `{"applied":true,"delivered":true}`)
	c := NewDaemonClient(d.sock)

	payload := AnswerPayload{
		QuestionID: "q_1",
		AnswerText: "yes, proceed",
		AnsweredBy: "usr_9",
	}
	if _, err := c.Answer("wi_abc", payload); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	got := d.rec.last()
	if got.Method != "POST" || got.Path != "/jobs/wi_abc/answer" {
		t.Errorf("request = %s %s", got.Method, got.Path)
	}
	// The payload IS the body (three fields, verbatim), and the
	// caller identity travels in X-PF-User.
	if got.Header.Get("X-PF-User") != "usr_9" {
		t.Errorf("X-PF-User = %q, want usr_9", got.Header.Get("X-PF-User"))
	}
	var body AnswerPayload
	if err := json.Unmarshal([]byte(got.Body), &body); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	if body != payload {
		t.Errorf("body = %+v, want %+v", body, payload)
	}
}

func TestDaemonClientPauseWrapResumeEndToEnd(t *testing.T) {
	// End-to-end through Run: pause (clean_pause flag on the hub AND in
	// the daemon request body), wrap, and the v1.5 resume (clear the
	// clean_pause flag on the hub; the paused WI stays with its holder
	// daemon — no local claim).
	var aihubRec reqRecorder
	aihub := newFakeAihub(t, &aihubRec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"wi_abc","slug":"pf-1","status":"paused","project":"polyforge","goal":"g","created_at":"t"}`)
	})
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/pause", `{}`)
	d.answer("POST", "/jobs/wi_abc/wrap", `{"sealed":true}`)

	env := map[string]string{"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock}
	if code, _, stderr := runPf(t, env, "pause", "wi_abc"); code != 0 {
		t.Fatalf("pause: code=%d stderr=%s", code, stderr)
	}
	// Merge amend: the pause is ATOMIC — only the daemon route is
	// contacted; the clean_pause flag rides the daemon's own fenced
	// SessionEnd (status=paused), so there is no early hub PATCH.
	if got := d.rec.last(); got.Method != "POST" || got.Path != "/jobs/wi_abc/pause" {
		t.Errorf("pause request = %s %s %q", got.Method, got.Path, got.Body)
	}
	for _, r := range aihubRec.all() {
		if r.Method == "PATCH" {
			t.Errorf("atomic pause must not PATCH the hub first; saw %s %s %q", r.Method, r.Path, r.Body)
		}
	}
	if code, _, stderr := runPf(t, env, "wrap", "wi_abc", "--fail"); code != 0 {
		t.Fatalf("wrap --fail: code=%d stderr=%s", code, stderr)
	}
	if got := d.rec.last(); got.Path != "/jobs/wi_abc/wrap" || !strings.Contains(got.Body, `"reason":"fail"`) {
		t.Errorf("wrap request = %s %s %q", got.Method, got.Path, got.Body)
	}
	if code, _, stderr := runPf(t, env, "resume", "wi_abc"); code != 0 {
		t.Fatalf("resume: code=%d stderr=%s", code, stderr)
	}
	// v1.5 resume flips the hub flag only: the LAST aihub call is the
	// clean_pause=false PATCH, and NO daemon route was contacted.
	last := aihubRec.last()
	if last.Method != "PATCH" || !strings.Contains(last.Body, `"clean_pause":false`) {
		t.Errorf("resume must PATCH clean_pause=false on the hub; last aihub call = %s %s %q",
			last.Method, last.Path, last.Body)
	}
	if got := d.rec.last(); got.Path == "/jobs/wi_abc/resume" {
		t.Errorf("v1.5 resume must not claim locally; the daemon saw %s", got.Path)
	}
}

func TestDaemonClientTypedErrorDecoding(t *testing.T) {
	d := newDaemonFixture(t)
	// No answer registered for watch → the fixture answers DAEMON-404.
	c := NewDaemonClient(d.sock)
	_, err := c.WatchSnapshot("wi_missing", 0, false)
	if err == nil {
		t.Fatal("expected typed error")
	}
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != "DAEMON-404" {
		t.Fatalf("want DAEMON-404, got %v", err)
	}
	if ae.HTTPStatus() != 404 {
		t.Errorf("HTTPStatus = %d, want 404", ae.HTTPStatus())
	}
	if code, _, _ := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock},
		"watch", "wi_missing", "--once"); code != 1 {
		t.Errorf("DAEMON-404 must exit 1, got %d", code)
	}
}

// TestDaemonClientStatusRoundtrip pins GET /status decoding.
func TestDaemonClientStatusRoundtrip(t *testing.T) {
	d := newDaemonFixture(t)
	c := NewDaemonClient(d.sock)
	raw, err := c.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	var s StatusResponse
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s.MachineID != "m1" || s.Auto || s.Queue.MaxConcurrent != 2 {
		t.Errorf("StatusResponse = %+v", s)
	}
	// #113: the workspace SET's data source decodes from the body.
	want := []string{"/srv/pf-ws", "/srv/pf-ws2"}
	if len(s.Workspaces) != 2 || s.Workspaces[0] != want[0] || s.Workspaces[1] != want[1] {
		t.Errorf("StatusResponse.Workspaces = %v, want %v", s.Workspaces, want)
	}
}

// TestStatusSessionColumnsRawDecodeRender pins the v1.5 additive keys
// on the status surface end to end: a RAW GET /status body whose jobs[]
// carry `spawn_n` and `session_fingerprint` (keys the base StatusJob
// bookkeeping predates) decodes into SpawnN / SessionFingerprint and
// renders as the SPAWN cell `#<N>` and the SESSION fingerprint; a job
// whose JSON LACKS the additive keys decodes to zero values and renders
// "-" in both cells (additive decode must not break older bodies).
func TestStatusSessionColumnsRawDecodeRender(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("GET", "/status",
		`{"machine_id":"m1","auto":false,"workspaces":["/srv/pf-ws"],"shutting_down":false,`+
			`"jobs":[`+
			`{"work_item_id":"wi_run","status":"running","spawn_n":3,`+
			`"session_fingerprint":"ab12cd34ef56aa00","agent_pid":42},`+
			`{"work_item_id":"wi_old","status":"needs_input","agent_pid":43}`+
			`],"queue":{"pending":1,"running":1,"max_concurrent":2}}`)

	// Decode path: the raw socket body → StatusResponse carries the
	// additive fields; the job without them reads as zero values.
	c := NewDaemonClient(d.sock)
	raw, err := c.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	var s StatusResponse
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(s.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(s.Jobs))
	}
	if s.Jobs[0].SpawnN != 3 || s.Jobs[0].SessionFingerprint != "ab12cd34ef56aa00" {
		t.Errorf("additive decode: SpawnN=%d SessionFingerprint=%q, want 3 / ab12cd34ef56aa00",
			s.Jobs[0].SpawnN, s.Jobs[0].SessionFingerprint)
	}
	if s.Jobs[1].SpawnN != 0 || s.Jobs[1].SessionFingerprint != "" {
		t.Errorf("body without the additive keys must decode to zero values, got SpawnN=%d SessionFingerprint=%q",
			s.Jobs[1].SpawnN, s.Jobs[1].SessionFingerprint)
	}

	// Render path: exact-output golden for the WORKSPACE line and the
	// SPAWN / SESSION cells.
	var b bytes.Buffer
	renderStatus(&b, &s)
	golden(t, "StatusSessionColumns", b.String(),
		`daemon: m1  auto: off  max: 2  queue: 1 pending / 1 running
model health: no model-health summary from this daemon
workspaces: /srv/pf-ws

WORK ITEM  STATUS       SESSION           SPAWN  AGENT PID  QUESTION  STEP-AGE  LAST-WORK
---------  -----------  ----------------  -----  ---------  --------  --------  ---------
wi_run     running      ab12cd34ef56aa00  #3     42         -         -         -
wi_old     needs_input  -                 -      43         -         -         -
`)

	// Full verb drive: `pf ops status` runs the same raw-JSON → decode
	// → render path against the same socket body.
	code, stdout, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "ops", "status")
	if code != 0 {
		t.Fatalf("ops status: code=%d stderr=%s", code, stderr)
	}
	golden(t, "OpsStatusSessionColumns", stdout, b.String())
}

// TestAnswerWithoutQuestionLeavesResolutionToDaemon: without --question the
// answer verb posts an empty question_id (the daemon resolves the unique
// pending question) and prints the id the daemon names in its reply.
func TestAnswerWithoutQuestionLeavesResolutionToDaemon(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/answer", `{"status":"accepted","question_id":"q_9"}`)

	code, stdout, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock, "PF_USER_ID": "usr_9"},
		"answer", "wi_abc", "--text", "yes")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	got := d.rec.last()
	if got.Path != "/jobs/wi_abc/answer" {
		t.Fatalf("request path = %s", got.Path)
	}
	if !strings.Contains(got.Body, `"question_id":""`) || !strings.Contains(got.Body, `"answered_by":"usr_9"`) {
		t.Errorf("answer body = %s", got.Body)
	}
	if !strings.Contains(stdout, "question q_9") {
		t.Errorf("stdout = %q, want the daemon-resolved question id", stdout)
	}
}

// TestOpsAutoAndMaxVerbs: `pf ops auto on|off` / `pf ops max N` post the
// one switch and the one number to /ops/config; bare forms read /status;
// bad arguments are refused locally.
func TestOpsAutoAndMaxVerbs(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("POST", "/ops/config", `{"auto":true,"max":3}`)
	env := map[string]string{"PF_DAEMON_SOCKET": d.sock}

	for _, c := range []struct {
		args []string
		body string
	}{
		{[]string{"ops", "auto", "on"}, `{"auto":true}`},
		{[]string{"ops", "auto", "off"}, `{"auto":false}`},
		{[]string{"ops", "max", "3"}, `{"max":3}`},
	} {
		if code, _, stderr := runPf(t, env, c.args...); code != 0 {
			t.Fatalf("%v: code=%d stderr=%s", c.args, code, stderr)
		}
		if got := strings.TrimSpace(d.rec.last().Body); d.rec.last().Path != "/ops/config" || got != c.body {
			t.Fatalf("%v posted %s %s, want /ops/config %s", c.args, d.rec.last().Path, got, c.body)
		}
	}
	// Bare forms read /status (fixture: auto off, max 2).
	if _, out, _ := runPf(t, env, "ops", "auto"); strings.TrimSpace(out) != "auto: off" {
		t.Fatalf("ops auto = %q", out)
	}
	if _, out, _ := runPf(t, env, "ops", "max"); strings.TrimSpace(out) != "max: 2" {
		t.Fatalf("ops max = %q", out)
	}
	n := len(d.rec.all())
	for _, bad := range [][]string{{"ops", "auto", "maybe"}, {"ops", "max", "0"}, {"ops", "max", "x"}, {"ops", "max", "--reset"}, {"ops", "mode", "drain"}} {
		if code, _, _ := runPf(t, env, bad...); code == 0 {
			t.Fatalf("%v accepted", bad)
		}
	}
	if len(d.rec.all()) != n {
		t.Fatal("a refused argument reached the daemon")
	}
	if _, _, stderr := runPf(t, env, "ops", "max", "--reset"); !strings.Contains(stderr, "--reset was removed") || !strings.Contains(stderr, "max_concurrent_jobs on daemon restart") {
		t.Fatalf("ops max --reset refusal = %q, want the removal explained", stderr)
	}
}
