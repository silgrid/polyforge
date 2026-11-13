package daemon

// answer_wake_test.go: the owner's message to a parked work item
// (workflow contract 4, design #483 §7; ruling R9). `pf
// answer` no longer bounces off a parked work item with DAEMON-409: the
// daemon names the park and writes nothing, the CLI opens the door `pf
// resume` opens with the caller's own key, then the daemon records the
// message and wakes the step. A park whose agent cannot start stays
// parked: the refusal says why and names `pf resume` / `pf cancel`. A
// refused door is reported verbatim and nothing is recorded.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

// seedBlockedCapsule writes a work item's capsule parked blocked with
// reason on step s (dispatch 2 on its spawn journal) and has the hub
// report it blocked. It answers the capsule dir.
func (td *testDaemon) seedBlockedCapsule(t *testing.T, wi, reason string) string {
	t.Helper()
	td.hub.mu.Lock()
	td.hub.status[wi] = api.StatusBlocked
	name := api.PathName(td.hub.slugs[wi], wi)
	td.hub.mu.Unlock()
	dir := filepath.Join(td.cfg.Mapping.Workspaces()[0], "capsules", name)
	write := func(rel string, raw []byte) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idn, _ := json.Marshal(capsule.Identity{WorkItemID: wi, BundleName: "demo-bundle", Project: "proj"})
	write(capsule.CapsuleJSON, idn)
	ex := capsule.Execution{WorkItemID: wi, CurrentStep: "s", WorkflowStatus: capsule.WorkflowStatusBlocked, BlockedReason: reason,
		Steps: map[string]*capsule.Step{"s": {Status: capsule.StepInProgress, Round: 1}}}
	raw, _ := json.Marshal(ex)
	write(capsule.ExecutionJSON, raw)
	jl, _ := json.Marshal(capsule.SpawnJournalEntry{StepID: "s", SpawnN: 2})
	write(filepath.ToSlash(filepath.Join(capsule.DirRuntimeState, capsule.SpawnJournalFile)), append(jl, '\n'))
	return dir
}

// asksOf reads a capsule's ask record (none: nil).
func asksOf(t *testing.T, dir string) []capsule.AskLine {
	t.Helper()
	lines, err := capsule.ReadAsks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

const supervisionPark = "step s timed out again (tool_hung) after its bound was already doubled once: bash 40m"

// TestAnswerParkedNamesItsParkAndWritesNothing: without `wake`, a message
// to a parked work item is no DAEMON-409 — the daemon names the park, the
// step and how it wakes, and records nothing (the door comes first).
func TestAnswerParkedNamesItsParkAndWritesNothing(t *testing.T) {
	td := newTestDaemon(t, nil)
	dir := td.seedBlockedCapsule(t, "wi_wake_q", supervisionPark)
	raw, err := td.client.Answer("wi_wake_q", cli.AnswerPayload{AnswerText: "try again with more time", AnsweredBy: "alice"})
	if err != nil {
		t.Fatalf("a message to a parked work item is answered, not refused: %v", err)
	}
	var ack protocol.OwnerMessageAck
	_ = json.Unmarshal(raw, &ack)
	if ack.Status != "parked" || ack.Park != ParkBlocked || ack.Wake != string(driver.WakeStep) || ack.Step != "s" || ack.SpawnN != 2 || ack.At != "" {
		t.Fatalf("ack %+v, want parked / blocked / step s dispatch 2, nothing recorded", ack)
	}
	if lines := asksOf(t, dir); len(lines) != 0 {
		t.Fatalf("the classification recorded %+v", lines)
	}
}

// TestAnswerCannotStartStaysParked: a park whose agent cannot start — no
// usable model, a failed composition, a block at aihub with no workflow
// park here — is refused DAEMON-409 WAKE_CANNOT_START naming why and both
// moves, with or without `wake`, and nothing is recorded.
func TestAnswerCannotStartStaysParked(t *testing.T) {
	td := newTestDaemon(t, nil)
	for _, tc := range []struct{ wi, reason, why string }{
		{"wi_wake_nomodel", driver.NoUsableModelBlockPrefix + "step s: a model-class death (model_error) on \"pi:a\" and the step's candidate list is spent", "no usable model"},
		{"wi_wake_compose", "step s: the candidate composition failed at dispatch time: DAEMON-414: broken yaml", "composition failed"},
	} {
		dir := td.seedBlockedCapsule(t, tc.wi, tc.reason)
		for _, wake := range []string{"", ParkBlocked} {
			_, err := td.client.Answer(tc.wi, cli.AnswerPayload{AnswerText: "go", AnsweredBy: "alice", Wake: wake})
			var ae *api.Error
			if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict || !strings.Contains(string(ae.Extra), WakeRefusedCannotStart) ||
				!strings.Contains(ae.Detail, tc.why) || !strings.Contains(ae.Detail, "pf resume "+tc.wi) || !strings.Contains(ae.Detail, "pf cancel "+tc.wi) {
				t.Fatalf("%s (wake %q): %v, want WAKE_CANNOT_START naming why and pf resume / pf cancel", tc.wi, wake, err)
			}
		}
		if lines := asksOf(t, dir); len(lines) != 0 {
			t.Fatalf("%s: a refused wake recorded %+v", tc.wi, lines)
		}
	}
	// blocked at aihub with no capsule here: its agent never started
	td.hub.mu.Lock()
	td.hub.status["wi_wake_hubonly"] = api.StatusBlocked
	td.hub.mu.Unlock()
	_, err := td.client.Answer("wi_wake_hubonly", cli.AnswerPayload{AnswerText: "go", AnsweredBy: "alice"})
	var ae *api.Error
	if !errors.As(err, &ae) || !strings.Contains(string(ae.Extra), WakeRefusedCannotStart) || !strings.Contains(ae.Detail, "pf resume") {
		t.Fatalf("a hub-only block: %v, want WAKE_CANNOT_START", err)
	}
}

// TestAnswerContractParkOffersCancelAlone: a park whose
// lane snapshot targets another workflow contract — a `contract` park, or
// a park of the previous contract made before the upgrade, whatever its
// reason — cannot start: a resume parks it again, so the refusal is
// WAKE_CANNOT_START_CONTRACT naming `pf cancel` alone, and nothing is
// recorded. The same supervision park on a snapshot of this contract is
// woken as usual.
func TestAnswerContractParkOffersCancelAlone(t *testing.T) {
	td := newTestDaemon(t, nil)
	// snapshot lays out the lane snapshot of contract: this contract's
	// workflow.yaml, an earlier one's workflow.md (a drain's leftover)
	snapshot := func(dir string, contract int) {
		file, lane := workflow.WorkflowFile, fmt.Sprintf("contract: %d\nname: demo-bundle\ndescription: d\nsteps:\n  - id: s\n", contract)
		if contract != workflow.ContractVersion {
			file, lane = workflow.Contract4File, "---\n"+lane+"---\n"
		}
		p := filepath.Join(dir, capsule.DirWorkflows, "demo-bundle", file)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(lane), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	previous := workflow.ContractVersion - 1
	contractPark := fmt.Sprintf("contract: the capsule's lane snapshot targets workflow contract %d, this daemon implements contract %d (WFC-101)", previous, workflow.ContractVersion)
	for _, tc := range []struct {
		wi, reason string
		contract   int
	}{{"wi_wake_contract", contractPark, workflow.ContractVersion}, {"wi_wake_previous", supervisionPark, previous}} {
		dir := td.seedBlockedCapsule(t, tc.wi, tc.reason)
		snapshot(dir, tc.contract)
		_, err := td.client.Answer(tc.wi, cli.AnswerPayload{AnswerText: "go", AnsweredBy: "alice"})
		var ae *api.Error
		var extra struct{ Reason string }
		if errors.As(err, &ae) {
			_ = json.Unmarshal(ae.Extra, &extra)
		}
		if ae == nil || ae.Code != api.ErrDaemonConflict || extra.Reason != WakeRefusedContract ||
			!strings.Contains(ae.Detail, "pf cancel "+tc.wi) || strings.Contains(ae.Detail, "pf resume") {
			t.Fatalf("%s: %v, want WAKE_CANNOT_START_CONTRACT naming pf cancel alone", tc.wi, err)
		}
		if lines := asksOf(t, dir); len(lines) != 0 {
			t.Fatalf("%s: a refused wake recorded %+v", tc.wi, lines)
		}
	}
	// the same supervision park on this contract's snapshot wakes
	dir := td.seedBlockedCapsule(t, "wi_wake_current", supervisionPark)
	snapshot(dir, workflow.ContractVersion)
	raw, err := td.client.Answer("wi_wake_current", cli.AnswerPayload{AnswerText: "go", AnsweredBy: "alice"})
	var ack protocol.OwnerMessageAck
	_ = json.Unmarshal(raw, &ack)
	if err != nil || ack.Status != "parked" || ack.Wake != string(driver.WakeStep) {
		t.Fatalf("a park of this contract wakes: %v %+v", err, ack)
	}
}

// TestAnswerPausedWithoutASessionIsRefusedPlainly: a work
// item paused at aihub that this daemon holds no paused session of (a
// restart dropped it, another machine paused it) is refused DAEMON-409
// naming why and `pf resume` — never the "retry" of a workflow between
// derivations — and nothing is recorded.
func TestAnswerPausedWithoutASessionIsRefusedPlainly(t *testing.T) {
	td := newTestDaemon(t, nil)
	dir := td.seedBlockedCapsule(t, "wi_wake_paused", supervisionPark)
	// a pause leaves the workflow running in its record
	raw, _ := json.Marshal(capsule.Execution{WorkItemID: "wi_wake_paused", CurrentStep: "s", WorkflowStatus: capsule.WorkflowStatusRunning,
		Steps: map[string]*capsule.Step{"s": {Status: capsule.StepInProgress, Round: 1}}})
	if err := os.WriteFile(filepath.Join(dir, capsule.ExecutionJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	td.hub.mu.Lock()
	td.hub.status["wi_wake_paused"] = api.StatusPaused
	td.hub.mu.Unlock()
	_, err := td.client.Answer("wi_wake_paused", cli.AnswerPayload{AnswerText: "go on", AnsweredBy: "alice"})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "holds no paused session") ||
		!strings.Contains(ae.Detail, "pf resume wi_wake_paused") || strings.Contains(ae.Detail, "retry") {
		t.Fatalf("a paused item without a session here: %v, want a plain refusal naming pf resume", err)
	}
	if lines := asksOf(t, dir); len(lines) != 0 {
		t.Fatalf("a refused message recorded %+v", lines)
	}
}

// claimedScene answers the live job's capsule dir once it holds a job.
func claimedScene(t *testing.T, td *testDaemon, wi string) string {
	t.Helper()
	td.waitLive(t, wi)
	ji, ok := td.rt.Info(wi)
	if !ok || ji.CapsuleDir == "" {
		t.Fatalf("%s holds no job after the wake", wi)
	}
	return ji.CapsuleDir
}

// hubDoorServer fronts the fake hub's resume-blocked door for the REAL
// CLI: refuse, when set, is the door's refusal envelope.
func hubDoorServer(t *testing.T, td *testDaemon, wi, refuse string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/work-items/"+wi+"/resume-blocked"):
			if refuse != "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(refuse))
				return
			}
			raw, _ := td.hub.ResumeBlockedWorkItem(wi)
			_, _ = w.Write(raw)
		default:
			http.Error(w, `{"code":"CORE-404","detail":"fake hub: route not scripted"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runRealAnswer drives the real `pf answer` against hubURL (through the
// daemon's data-plane proxy stand-in) and the daemon socket under test.
func runRealAnswer(t *testing.T, hubURL, socket, wi, text string) (int, string, string) {
	t.Helper()
	for _, k := range []string{"PF_SERVER", "PF_API_KEY", "PF_DAEMON_SOCKET", "PF_USER_ID", "PF_CHAT_ID"} {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PF_DAEMON_SOCKET", proxyFront(t, hubURL, socket))
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"answer", wi, "--text", text, "--by", "alice"}, bytes.NewReader(nil), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestPfAnswerWakesABlockedWorkItemThroughTheDoor: the real `pf answer`
// opens the resume-blocked door with the caller's key, then the daemon
// claims the work item with the message: the claimed scene's ask record
// carries it before the engine starts, addressed to the parked step.
func TestPfAnswerWakesABlockedWorkItemThroughTheDoor(t *testing.T) {
	td := newTestDaemon(t, nil)
	const wi = "wi_wake_door"
	td.seedBlockedCapsule(t, wi, supervisionPark)
	mkBare(t, td.cfg.GitRemoteBase, wi)
	hub := hubDoorServer(t, td, wi, "")
	code, stdout, stderr := runRealAnswer(t, hub.URL, td.sock, wi, "the flaky test is fixed upstream, run it again")
	if code != 0 {
		t.Fatalf("pf answer exited %d (stdout %q, stderr %q)", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "woken") || !strings.Contains(stdout, "step s") {
		t.Fatalf("stdout %q, want the woken line naming step s", stdout)
	}
	td.hub.mu.Lock()
	doors := append([]string(nil), td.hub.resumeBlockeds...)
	td.hub.mu.Unlock()
	if len(doors) != 1 || doors[0] != wi {
		t.Fatalf("resume-blocked doors %v, want one for %s (the caller's door)", doors, wi)
	}
	lines := asksOf(t, claimedScene(t, td, wi))
	if len(lines) != 1 || lines[0].Kind != capsule.AskKindMessage || lines[0].Step != "s" || lines[0].Spawn != 2 ||
		lines[0].Text != "the flaky test is fixed upstream, run it again" || lines[0].By != "alice" {
		t.Fatalf("the claimed scene's ask record %+v, want the owner's message to step s", lines)
	}
}

// TestPfAnswerReportsARefusedDoorVerbatim: the door refuses the caller —
// the refusal is reported verbatim, nothing is recorded, nothing claimed.
func TestPfAnswerReportsARefusedDoorVerbatim(t *testing.T) {
	td := newTestDaemon(t, nil)
	const wi = "wi_wake_refused"
	dir := td.seedBlockedCapsule(t, wi, supervisionPark)
	const detail = "only the reporter, a project maintainer or an admin may resume this work item"
	hub := hubDoorServer(t, td, wi, `{"code":"CORE-403","detail":"`+detail+`"}`)
	code, stdout, stderr := runRealAnswer(t, hub.URL, td.sock, wi, "go on")
	if code == 0 {
		t.Fatalf("pf answer exited 0 on a refused door (stdout %q)", stdout)
	}
	if !strings.Contains(stderr, detail) || !strings.Contains(stderr, "nothing was recorded") {
		t.Fatalf("stderr %q, want the door's refusal verbatim and that nothing was recorded", stderr)
	}
	if lines := asksOf(t, dir); len(lines) != 0 {
		t.Fatalf("a refused door recorded %+v", lines)
	}
	td.hub.mu.Lock()
	claims := len(td.hub.claims)
	td.hub.mu.Unlock()
	if claims != 0 {
		t.Fatalf("a refused door claimed the work item (%d claims)", claims)
	}
}

// TestPfAnswerWakesAPausedWorkItem: the real `pf answer` to a paused work
// item clears clean_pause with the caller's key (the paused door), then
// the daemon records the message in the retained scene and resumes the
// session in place.
func TestPfAnswerWakesAPausedWorkItem(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) { c.DrainInterval = time.Hour })
	td.submitClaim(t, "wi_b6")
	if _, err := td.client.Pause("wi_b6"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	waitState(t, td, "wi_b6", "paused")
	ji, _ := td.rt.Info("wi_b6")
	ex := capsule.Execution{WorkItemID: "wi_b6", CurrentStep: "s", WorkflowStatus: capsule.WorkflowStatusRunning,
		Steps: map[string]*capsule.Step{"s": {Status: capsule.StepInProgress, Round: 1, Dispatch: 1}}}
	raw, _ := json.Marshal(ex)
	if err := os.WriteFile(filepath.Join(ji.CapsuleDir, capsule.ExecutionJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	patches := 0
	hub := hubOverHTTP(t, td, &patches)
	code, stdout, stderr := runRealAnswer(t, hub.URL, td.sock, "wi_b6", "carry on, the outage is over")
	if code != 0 {
		t.Fatalf("pf answer exited %d (stdout %q, stderr %q)", code, stdout, stderr)
	}
	if patches != 1 || !strings.Contains(stdout, "paused step continues") {
		t.Fatalf("clean_pause patches %d, stdout %q: want the paused door opened once and the woken line", patches, stdout)
	}
	lines := asksOf(t, ji.CapsuleDir)
	if len(lines) != 1 || lines[0].Step != "s" || lines[0].Spawn != 1 || lines[0].Text != "carry on, the outage is over" {
		t.Fatalf("the retained scene's ask record %+v, want the owner's message to step s", lines)
	}
	waitState(t, td, "wi_b6", "running")
	if attachCount(td.hub) == 0 {
		t.Fatal("the paused session was not re-attached")
	}
}
