package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// v1_s10_spawn_fence_test.go: the spawn fence, driver/envelope edition.
//
// RETIRED SUB-ASSERTIONS (Phase-4 ticket polyforge#106): the legacy
// scenario's core was the agent→daemon flush(wrap) STALE_SPAWN fence — a
// zombie survivor (or a stale flush body) carrying an old/absent
// derivation counter answered DAEMON-409 STALE_SPAWN and never reached
// the monitor. That fence lives on the WI-Agent RPC flush proxy
// (protocol.FlushRequest spawn_n vs the runtime's announced counter), a
// legacy-only face with no driver counterpart: the driver-era seal is
// envelope collection + the CLI close, neither of which speaks the flush
// proxy. The sub-assertions are retired with this comment (deletion is
// #106's), not silently dropped.
//
// (The flush route and protocol.FlushRequest themselves are deleted.)
//
// LIVE REMAINDER, re-expressed on the driver's spawn/drop contract: the
// result-envelope drop path is ordinal-keyed AND ordinal-fenced.
// pending-results/<n>.json is only ever read by the dispatch holding
// ordinal n, and an envelope whose INNER spawn_n mismatches its drop
// path's ordinal is a validation defect — never accepted, never applied.
// The scenario forges a drop file at the live ordinal's path carrying a
// wrong inner spawn_n while the step awaits the owner on its open `pf ask`
// (its process resident, its dispatch polling that path) and pins: the
// forged envelope is fenced out (the open ask and the durable state are
// undisturbed), the REAL envelope — the same dispatch's, written once the
// owner's reply reached the process — is accepted exactly once, and the
// forged file never enters the accepted record.
func TestV1S10SpawnDropPathFence(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "s10: the spawn/drop-path fence",
		[]map[string]any{
			askAction("fix", "s10: parked while the fence is probed.", map[string]any{"do": "envelope", "id": "fix", "outcome": "completed"}),
			{"do": "envelope", "id": "review", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	askAt := waitAsk(t, d, wi, 30*time.Second)
	capDir := d.capsuleDir(wi)
	// The fence target: the LIVE ordinal's drop path (fix's dispatch
	// awaits the owner with its process resident, and its stepRun keeps
	// polling the path: the fence under test).
	dropPath := filepath.Join(capDir, "runtime-state", "pending-results", "1.json")
	forge := map[string]any{
		"protocol_version": 1,
		"result_id":        "forged-result-id-s10",
		"wi_id":            wi,
		"step_id":          "fix",
		"spawn_n":          999, // the INNER ordinal mismatches the drop path
		"outcome":          "completed",
		"output":           map[string]any{"summary": "forged by the fence probe"},
		"artifacts":        []any{},
		"command_evidence": []any{},
		"submitted_at":     time.Now().UTC().Format(time.RFC3339),
	}
	blob, err := json.Marshal(forge)
	if err != nil {
		t.Fatal(err)
	}
	// `pf ask` wrote no envelope, so the drop directory may not exist yet
	if err := os.MkdirAll(filepath.Dir(dropPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dropPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}

	// The forged envelope is fenced out: the driver collects it, sees the
	// inner spawn_n mismatch (a validation defect against the dispatch's
	// fence), and never applies it. The open ask and the durable state
	// are undisturbed while the live dispatch keeps waiting for ITS
	// envelope.
	time.Sleep(1500 * time.Millisecond)
	if questionIDOf(statusQuiet(d), wi) != askAt {
		t.Fatal("the forged drop disturbed the open ask")
	}
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "running" || x.Steps["fix"].Status != "in_progress" {
		t.Fatalf("the forged drop disturbed the durable state: %+v", x)
	}
	for _, a := range x.Steps["fix"].Attempts {
		if a["accepted_result_id"] == "forged-result-id-s10" {
			t.Fatal("the forged envelope was accepted — the spawn fence is broken")
		}
	}

	// The fence held while the live derivation answered for itself: the
	// owner's reply reaches the resident process, whose envelope replaces
	// the forged file at its own ordinal and is accepted.
	replyOwner(t, d, wi, "The fence held.")
	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)

	// The accepted record is the real one, exactly once: the archived
	// envelopes carry the real result ids, the forged id appears nowhere,
	// and fix's accepted attempts are exactly the real continuations.
	x = readExecution(t, capDir)
	for _, step := range []string{"fix", "review"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	if h := acceptedHandoff(t, capDir, "fix", 1); h.Handoff.Status != stepcall.StatusDone {
		t.Fatalf("fix's real hand-off is %s, want done", h.Handoff.Status)
	}
	rawExec, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "execution.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawExec), "forged-result-id-s10") {
		t.Fatal("the forged result id entered the accepted execution state")
	}
	// One awaiting_owner for the open ask, one clean terminal close.
	if n := countOf(d.eventKinds(t, wi), "awaiting_owner"); n != 1 {
		t.Fatalf("awaiting_owner fired %d times, want 1: %v", n, d.eventKinds(t, wi))
	}
	if sess := inst.sessionOf(t, wi); sess.Fingerprint != "" {
		t.Fatalf("wrapped work item still names session %q", sess.Fingerprint)
	}
}
