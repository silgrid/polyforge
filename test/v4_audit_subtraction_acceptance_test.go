package test

// v4_audit_subtraction_acceptance_test.go: the e2e acceptance checks of
// the audit-subtraction passes (polyforge#147 pass A, polyforge#148 pass
// B) that no unit or other e2e test already covers, driven through the
// real pf/aihub/fakepi binaries. The rest of both contracts is pinned
// by the package-level tests (driver, supervise, runtime, daemon, cli,
// watcher) and the v1/v2 e2e suites.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v4PostDaemon issues one raw HTTP request against the daemon's UDS
// face and returns the status and the body verbatim — the wire surface
// `pf` itself speaks, so error-envelope scenarios see exactly what a
// client sees.
func v4PostDaemon(t *testing.T, sock, method, path, body string) (int, string) {
	t.Helper()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}
	c := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), method, "http://daemon"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return resp.StatusCode, string(raw)
}

// v4ErrCode decodes a `{code, detail}` envelope body and asserts the
// standard shape: a non-empty code and a non-empty detail.
func v4ErrCode(t *testing.T, body string) (code, detail string) {
	t.Helper()
	var e struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("body is not a JSON envelope: %v (%q)", err, body)
	}
	if e.Code == "" || e.Detail == "" {
		t.Fatalf("envelope missing code/detail: %q", body)
	}
	return e.Code, e.Detail
}

// TestV4AnswerRouteErrorsUseStandardEnvelope (D12): a failing POST
// /jobs/{wi}/answer on a LIVE driver bridge answers the standard
// top-level {code, detail} DAEMON-* envelope with the right HTTP status
// for the two arms the daemon unit tests cannot reach with a real
// bridge: an undecodable body (400) and a question id the bridge does
// not hold pending (409). The not-running (404), no-bridge (409) and
// no-pending-question (400) arms are pinned by
// internal/polyforge/daemon/answer_failfast_test.go.
func TestV4AnswerRouteErrorsUseStandardEnvelope(t *testing.T) {
	inst := newV1Install(t)
	const qid = "q_v4ans"
	wi := inst.newWI(t, "v4 d12: answer route standard error envelope",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": "v4: park for the answer-route arms."},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	t.Cleanup(func() { killOrphans(wi) })
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	waitQuestion(t, d, wi, qid, 60*time.Second)

	// arm 1: an undecodable body — 400, DAEMON-* envelope
	status, body := v4PostDaemon(t, d.sock, "POST", "/jobs/"+wi+"/answer", "{not json")
	if status != 400 {
		t.Fatalf("decode-error arm: HTTP %d, want 400 (body %q)", status, body)
	}
	if code, detail := v4ErrCode(t, body); !strings.HasPrefix(code, "DAEMON-") {
		t.Fatalf("decode-error arm: code %q (detail %q), want a DAEMON-* code — body %q", code, detail, body)
	}

	// arm 2: a question id that is not pending — 409, DAEMON-* envelope
	status, body = v4PostDaemon(t, d.sock, "POST", "/jobs/"+wi+"/answer",
		`{"question_id":"q_v4nope","answer_text":"x","answered_by":"usr_root"}`)
	if status != 409 {
		t.Fatalf("unknown-question arm: HTTP %d, want 409 (body %q)", status, body)
	}
	if code, detail := v4ErrCode(t, body); !strings.HasPrefix(code, "DAEMON-") {
		t.Fatalf("unknown-question arm: code %q (detail %q), want a DAEMON-* code — body %q", code, detail, body)
	}
}

// TestV4StepAgentDedicatedAgentDirectory (addendum 2): a step process
// spawned by the REAL daemon runs with PI_CODING_AGENT_DIR pointing at
// an existing directory under the daemon data dir (never under the
// capsule workspace) that declares no packages, even though the
// user-level settings under HOME declare a background-task package.
// The runtime unit tests (spawn_test.go, stepagentdir_test.go) pin the
// env construction and the snapshot; this drive pins the daemon's
// wiring end to end (flag/config -> start-time sync -> child env).
func TestV4StepAgentDedicatedAgentDirectory(t *testing.T) {
	inst := newV1Install(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	agentUserDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agentUserDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentUserDir, "settings.json"),
		[]byte(`{"packages":["npm:pi-background-tasks"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	const qid = "q_v4iso"
	wi := inst.newWI(t, "v4 iso: dedicated step agent directory",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": "v4: park so the spawn ledger is inspectable."},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	t.Cleanup(func() { killOrphans(wi) })

	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	waitQuestion(t, d, wi, qid, 90*time.Second)

	var agentDir string
	for _, e := range fakeJournal(t, d.capsuleDir(wi)) {
		if k, _ := e["type"].(string); k == "spawn" {
			agentDir, _ = e["agent_dir"].(string)
		}
	}
	if agentDir == "" {
		t.Fatalf("the step process was not given an agent directory override (PI_CODING_AGENT_DIR absent from its environment)")
	}
	if rel, rerr := filepath.Rel(d.dataDir, agentDir); rerr != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("the dedicated agent directory %s is not under the daemon data dir %s", agentDir, d.dataDir)
	}
	if rel, rerr := filepath.Rel(d.workspace, agentDir); rerr == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("the dedicated agent directory %s lives under the capsule workspace %s", agentDir, d.workspace)
	}
	if st, serr := os.Stat(agentDir); serr != nil || !st.IsDir() {
		t.Fatalf("the dedicated agent directory does not exist at %s: %v", agentDir, serr)
	}
	if _, serr := os.Stat(filepath.Join(agentDir, "settings.json")); !os.IsNotExist(serr) {
		t.Fatalf("the dedicated agent directory carries a settings.json (packages would load): %v", serr)
	}
}

// TestV4NeedsInputWaitNotCountedIntoT: the wall time a step spends
// PARKED on an owner question must not enter the step's cumulative
// runtime T (T counts agent running time only).
func TestV4NeedsInputWaitNotCountedIntoT(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "v4: the needs_input wait is not agent runtime", plan([]map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "needs_input",
			"question_id": "q_v4wait", "question": "should the wait count into T?"},
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
	}), nil)
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	capDir := d.capsuleDir(wi)

	waitQuestion(t, d, wi, "q_v4wait", 60*time.Second)
	// The owner takes their time: a wait far longer than either of the
	// step's two agent dispatches.
	time.Sleep(6 * time.Second)
	answerQuestion(t, d, wi, "q_v4wait", "No — only agent runtime counts.")
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)

	x := readExecution(t, capDir)
	wall, _ := x.Steps["grill"].BudgetConsumed["wall_ms"].(float64)
	if wall >= 5000 {
		t.Fatalf("SYMPTOM: grill booked %vms of wall — the 6s owner wait leaked into T (agent runtime is ~2s)", wall)
	}
}

// TestV4BundleFeatureFullLaneWraps: the shipped feature-full bundle
// (grill -> spec -> grill-review -> plan -> e2e-author -> code ->
// review -> verification; the harness strips deliver;'s
// order) runs end to end through
// the real daemon and wraps — the only e2e run of that lane.
func TestV4BundleFeatureFullLaneWraps(t *testing.T) {
	inst := newV1Install(t)
	actions := []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "spec", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "grill-review", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "e2e-author", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "review", "outcome": "completed"},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	}
	wi := newWIWithBundle(t, inst, "v4: feature-full lane end to end", "feature-full", actions)
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	inst.waitWIStatus(t, wi, "wrapped", 240*time.Second)
	x := readExecution(t, d.capsuleDir(wi))
	if x.WorkflowStatus != "completed" {
		t.Fatalf("workflow status %q, want completed", x.WorkflowStatus)
	}
	for _, step := range []string{"grill", "spec", "grill-review", "plan", "e2e-author", "code", "review", "verification"} {
		if st, ok := x.Steps[step]; !ok || st.Status != "done" {
			t.Fatalf("step %s not done: %+v", step, st)
		}
	}
}
