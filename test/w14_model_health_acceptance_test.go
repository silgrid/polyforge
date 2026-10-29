package test

// w14_model_health_acceptance_test.go: end-to-end acceptance
// for model health tracking + the per-step model profile, authored against
// artifacts/spec.md BEFORE the implementation lands. The scenarios drive
// the project's real faces only: a real `pf daemon` over the durable
// events log, the raw UDS route, the real `pf` CLI and `pf doctor`; the
// candidate-identity scenario additionally runs a real fakepi dispatch.
//
// Every scenario is RED on the pre-implementation tree at assertion level
// (there is no model-health profile, no /models route, no `ops models`
// action, no `model_health` status object, no model-health doctor row and
// no `candidate` on the driver events).

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// w14Seed writes a deterministic forged events stream into dataDir/events.
// The profile it encodes (all inside the retention window unless named):
//
//	glm-5.3-flash x verification: 6 dispatches (two resume=true), 2 model
//	deaths (model_error, ended_without_result), 2 non-model (tool_hung,
//	an unclassified cause) and 1 excluded (rate_limited); one success.
//	glm-5.3-flash x grill: one more model death, so the model+effort
//	crosses the 3-in-24h threshold.
//	claude-opus-5-5:high x fix: one pre-candidate and one post-candidate
//	failure, reached through two harness spellings -> ONE cell, 2/2.
//	expired-model: a >30-day-old failure that must not survive retention.
//	forged-orphan-model: a step_failed with no preceding step_started; its
//	identity must not be guessed from the body or a neighbour.
func w14Seed(t *testing.T, dataDir string) {
	t.Helper()
	base := time.Now().UTC().Add(-4 * time.Hour)
	files := map[string][]string{}
	seq := map[string]int64{}
	add := func(wi, kind string, at time.Time, body map[string]any) {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		seq[wi]++
		line, err := json.Marshal(protocol.EventEntry{
			At: at.Format(time.RFC3339Nano), Kind: kind, WI: wi, Body: raw, Seq: seq[wi],
		})
		if err != nil {
			t.Fatal(err)
		}
		files[wi] = append(files[wi], string(line))
	}
	start := func(wi, step, model, effort, candidate string, at time.Time, n int, resume bool) {
		b := map[string]any{"step": step, "model": model, "spawn_n": n, "resume": resume}
		if effort != "" {
			b["effort"] = effort
		}
		if candidate != "" {
			b["candidate"] = candidate
		}
		add(wi, "step_started", at, b)
	}
	fail := func(wi, step, cause, candidate string, at time.Time, n int) {
		b := map[string]any{"step": step, "cause": cause, "spawn_n": n}
		if candidate != "" {
			b["candidate"] = candidate
		}
		add(wi, "step_failed", at, b)
	}

	const prof, hist, orphan = "wi_w14_profile", "wi_w14_history", "wi_w14_orphan"
	// > retention: must contribute no count and no cell.
	start(prof, "verification", "expired-model", "", "pi:expired-model", base.Add(-31*24*time.Hour), 1, false)
	fail(prof, "verification", "model_error", "pi:expired-model", base.Add(-31*24*time.Hour+time.Minute), 1)

	// glm-5.3-flash x verification, model+effort key with an empty effort.
	for i := 0; i < 6; i++ {
		start(prof, "verification", "glm-5.3-flash", "", "pi:glm-5.3-flash", base.Add(time.Duration(2*i)*time.Minute), i+1, i > 0)
		switch i {
		case 0:
			fail(prof, "verification", "model_error", "pi:glm-5.3-flash", base.Add(1*time.Minute), i+1)
		case 1:
			fail(prof, "verification", "ended_without_result", "pi:glm-5.3-flash", base.Add(3*time.Minute), i+1)
		case 2:
			add(prof, "step_done", base.Add(5*time.Minute), map[string]any{"step": "verification", "spawn_n": i + 1})
		case 3:
			fail(prof, "verification", "tool_hung", "pi:glm-5.3-flash", base.Add(7*time.Minute), i+1)
		case 4:
			fail(prof, "verification", "rate_limited", "pi:glm-5.3-flash", base.Add(9*time.Minute), i+1)
		case 5:
			fail(prof, "verification", "mystery_cause", "pi:glm-5.3-flash", base.Add(11*time.Minute), i+1)
		}
	}
	// The third model death for this model+effort (different step).
	start(prof, "grill", "glm-5.3-flash", "", "pi:glm-5.3-flash", base.Add(12*time.Minute), 7, false)
	fail(prof, "grill", "model_error", "pi:glm-5.3-flash", base.Add(13*time.Minute), 7)

	// Old-shape (no candidate) + new-shape failures of the same dispatch
	// model, reached through two harness spellings: one cell, no harness key.
	start(hist, "fix", "claude-opus-5-5", "high", "pi:claude-opus-5-5:high", base.Add(14*time.Minute), 1, false)
	fail(hist, "fix", "model_error", "", base.Add(15*time.Minute), 1)
	start(hist, "fix", "claude-opus-5-5", "high", "claude:claude-opus-5-5:high", base.Add(16*time.Minute), 2, false)
	fail(hist, "fix", "model_error", "claude:claude-opus-5-5:high", base.Add(17*time.Minute), 2)

	// No preceding step_started: the identity must not be guessed.
	add(orphan, "step_failed", base.Add(18*time.Minute), map[string]any{
		"step": "fix", "cause": "model_error", "spawn_n": 1,
		"model": "forged-orphan-model", "effort": "high",
	})

	dir := filepath.Join(dataDir, "events")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for wi, lines := range files {
		if err := os.WriteFile(filepath.Join(dir, "wi-"+wi+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func w14Daemon(t *testing.T, inst *v1Install) *v1Daemon {
	t.Helper()
	d := newV1Daemon(t, inst)
	w14Seed(t, d.dataDir)
	d.start()
	return d
}

// w14Raw speaks one request over the daemon's UDS face and answers status+body.
func w14Raw(t *testing.T, sock, method, path string) (int, string) {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	c := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), method, "http://daemon"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// w14PF runs the real pf binary against sock and answers stdout+exit code.
func w14PF(t *testing.T, inst *v1Install, sock string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(inst.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(),
		"PF_SERVER="+inst.url, "PF_API_KEY="+inst.adminKey,
		"PF_USER_ID=usr_root", "PF_DAEMON_SOCKET="+sock)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), 0
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("pf %v: %v", args, err)
	}
	return stdout.String(), ee.ExitCode()
}

var w14Stamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z`)
var w14Epoch = regexp.MustCompile(`\b1[0-9]{9,18}\b`)

func w14Volatile(s string) string {
	s = w14Stamp.ReplaceAllString(s, "<ts>")
	return w14Epoch.ReplaceAllString(s, "<epoch>")
}

func w14WaitFile(t *testing.T, path string, bound time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 && json.Valid(raw) {
			return raw
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s never became a complete JSON document", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestW14ProfileTableAndAttribution pins the profile's semantics through
// `pf ops models`: model+effort keys (no harness), the three attribution
// tiers, the events-only denominator, the retention window and the
// explicit window statement. The forged stream also carries a failure
// with no preceding start and one beyond retention.
func TestW14ProfileTableAndAttribution(t *testing.T) {
	inst := newV1Install(t)
	d := w14Daemon(t, inst)
	out := d.pf(t, "ops", "models")
	low := strings.ToLower(out)
	for _, want := range []string{"glm-5.3-flash", "verification", "mystery_cause", "window"} {
		if !strings.Contains(low, strings.ToLower(want)) {
			t.Fatalf("pf ops models must name %q (the profile table + its window); output:\n%s", want, out)
		}
	}
	if !regexp.MustCompile(`non[-_ ]?model`).MatchString(low) {
		t.Fatalf("the table must show non-model deaths in their own place beside the rate:\n%s", out)
	}
	// 2 model-tier deaths over 6 dispatches = 33.3%; a model/non-model/
	// excluded mis-count would move it to 50% or 66.7%.
	if !regexp.MustCompile(`33(\.3)?%`).MatchString(out) {
		t.Fatalf("glm-5.3-flash x verification must be 2 model deaths / 6 dispatches (33.3%%); output:\n%s", out)
	}
	if strings.Contains(out, "rate_limited") {
		t.Fatalf("rate_limited is excluded and must appear in neither death column:\n%s", out)
	}
	if strings.Contains(out, "expired-model") {
		t.Fatalf("an event older than the retention window leaked into the profile:\n%s", out)
	}
	if strings.Contains(out, "forged-orphan-model") {
		t.Fatalf("a failure with no preceding step_started was attributed by guessing:\n%s", out)
	}
	if strings.Contains(out, "pi:") || strings.Contains(out, "claude:") {
		t.Fatalf("the profile key must be model+effort, never harness:candidate:\n%s", out)
	}
	if n := strings.Count(out, "claude-opus-5-5"); n != 1 {
		t.Fatalf("the two harness spellings of claude-opus-5-5:high must land in ONE cell, saw %d rows:\n%s", n, out)
	}
}

// TestW14ModelsRouteReadOnly pins GET /models beside GET /status: a local
// peer reads the profile JSON, a non-GET is refused, the payload carries
// no secrets/paths/work-item content, and the read still works with aihub
// gone (daemon-local only).
func TestW14ModelsRouteReadOnly(t *testing.T) {
	inst := newV1Install(t)
	d := w14Daemon(t, inst)

	code, body := w14Raw(t, d.sock, "GET", "/models")
	if code != http.StatusOK {
		t.Fatalf("GET /models = %d, want 200 with the profile; body: %s", code, body)
	}
	for _, want := range []string{"glm-5.3-flash", "window"} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /models payload must carry %q; body: %s", want, body)
		}
	}
	for _, leak := range []string{d.dataDir, d.workspace, "transcript", "prompt", "api_key", "token"} {
		if leak != "" && strings.Contains(body, leak) {
			t.Fatalf("GET /models payload leaked %q; body: %s", leak, body)
		}
	}
	if code, _ := w14Raw(t, d.sock, "POST", "/models"); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /models = %d, want 405 (the route is GET-only)", code)
	}
	// --json re-emits the server document unchanged.
	rawCLI := d.pf(t, "--json", "ops", "models")
	var fromRoute, fromCLI any
	if err := json.Unmarshal([]byte(body), &fromRoute); err != nil {
		t.Fatalf("route body is not JSON: %v (%s)", err, body)
	}
	if err := json.Unmarshal([]byte(rawCLI), &fromCLI); err != nil {
		t.Fatalf("pf --json ops models is not JSON: %v (%s)", err, rawCLI)
	}
	if !reflect.DeepEqual(fromRoute, fromCLI) {
		t.Fatalf("pf --json ops models must re-emit the route document unchanged; route=%v cli=%v", fromRoute, fromCLI)
	}
	// No aihub call in either path: stop the hub and read again.
	inst.stopAihub()
	if code, body := w14Raw(t, d.sock, "GET", "/models"); code != http.StatusOK {
		t.Fatalf("GET /models after aihub stopped = %d, want 200 (daemon-local only); body: %s", code, body)
	}
}

// TestW14ModelHealthFileRebuild pins the persistence contract: the daemon
// writes <DataDir>/model-health.json, and deleting it while stopped loses
// nothing because the next start rebuilds the same profile from events/.
func TestW14ModelHealthFileRebuild(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	w14Seed(t, d.dataDir)
	d.start()
	path := filepath.Join(d.dataDir, "model-health.json")
	first := w14WaitFile(t, path, 20*time.Second)
	if !strings.Contains(string(first), "glm-5.3-flash") || !strings.Contains(string(first), "window") {
		t.Fatalf("model-health.json must carry the profile cells and its window; content: %s", first)
	}

	d.stop()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("model-health.json was not deleted: %v", err)
	}
	d.start()
	second := w14WaitFile(t, path, 20*time.Second)
	if a, b := w14Volatile(string(first)), w14Volatile(string(second)); a != b {
		t.Fatalf("the rebuilt model-health.json differs from the deleted one for the same events:\nfirst:  %s\nsecond: %s", a, b)
	}
}

// TestW14OpsStatusModelHealthSummary pins the operator's first-look face:
// current warnings are listed with the pointer to `pf ops models`, and no
// warnings (including no data yet) is stated explicitly.
func TestW14OpsStatusModelHealthSummary(t *testing.T) {
	inst := newV1Install(t)
	warn := w14Daemon(t, inst)
	out := warn.pf(t, "ops", "status")
	for _, want := range []string{"glm-5.3-flash", "pf ops models"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pf ops status with an active warning must name %q and point at `pf ops models`; output:\n%s", want, out)
		}
	}
	if code, body := w14Raw(t, warn.sock, "GET", "/status"); code != http.StatusOK || !strings.Contains(body, `"model_health"`) {
		t.Fatalf("GET /status must carry the additive model_health object; code=%d body: %s", code, body)
	}

	clean := newV1Daemon(t, inst)
	clean.start()
	out = clean.pf(t, "ops", "status")
	low := strings.ToLower(out)
	if !strings.Contains(low, "no model") || !strings.Contains(low, "warning") {
		t.Fatalf("pf ops status with no warnings must say so explicitly (not omit the block); output:\n%s", out)
	}
}

// TestW14ModelUnhealthyEdgeTriggered pins the two constant rules and the
// edge trigger: the third model death in 24h writes exactly one
// daemon-scope model_unhealthy, 1-of-4 (25%) does not warn, and a restart
// over the same events does not duplicate the event.
func TestW14ModelUnhealthyEdgeTriggered(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	w14Seed(t, d.dataDir)
	// A cell with 4 dispatches and 1 model death (25%): the 7-day rule
	// needs >=5 dispatches, so it must stay quiet.
	base := time.Now().UTC().Add(-time.Hour)
	var lines []string
	seq := int64(0)
	addTiny := func(kind string, body map[string]any, at time.Time) {
		raw, _ := json.Marshal(body)
		seq++
		line, _ := json.Marshal(protocol.EventEntry{At: at.Format(time.RFC3339Nano), Kind: kind,
			WI: "wi_w14_tiny", Body: raw, Seq: seq})
		lines = append(lines, string(line))
	}
	for i := 0; i < 4; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		addTiny("step_started", map[string]any{"step": "code", "model": "tiny-model", "spawn_n": i + 1,
			"candidate": "pi:tiny-model", "resume": i > 0}, at)
		if i == 0 {
			addTiny("step_failed", map[string]any{"step": "code", "spawn_n": 1,
				"cause": "model_error", "candidate": "pi:tiny-model"}, at)
		} else {
			addTiny("step_done", map[string]any{"step": "code", "spawn_n": i + 1}, at)
		}
	}
	if err := os.WriteFile(filepath.Join(d.dataDir, "events", "wi-wi_w14_tiny.jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.start()

	count := func() int {
		n := 0
		for _, e := range daemonEvents(t, d) {
			if e.Kind == "model_unhealthy" {
				n++
			}
		}
		return n
	}
	waitUntil(t, 15*time.Second, "no model_unhealthy event for the 3-in-24h threshold", func() bool { return count() == 1 })
	for _, e := range daemonEvents(t, d) {
		if e.Kind != "model_unhealthy" {
			continue
		}
		if e.WI != "" {
			t.Fatalf("model_unhealthy must be daemon-scope (wi=\"\"), got wi=%q", e.WI)
		}
		if !strings.Contains(string(e.Body), "glm-5.3-flash") {
			t.Fatalf("model_unhealthy must name the model+effort; body: %s", e.Body)
		}
		if strings.Contains(string(e.Body), "tiny-model") {
			t.Fatalf("1 of 4 (25%%) must not warn (the 7-day rule needs >=5 dispatches); body: %s", e.Body)
		}
	}
	time.Sleep(time.Second)
	if n := count(); n != 1 {
		t.Fatalf("model_unhealthy fired %d times, want exactly one per episode", n)
	}
	d.stop()
	d.start()
	time.Sleep(time.Second)
	if n := count(); n != 1 {
		t.Fatalf("a restart over the same still-unhealthy events duplicated model_unhealthy (%d events)", n)
	}
}

// TestW14DoctorModelHealthRow pins the warn-at-most doctor row: warn does
// not move the verdict/exit code, a down daemon is skip with the existing
// remediation, never fail, and --harness does not run the row.
func TestW14DoctorModelHealthRow(t *testing.T) {
	type report struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	row := func(rep report) (string, string, bool) {
		for _, c := range rep.Checks {
			n := strings.ToLower(strings.ReplaceAll(c.Name, "-", " "))
			if strings.Contains(n, "model health") || strings.Contains(n, "modelhealth") {
				return c.Status, c.Detail, true
			}
		}
		return "", "", false
	}
	inst := newV1Install(t)
	warn := w14Daemon(t, inst)
	clean := newV1Daemon(t, inst)
	clean.start()

	rawWarn, codeWarn := w14PF(t, inst, warn.sock, "--json", "doctor")
	var repWarn, repClean report
	if err := json.Unmarshal([]byte(rawWarn), &repWarn); err != nil {
		t.Fatalf("pf --json doctor output undecodable: %v (%s)", err, rawWarn)
	}
	rawClean, codeClean := w14PF(t, inst, clean.sock, "--json", "doctor")
	if err := json.Unmarshal([]byte(rawClean), &repClean); err != nil {
		t.Fatalf("pf --json doctor (clean daemon) output undecodable: %v (%s)", err, rawClean)
	}
	status, detail, ok := row(repWarn)
	if !ok {
		t.Fatalf("pf doctor must carry the additive model health row; checks: %+v", repWarn.Checks)
	}
	if status != "warn" || !strings.Contains(detail, "glm-5.3-flash") {
		t.Fatalf("an unhealthy model must make the row warn naming it; got %q %q", status, detail)
	}
	if repWarn.OK != repClean.OK || codeWarn != codeClean {
		t.Fatalf("the warn row moved the overall verdict/exit code: warning ok=%v exit=%d, clean ok=%v exit=%d",
			repWarn.OK, codeWarn, repClean.OK, codeClean)
	}
	rawHarness, _ := w14PF(t, inst, warn.sock, "--json", "doctor", "--harness")
	var repHarness report
	if err := json.Unmarshal([]byte(rawHarness), &repHarness); err != nil {
		t.Fatalf("pf --json doctor --harness undecodable: %v (%s)", err, rawHarness)
	}
	if _, _, ok := row(repHarness); ok {
		t.Fatalf("the model health row must not appear under --harness: %+v", repHarness.Checks)
	}
	warn.stop()
	rawDown, _ := w14PF(t, inst, warn.sock, "--json", "doctor")
	var repDown report
	if err := json.Unmarshal([]byte(rawDown), &repDown); err != nil {
		t.Fatalf("pf --json doctor (daemon down) undecodable: %v (%s)", err, rawDown)
	}
	status, detail, ok = row(repDown)
	if !ok || status != "skip" || !strings.Contains(detail, "pf daemon ensure") {
		t.Fatalf("with the daemon down the row must be skip with the `pf daemon ensure` remediation; got %q %q (found=%v)", status, detail, ok)
	}
	if status == "fail" {
		t.Fatalf("the model health row must never fail")
	}
}

// TestW14EventCandidateIdentity pins the event self-description: the start
// and its failure carry the same dispatched candidate; step_dead keeps
// today's body (no candidate). A real fakepi dispatch produces both.
func TestW14EventCandidateIdentity(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "w14: candidate identity on start and failure",
		plan([]map[string]any{
			{"do": "envelope", "id": "grill", "error_turn": "provider error: 400 invalid_request_error: messages must alternate"},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		}), nil)
	d := newV1Daemon(t, inst, optSupervision(supBounds{modelErrorGrace: 2 * time.Second}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	waitForEvent(t, d, wi, "step_dead", 60*time.Second)

	var startBody, failBody, deadBody map[string]any
	for _, e := range d.eventsOf(t, wi) {
		var body map[string]any
		_ = json.Unmarshal(e.Body, &body)
		switch {
		case e.Kind == "step_started" && startBody == nil && body["step"] == "grill":
			startBody = body
		case e.Kind == "step_failed" && failBody == nil && body["step"] == "grill":
			failBody = body
		case e.Kind == "step_dead" && deadBody == nil && body["step"] == "grill":
			deadBody = body
		}
	}
	if startBody == nil || failBody == nil || deadBody == nil {
		t.Fatalf("missing events: start=%v fail=%v dead=%v", startBody, failBody, deadBody)
	}
	cand, _ := startBody["candidate"].(string)
	if cand == "" {
		t.Fatalf("step_started must carry the dispatch's candidate; body: %v", startBody)
	}
	if got, _ := failBody["candidate"].(string); got != cand {
		t.Fatalf("step_failed candidate %q, want the failing dispatch's %q (not its successor); body: %v", got, cand, failBody)
	}
	if _, ok := deadBody["candidate"]; ok {
		t.Fatalf("step_dead must keep today's shape (no candidate); body: %v", deadBody)
	}
}

// TestW14WarningDoesNotReorderDispatch pins the advisory boundary: with
// an active warning for the list head, the dispatched model is still the
// owner's list head — no reordering, pinning or skipping.
func TestW14WarningDoesNotReorderDispatch(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	w14Seed(t, d.dataDir)
	if err := os.WriteFile(filepath.Join(d.workspace, ".polyforge.yaml"), []byte(
		"steps:\n  e2e:\n"+
			"    grill: { models: [pi:glm-5.3-flash, pi:claude-opus-5-5:high] }\n"+
			"    fix: { models: [pi:glm-5.3-flash, pi:claude-opus-5-5:high] }\n"+
			"    verification: { models: [pi:glm-5.3-flash, pi:claude-opus-5-5:high] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.start()
	if out := d.pf(t, "ops", "models"); !strings.Contains(out, "glm-5.3-flash") {
		t.Fatalf("the warning profile must be live before the dispatch; output:\n%s", out)
	}
	wi := inst.newWI(t, "w14: warning does not reorder dispatch", envelopeAutopilot(), nil)
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	capDir := d.capsuleDir(wi)
	w33WaitModels(t, capDir, 1, 90*time.Second)
	if got := w33Models(t, capDir)[0]; got != "glm-5.3-flash" {
		t.Fatalf("first dispatch model %q, want the owner's list head glm-5.3-flash despite its warning", got)
	}
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
}
