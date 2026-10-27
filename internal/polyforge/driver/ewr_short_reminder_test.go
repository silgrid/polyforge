package driver

// ewr_short_reminder_test.go: workbench#7 — the first
// ended_without_result retry that really resumes the same harness
// conversation sends only the short reminder (finish the remaining
// work, submit with `pf result submit`), never the assembled step
// prompt. The driver test below drives spawn 1 to an EWR verdict and
// spawn 2 to a same-session resume and reads the prompt the production
// spawn adapter assembles for it.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// captureCount / captureParamsAt read the capture spawner under its lock
// (the driver appends concurrently).
func captureCount(sp *captureSpawner) int {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return len(sp.params)
}

func captureParamsAt(sp *captureSpawner, i int) runtime.SpawnParams {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.params[i]
}

func waitForCaptures(t *testing.T, sp *captureSpawner, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if captureCount(sp) >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d spawns (got %d)", n, captureCount(sp))
}

// The bundle's step definition is long enough to be recognisable in the
// prompt; the resumed spawn must NOT contain it (nor the context-policy
// line, the output schema or the recalled memories).
const ewrDefBody = "ewr-probe: implement the micro-spec, run the fast checks, then submit the envelope"

// TestEwrResumeSendsShortReminderNotTheStepPrompt is the external oracle
// (verification check ①): spawn 1 ends with an EWR verdict, spawn 2 is a
// same-session pi resume, and spawn 2's prompt contains `pf result
// submit` and neither the step definition nor the "Context policy" line.
// It fails on the pre-fix code (which re-sends the whole assembled
// prompt into the resumed session).
func TestEwrResumeSendsShortReminderNotTheStepPrompt(t *testing.T) {
	capDir := t.TempDir()
	// A materialized bundle the production adapter can read (the driver
	// under test uses the in-memory Workflow below).
	writeCapsuleFile(t, capDir, "workflows/b/workflow.md", "---\nname: b\nsteps:\n  - { id: s, context_policy: fresh }\n---\n")
	writeCapsuleFile(t, capDir, "workflows/b/steps/s.md", ewrDefBody)
	writeCapsuleFile(t, capDir, capsule.RecalledMemoriesJSON,
		`{"items":[{"id":"mem_ewr","type":"doc","title":"a recalled memory","excerpt":"prior context"}]}`)

	wi := WIInfo{ID: "wi_ewr", CapsuleDir: capDir, BundleName: "b", SessionHandle: "ab12cd34"}
	cap := &captureSpawner{}
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:           wi,
		Spawner:      cap,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	link := supervise.NewLink("wi_ewr")
	d, err := New(Config{
		WI:       wi,
		ExecPath: filepath.Join(capDir, capsule.ExecutionJSON),
		Workflow: &Workflow{Steps: []StepSpec{spec("s")}, MaxTotal: 4, MaxPerStep: 2},
		Compose:  composeOf(map[string][]string{"s": {"pi:a", "pi:b"}}),
		Spawner:  ad,
		Log:      &safeLog{},
		Link:     link,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- d.Run(context.Background()) }()

	// Spawn 1: make its capsule transcript readable and journal it, so
	// spawn 2 is a REAL same-session pi resume, then deliver the EWR
	// verdict the supervisor would send.
	waitForCaptures(t, cap, 1)
	p1 := captureParamsAt(cap, 0)
	writeCapsuleFile(t, capDir, strings.TrimPrefix(p1.TranscriptPath, capDir+"/"), "{\"type\":\"user\"}\n")
	if err := ad.appendSpawnJournal(p1, "s", "tok1", 1, "2026-10-25T00:00:00Z"); err != nil {
		t.Fatalf("appendSpawnJournal: %v", err)
	}
	link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseEndedWithoutResult, Evidence: "turn ended ok"})

	// Spawn 2: collect the prompt the adapter assembled, then complete
	// the step through the drop path.
	waitForCaptures(t, cap, 2)
	writeCapsuleFile(t, capDir, "runtime-state/pending-results/2.json", `{"outcome":"completed","output":{"ok":true}}`)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	p2 := captureParamsAt(cap, 1)
	if !p2.Resume {
		t.Fatalf("spawn 2 must resume the same harness session: %+v", p2)
	}
	prompt := p2.InitialPrompt
	for _, want := range []string{
		"pf result submit",
		"ended without submitting a result",
		"Do not start over",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("short reminder is missing %q:\n%s", want, prompt)
		}
	}
	for _, noise := range []string{
		"Context policy",
		ewrDefBody,
		"# Output schema",
		"a recalled memory",
		"# Recalled project memories",
	} {
		if strings.Contains(prompt, noise) {
			t.Errorf("short reminder must not re-send %q:\n%s", noise, prompt)
		}
	}
}

// TestEwrRetryAfterAnAnswerKeepsTheFullPrompt: the ledger can still name
// an earlier EWR attempt when an answered question continues the step.
// That dispatch is an answer continuation (RetryTriggered false), a
// non-goal: it keeps the full prompt, never the short reminder.
func TestEwrRetryAfterAnAnswerKeepsTheFullPrompt(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{
		StepID: "grill", WIID: "wi_sp", SpawnN: 1, Harness: harness.Pi,
	}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p1, _ := sp.last()
	writeCapsuleFile(t, capDir, strings.TrimPrefix(p1.TranscriptPath, capDir+"/"), "{\"type\":\"user\"}\n")
	if err := ad.appendSpawnJournal(p1, "grill", "tok1", 1, "2026-10-25T00:00:00Z"); err != nil {
		t.Fatalf("appendSpawnJournal: %v", err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{
		StepID: "grill", WIID: "wi_sp", SpawnN: 2, Harness: harness.Pi,
		Resume: true, PriorEndedWithoutResult: true, RetryTriggered: false,
	}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p2, _ := sp.last()
	if !p2.Resume {
		t.Fatalf("the answer continuation must resume the same session: %+v", p2)
	}
	if !strings.Contains(p2.InitialPrompt, "Context policy") {
		t.Fatalf("an answer continuation keeps the full prompt:\n%s", p2.InitialPrompt)
	}
	if strings.Contains(p2.InitialPrompt, "You are resuming step") {
		t.Fatalf("an answer continuation must not get the EWR short reminder:\n%s", p2.InitialPrompt)
	}
}

// TestEwrResumeThatDegradesKeepsTheFullPrompt: when the native session is
// gone the EWR retry degrades to a fresh conversation, and that dispatch
// keeps today's full prompt (definition + the EWR notice naming
// `pf result submit`), never the short reminder.
func TestEwrResumeThatDegradesKeepsTheFullPrompt(t *testing.T) {
	ad, sp, _ := spawnEnv(t)
	// no spawn journal line: the pi transcript is unusable, so the
	// ordered resume degrades to fresh + read-only reference
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{
		StepID: "grill", WIID: "wi_sp", SpawnN: 2, Harness: harness.Pi,
		Resume: true, PriorEndedWithoutResult: true, RetryTriggered: true,
	}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	if p.Resume {
		t.Fatalf("no usable transcript must degrade to fresh: %+v", p)
	}
	if !strings.Contains(p.InitialPrompt, "Context policy") {
		t.Fatalf("the degraded dispatch keeps the full prompt:\n%s", p.InitialPrompt)
	}
	if !strings.Contains(p.InitialPrompt, "ended without submitting a result") ||
		!strings.Contains(p.InitialPrompt, "pf result submit") {
		t.Fatalf("the degraded dispatch's EWR notice must name `pf result submit`:\n%s", p.InitialPrompt)
	}
}
