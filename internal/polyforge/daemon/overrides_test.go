package daemon

// overrides_test.go (u4, polyforge#25): the step-overlay face over the
// real UDS daemon — the write's validation refusals (candidate shape,
// the step-agent models.json membership for pi, the unknown step, the
// ambiguous action), the live-dispatch refusal, the non-skippable skip
// refusal naming the waive door, the wrap-attrs guard, the successful
// write (file + event + chain commit), the idempotent clear, the paused
// scene write (the frozen current step accepts a model chain), the
// blocked pending record (the sealed scene is not written), the view's
// per-step effective source, and the step-origin refusal (DAEMON-403).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// ovLane: build → review (gate) → merge (skippable tail, wrap attr).
const ovLane = `max_rewinds: 3
steps:
  - id: build
  - id: review
    gate: true
    on_fail: { target: build }
  - id: merge
    skippable: true
    wrap_attrs: [rec]
`

// ovFixture: a live-claimed work item whose capsule carries ovLane, an
// execution record (build in_progress — the current step —, review and
// merge pending) and a step-agent models.json (provider ovtest, models
// m-ok and m-two) synced from an explicit operator agent source.
type ovFixture struct {
	td     *testDaemon
	wi     string
	capDir string
}

func (f *ovFixture) writeExec(t *testing.T, steps map[string]*capsule.Step, current string) {
	t.Helper()
	raw, err := json.Marshal(capsule.Execution{WorkItemID: f.wi, WorkflowStatus: capsule.WorkflowStatusRunning,
		CurrentStep: current, Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.capDir, capsule.ExecutionJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newOverrideFixture(t *testing.T, wi string) *ovFixture {
	t.Helper()
	src := t.TempDir()
	modelsJSON := `{"providers":{"ovtest":{"baseUrl":"http://localhost:1","api":"openai-completions","models":[{"id":"m-ok"},{"id":"m-two"}]}}}`
	if err := os.WriteFile(filepath.Join(src, "models.json"), []byte(modelsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	td := newTestDaemon(t, func(c *Config) { c.OperatorAgentDir = src })
	td.submitClaim(t, wi)
	ji, _ := td.rt.Info(wi)
	f := &ovFixture{td: td, wi: wi, capDir: ji.CapsuleDir}
	// the lane lands under the bundle name the claim pinned
	workflowtest.WriteLane(t, filepath.Join(f.capDir, capsule.DirWorkflows), ji.BundleName, ovLane)
	idn := capsule.Identity{WorkItemID: wi, Project: "proj", BundleName: ji.BundleName}
	raw, _ := json.Marshal(idn)
	if err := os.WriteFile(filepath.Join(f.capDir, capsule.CapsuleJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	f.writeExec(t, map[string]*capsule.Step{
		"build":  {Status: capsule.StepInProgress, Round: 1, Candidate: "pi:ovtest/m-build"},
		"review": {Status: capsule.StepPending},
		"merge":  {Status: capsule.StepPending},
	}, "build")
	return f
}

// ovClient dials the daemon's UDS face directly (the CLI verb is a later
// unit; the route is the class under test here).
func (td *testDaemon) ovDo(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", td.sock)
		}}, Timeout: 30 * time.Second}
	defer c.CloseIdleConnections()
	var rd io.Reader
	if body != nil {
		blob, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(blob)
	}
	req, err := http.NewRequest(method, "http://daemon"+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}

func (td *testDaemon) ovPost(t *testing.T, wi string, req protocol.OverrideWriteRequest) (*protocol.OverrideWriteResult, *api.Error) {
	t.Helper()
	code, raw := td.ovDo(t, "POST", "/jobs/"+wi+"/overrides", req)
	return ovDecodeWrite(t, code, raw)
}

func ovDecodeWrite(t *testing.T, code int, raw []byte) (*protocol.OverrideWriteResult, *api.Error) {
	t.Helper()
	if code == http.StatusOK {
		var res protocol.OverrideWriteResult
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatalf("write answer undecodable: %v (%s)", err, raw)
		}
		return &res, nil
	}
	var ae api.Error
	if err := json.Unmarshal(raw, &ae); err != nil || ae.Code == "" {
		t.Fatalf("refusal %d undecodable as api.Error: %s", code, raw)
	}
	return nil, &ae
}

func (td *testDaemon) ovView(t *testing.T, wi string) (*protocol.OverrideView, *api.Error) {
	t.Helper()
	code, raw := td.ovDo(t, "GET", "/jobs/"+wi+"/overrides", nil)
	if code == http.StatusOK {
		var v protocol.OverrideView
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("view undecodable: %v (%s)", err, raw)
		}
		return &v, nil
	}
	var ae api.Error
	if err := json.Unmarshal(raw, &ae); err != nil || ae.Code == "" {
		t.Fatalf("view refusal %d undecodable: %s", code, raw)
	}
	return nil, &ae
}

func (f *ovFixture) overlayFile() string {
	return filepath.Join(f.capDir, filepath.FromSlash(capsule.StepOverridesJSON))
}

func (f *ovFixture) readOverlay(t *testing.T) capsule.StepOverrides {
	t.Helper()
	ov, err := capsule.ReadStepOverrides(f.capDir)
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	return ov
}

func (f *ovFixture) events(t *testing.T) []protocol.EventEntry {
	t.Helper()
	return f.td.obs.Events().SinceAfter(f.wi, 0)
}

func (f *ovFixture) gitLog(t *testing.T) string {
	t.Helper()
	ji, _ := f.td.rt.Info(f.wi)
	out, err := exec.Command("git", "--git-dir", ji.Git.GitDir, "log", "--format=%s", "-10").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	return string(out)
}

// The write's validation refusals: every one leaves no overlay file.
func TestOverrideWriteValidation(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov4a")
	for _, tc := range []struct {
		name string
		req  protocol.OverrideWriteRequest
		want string
	}{
		{"missing prefix", protocol.OverrideWriteRequest{Step: "review", Models: []string{"m-ok"}}, "needs a harness prefix"},
		{"unknown harness", protocol.OverrideWriteRequest{Step: "review", Models: []string{"codex:m"}}, "unknown harness prefix"},
		{"effort the harness refuses", protocol.OverrideWriteRequest{Step: "review", Models: []string{"claude:claude-x:off"}}, "is not accepted by claude"},
		{"pi model outside models.json", protocol.OverrideWriteRequest{Step: "review", Models: []string{"pi:ovtest/absent"}}, "models.json"},
		{"pi model of another provider", protocol.OverrideWriteRequest{Step: "review", Models: []string{"pi:other/m-ok"}}, "models.json"},
		{"unknown step", protocol.OverrideWriteRequest{Step: "nosuch", Models: []string{"pi:ovtest/m-ok"}}, "declares no step"},
		{"no action", protocol.OverrideWriteRequest{Step: "review"}, "exactly one action"},
		{"two actions", protocol.OverrideWriteRequest{Step: "review", Models: []string{"pi:ovtest/m-ok"}, ClearModels: true}, "exactly one action"},
	} {
		_, ae := f.td.ovPost(t, f.wi, tc.req)
		if ae == nil {
			t.Fatalf("%s: accepted %+v", tc.name, tc.req)
		}
		if ae.Code != api.ErrDaemonBadInput {
			t.Errorf("%s: code %s, want %s (%s)", tc.name, ae.Code, api.ErrDaemonBadInput, ae.Detail)
		}
		if !strings.Contains(ae.Detail, tc.want) {
			t.Errorf("%s: detail %q does not name %q", tc.name, ae.Detail, tc.want)
		}
		if _, err := os.Stat(f.overlayFile()); !os.IsNotExist(err) {
			t.Fatalf("%s: a refused write left an overlay file behind", tc.name)
		}
	}
	// the models.json membership accepts the provider-qualified form and
	// the globally unique bare id alike
	for _, ok := range []string{"pi:ovtest/m-ok", "pi:m-ok", "claude:any-model"} {
		res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_1", Models: []string{ok}})
		if ae != nil {
			t.Fatalf("%q must be accepted: %s (%s)", ok, ae.Code, ae.Detail)
		}
		if !res.Changed || res.Posture != "running" {
			t.Fatalf("%q: %+v", ok, res)
		}
	}
}

// A step with a live dispatch is refused; the parked postures' frozen
// current step is not (see the paused test below).
func TestOverrideWriteRunningStepRefused(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov4b")
	_, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "build", Models: []string{"pi:ovtest/m-ok"}})
	if ae == nil {
		t.Fatal("the live current step accepted a model chain")
	}
	if ae.Code != api.ErrDaemonConflict {
		t.Fatalf("code %s, want DAEMON-409 (%s)", ae.Code, ae.Detail)
	}
	if !strings.Contains(ae.Detail, "live dispatch") {
		t.Fatalf("the refusal must name the live dispatch: %s", ae.Detail)
	}
	if _, err := os.Stat(f.overlayFile()); !os.IsNotExist(err) {
		t.Fatal("a refused write left an overlay file behind")
	}
}

// The skip switch needs the lane's skippable declaration; the refusal
// names the waive door, and a skip of the wrap-attrs tail after
// everything before it finished is refused (its wrap attr would strand).
func TestOverrideSkipNonSkippableNamesWaive(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov4c")
	yes := true
	_, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Skip: &yes})
	if ae == nil {
		t.Fatal("a non-skippable step accepted the skip switch")
	}
	if ae.Code != api.ErrDaemonConflict {
		t.Fatalf("code %s, want DAEMON-409", ae.Code)
	}
	if !strings.Contains(ae.Detail, "waive") {
		t.Fatalf("the refusal must point at the waive door: %s", ae.Detail)
	}
	// the tail's wrap-attrs guard: review done, build done — skipping
	// merge now would strand its wrap attr
	f.writeExec(t, map[string]*capsule.Step{
		"build":  {Status: capsule.StepDone, Round: 1},
		"review": {Status: capsule.StepDone, Round: 1},
		"merge":  {Status: capsule.StepPending},
	}, "")
	_, ae = f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "merge", Skip: &yes})
	if ae == nil {
		t.Fatal("skipping the wrap-attrs tail after its predecessors finished accepted the switch")
	}
	if !strings.Contains(ae.Detail, "wrap") {
		t.Fatalf("the guard's refusal must name the wrap attr: %s", ae.Detail)
	}
	// while a predecessor still runs the same skip is fine
	f.writeExec(t, map[string]*capsule.Step{
		"build":  {Status: capsule.StepDone, Round: 1},
		"review": {Status: capsule.StepInProgress, Round: 1},
		"merge":  {Status: capsule.StepPending},
	}, "review")
	if res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "merge", Operator: "usr_2", Skip: &yes}); ae != nil {
		t.Fatalf("skipping merge while review runs must be accepted: %s (%s)", ae.Code, ae.Detail)
	} else if !res.Changed {
		t.Fatalf("the skip must change the record: %+v", res)
	}
	ov := f.readOverlay(t)
	if ov.Steps["merge"] == nil || ov.Steps["merge"].Skip == nil || !*ov.Steps["merge"].Skip || ov.Steps["merge"].SkipBy != "usr_2" {
		t.Fatalf("the overlay after the skip: %+v", ov.Steps["merge"])
	}
}

// The successful write: the overlay file, the event (operator, step, new
// value) and the immediate chain commit+push; a clear of nothing is the
// idempotent no-op.
func TestOverrideWriteAppendsEventAndPublishes(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov4d")
	res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{
		Step: "merge", Operator: "usr_9", Models: []string{"pi:ovtest/m-ok", "pi:ovtest/m-two"},
	})
	if ae != nil {
		t.Fatalf("the write was refused: %s (%s)", ae.Code, ae.Detail)
	}
	if !res.Changed || res.Posture != "running" || !res.Published {
		t.Fatalf("the write's answer: %+v", res)
	}
	ov := f.readOverlay(t)
	if m := ov.Steps["merge"]; m == nil || len(m.Models) != 2 || m.Models[0] != "pi:ovtest/m-ok" || m.ModelsBy != "usr_9" || m.ModelsAt == "" {
		t.Fatalf("the overlay after the write: %+v", ov.Steps["merge"])
	}
	// the event carries the operator, the step and the new value
	var found bool
	for _, e := range f.events(t) {
		if e.Kind != "override_set" {
			continue
		}
		var body struct {
			Step   string   `json:"step"`
			Aspect string   `json:"aspect"`
			Value  string   `json:"value"`
			By     string   `json:"operator"`
			Models []string `json:"models"`
		}
		if err := json.Unmarshal(e.Body, &body); err != nil {
			t.Fatalf("event body: %v", err)
		}
		if body.Step == "merge" && body.By == "usr_9" && body.Aspect == "models" && strings.Join(body.Models, ",") == body.Value {
			found = true
		}
	}
	if !found {
		t.Fatalf("no override_set event carries {step: merge, operator: usr_9, the new chain}: %v", f.events(t))
	}
	// the write's chain commit is in the capsule's history
	if log := f.gitLog(t); !strings.Contains(log, "step override") {
		t.Fatalf("the write's commit is not in the capsule chain's history:\n%s", log)
	}

	// the idempotent clear: nothing to clear, no change, no event
	before := len(f.events(t))
	res, ae = f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "review", Operator: "usr_9", ClearModels: true})
	if ae != nil {
		t.Fatalf("the idempotent clear was refused: %s", ae.Detail)
	}
	if res.Changed {
		t.Fatal("clearing a models aspect that was never set must answer changed=false")
	}
	if after := len(f.events(t)); after != before {
		t.Fatalf("the idempotent clear emitted %d events", after-before)
	}
}

// The paused scene keeps accepting writes (the owner's
// change-before-wake window): the frozen CURRENT step's model chain may
// change, the write lands in the kept scene and the answer names the
// paused posture.
func TestOverrideWritePausedScene(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov4e")
	if _, err := f.td.client.Pause(f.wi); err != nil {
		t.Fatalf("pause: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if ji, ok := f.td.rt.Info(f.wi); ok && ji.State == runtime.StatePaused {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the work item never reached the paused state")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// the frozen current step (build, in_progress, no process) accepts a
	// model chain — exactly the owner's window
	res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "build", Operator: "usr_p", Models: []string{"pi:ovtest/m-two"}})
	if ae != nil {
		t.Fatalf("the paused write was refused: %s (%s)", ae.Code, ae.Detail)
	}
	if res.Posture != "paused" {
		t.Fatalf("posture %q, want paused", res.Posture)
	}
	ov := f.readOverlay(t)
	if m := ov.Steps["build"]; m == nil || len(m.Models) != 1 || m.Models[0] != "pi:ovtest/m-two" {
		t.Fatalf("the overlay in the kept scene: %+v", ov.Steps["build"])
	}
	// the skip switch stays for steps that never started
	yes := true
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "merge", Skip: &yes}); ae != nil {
		t.Fatalf("skipping the pending merge of a paused work item must be accepted: %s", ae.Detail)
	}
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "build", Skip: &yes}); ae == nil {
		t.Fatal("the frozen current step accepted the skip switch (it has started)")
	}
}

// The blocked work item (no job; the sealed scene of its last close):
// the write lands in the daemon-local pending record, the sealed scene
// is not written, and the view marks the aspects pending.
func TestOverrideWriteBlockedPendingRecord(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov4f")
	if err := f.td.rt.Stop(context.Background(), f.wi, protocol.FinishBlocked); err != nil {
		t.Fatalf("blocked close: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, ok := f.td.rt.Info(f.wi); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the blocked close never dropped the job")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// the sealed scene is read-only
	st, err := os.Stat(f.capDir)
	if err != nil || st.Mode().Perm() != 0o555 {
		t.Fatalf("the blocked capsule must be sealed read-only: %v (%o)", err, st.Mode().Perm())
	}
	res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "build", Operator: "usr_b", Models: []string{"pi:ovtest/m-ok"}})
	if ae != nil {
		t.Fatalf("the blocked write was refused: %s (%s)", ae.Code, ae.Detail)
	}
	if res.Posture != "blocked" || res.Published {
		t.Fatalf("the blocked write's answer: %+v", res)
	}
	if _, err := os.Stat(f.overlayFile()); !os.IsNotExist(err) {
		t.Fatal("the blocked write must not touch the sealed capsule")
	}
	pend := f.td.rt.PendingStepOverrides(f.wi)
	if pend["build"] == nil || len(pend["build"].Models) != 1 || pend["build"].Models[0] != "pi:ovtest/m-ok" {
		t.Fatalf("the pending record: %+v", pend)
	}
	// the view answers over the sealed scene + the pending record
	v, ae := f.td.ovView(t, f.wi)
	if ae != nil {
		t.Fatalf("view: %s", ae.Detail)
	}
	if v.Posture != "blocked" {
		t.Fatalf("view posture %q, want blocked", v.Posture)
	}
	var row *protocol.OverrideStepView
	for i := range v.Steps {
		if v.Steps[i].ID == "build" {
			row = &v.Steps[i]
		}
	}
	if row == nil || !row.Pending || len(row.Models) != 1 || row.Models[0] != "pi:ovtest/m-ok" {
		t.Fatalf("the build row: %+v", row)
	}
	if row.Source != "step override (pending)" {
		t.Fatalf("the pending row's source %q", row.Source)
	}
}

// The view: one row per declared step in lane order, the skippable
// declaration, the execution statuses, and the effective chain's source
// (the overlay over the yaml).
func TestOverrideViewEffectiveSource(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov4g")
	ws, err := f.td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"),
		[]byte("steps:\n  proj:\n    review: { models: [pi:ovtest/m-review] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "merge", Operator: "usr_1", Models: []string{"pi:ovtest/m-two"}}); ae != nil {
		t.Fatalf("the merge write: %s", ae.Detail)
	}
	v, ae := f.td.ovView(t, f.wi)
	if ae != nil {
		t.Fatalf("view: %s", ae.Detail)
	}
	if v.Posture != "running" {
		t.Fatalf("posture %q", v.Posture)
	}
	if len(v.Steps) != 3 || v.Steps[0].ID != "build" || v.Steps[1].ID != "review" || v.Steps[2].ID != "merge" {
		t.Fatalf("the rows are not the lane's steps in order: %+v", v.Steps)
	}
	byID := map[string]protocol.OverrideStepView{}
	for _, s := range v.Steps {
		byID[s.ID] = s
	}
	if r := byID["review"]; r.Skippable || r.Status != capsule.StepPending || r.Source != "steps.proj.review.models" || len(r.Effective) != 1 || r.Effective[0] != "pi:ovtest/m-review" {
		t.Fatalf("the review row: %+v", r)
	}
	if m := byID["merge"]; !m.Skippable || m.Source != "step override" || len(m.Effective) != 1 || m.Effective[0] != "pi:ovtest/m-two" {
		t.Fatalf("the merge row (the overlay governs): %+v", m)
	}
	if b := byID["build"]; b.Skippable || b.Status != capsule.StepInProgress || b.Source != "" {
		t.Fatalf("the build row (no list anywhere; the harness default): %+v", b)
	}
	// an unknown work item answers 404
	if _, ae := f.td.ovView(t, "wi_nope"); ae == nil || ae.Code != api.ErrDaemonNotFound {
		t.Fatalf("an unknown work item's view: %+v", ae)
	}
}

// TestPF25OverrideHelperProcess is not a test: re-executed as a child, it
// posts one override write over the socket named in its environment.
func TestPF25OverrideHelperProcess(t *testing.T) {
	sock := os.Getenv("PF25_SOCK")
	if sock == "" {
		t.Skip("helper process only")
	}
	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}}, Timeout: 30 * time.Second}
	defer c.CloseIdleConnections()
	body := `{"step":"merge","operator":"agent","models":["pi:ovtest/m-ok"]}`
	resp, err := c.Post("http://daemon/jobs/wi_ov4h/overrides", "application/json", strings.NewReader(body))
	if err != nil {
		fmt.Println("PF25-ERROR", err)
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusForbidden {
		fmt.Println("PF25-REFUSED", strings.TrimSpace(string(raw)))
		return
	}
	fmt.Println("PF25-ACCEPTED", resp.StatusCode, strings.TrimSpace(string(raw)))
}

// A step agent's process tree is refused DAEMON-403 on the write (the
// overlay is the owner's move, exactly like every other mutating route).
func TestOverrideStepOriginRefused(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov4h")
	argv := []string{os.Args[0], "-test.run=^TestPF25OverrideHelperProcess$", "-test.count=1"}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "PF25_SOCK="+f.td.sock, "PF_WI_ID=wi_ov4h", "PF_DAEMON_ID="+f.td.cfg.MachineID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "PF25-REFUSED") || !strings.Contains(got, "DAEMON-403") {
		t.Fatalf("a step process wrote the overlay: %s", got)
	}
	if _, err := os.Stat(f.overlayFile()); !os.IsNotExist(err) {
		t.Fatal("the refused write left an overlay file behind")
	}
	// the control: the same write from a plain caller lands
	if res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "merge", Operator: "usr_1", Models: []string{"pi:ovtest/m-ok"}}); ae != nil {
		t.Fatalf("the plain caller's write: %s", ae.Detail)
	} else if !res.Changed {
		t.Fatalf("the plain caller's write changed nothing: %+v", res)
	}
}

// A blocked clear of the chain the sealed capsule already carries (set
// while running, published with the chain) is a change: the answer says
// changed, and the view shows the step's chain cleared, pending — the
// overlay no longer governs it (r1#5).
func TestOverrideBlockedClearOfSealedChain(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov11d")
	if _, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "merge", Operator: "usr_1", Models: []string{"pi:ovtest/m-two"}}); ae != nil {
		t.Fatalf("the running write: %s (%s)", ae.Code, ae.Detail)
	}
	if err := f.td.rt.Stop(context.Background(), f.wi, protocol.FinishBlocked); err != nil {
		t.Fatalf("blocked close: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, ok := f.td.rt.Info(f.wi); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the blocked close never dropped the job")
		}
		time.Sleep(5 * time.Millisecond)
	}
	res, ae := f.td.ovPost(t, f.wi, protocol.OverrideWriteRequest{Step: "merge", Operator: "usr_b", ClearModels: true})
	if ae != nil {
		t.Fatalf("the blocked clear was refused: %s (%s)", ae.Code, ae.Detail)
	}
	if !res.Changed || res.Posture != "blocked" || res.Published {
		t.Fatalf("the blocked clear of the sealed chain must be an unpublished blocked change: %+v", res)
	}
	if res.Step.Models != nil {
		t.Fatalf("the answer's row still carries a chain: %+v", res.Step)
	}
	v, ae := f.td.ovView(t, f.wi)
	if ae != nil {
		t.Fatalf("view: %s", ae.Detail)
	}
	var row *protocol.OverrideStepView
	for i := range v.Steps {
		if v.Steps[i].ID == "merge" {
			row = &v.Steps[i]
		}
	}
	if row == nil || !row.Pending || row.Models != nil || row.Source == "step override" || row.Source == "step override (pending)" {
		t.Fatalf("the merge row must show the chain cleared, pending: %+v", row)
	}
}

// A work item in a terminal status (here wrapped) takes no overlay
// write: it will never be claimed again, so the change would neither
// land nor ever be consumed (r1#6). DAEMON-409, nothing recorded.
func TestOverrideWriteTerminalRefused(t *testing.T) {
	f := newOverrideFixture(t, "wi_ov12")
	if err := f.td.rt.Stop(context.Background(), f.wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("wrapped close: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, ok := f.td.rt.Info(f.wi); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the wrapped close never dropped the job")
		}
		time.Sleep(5 * time.Millisecond)
	}
	no := false
	for _, req := range []protocol.OverrideWriteRequest{
		{Step: "merge", Operator: "usr_t", Models: []string{"pi:ovtest/m-ok"}},
		{Step: "merge", Operator: "usr_t", Skip: &no},
	} {
		res, ae := f.td.ovPost(t, f.wi, req)
		if ae == nil {
			t.Fatalf("the wrapped work item accepted %+v: %+v", req, res)
		}
		if ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "wrapped") {
			t.Fatalf("the refusal must be DAEMON-409 naming the status: %s (%s)", ae.Code, ae.Detail)
		}
	}
	if pend := f.td.rt.PendingStepOverrides(f.wi); len(pend) != 0 {
		t.Fatalf("a refused terminal write left a pending record: %+v", pend)
	}
}
