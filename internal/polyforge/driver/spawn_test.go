package driver

// spawn_test.go: the StepSpawnAdapter — prompt assembly, the SpawnParams
// mapping (model pin, resume session from the spawn journal, identity
// journal), and the stepRun's hand-off collection (the call ledger,
// teardown, exit without a hand-off).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// ---------------------------------------------------------------------------
// Fakes: a runtime.Spawner that records every SpawnParams and hands
// back a controllable AgentHandle.

type fakeAgent struct {
	mu   sync.Mutex
	done bool
	term bool
}

func (h *fakeAgent) PID() int          { return 4242 }
func (h *fakeAgent) StartedAt() string { return "2026-10-06T00:00:00Z" }
func (h *fakeAgent) Alive() bool       { h.mu.Lock(); defer h.mu.Unlock(); return !h.done }
func (h *fakeAgent) Done() bool        { h.mu.Lock(); defer h.mu.Unlock(); return h.done }
func (h *fakeAgent) Terminate(bound time.Duration) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.done = true
	h.term = true
	return nil
}

func (h *fakeAgent) setDone() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.done = true
}

func (h *fakeAgent) terminated() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.term
}

type captureSpawner struct {
	mu      sync.Mutex
	params  []runtime.SpawnParams
	handles []*fakeAgent
}

func (s *captureSpawner) Spawn(ctx context.Context, p runtime.SpawnParams) (runtime.AgentHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := &fakeAgent{}
	s.params = append(s.params, p)
	s.handles = append(s.handles, h)
	return h, nil
}

func (s *captureSpawner) last() (runtime.SpawnParams, *fakeAgent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.params[len(s.params)-1], s.handles[len(s.handles)-1]
}

// ---------------------------------------------------------------------------
// Environment: a capsule with a materialized bundle (workflow.yaml with
// the extended frontmatter + steps/<id>.md definitions) and a session
// handle.

func writeCapsuleFile(t *testing.T, capDir, rel, content string) {
	t.Helper()
	path := filepath.Join(capDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeArchivedResult(t *testing.T, capDir, stepID string, n int64, res *StepResult) {
	t.Helper()
	blob, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeCapsuleFile(t, capDir, fmt.Sprintf("artifacts/steps/%s/s%d/result.json", stepID, n), string(blob))
}

func spawnEnv(t *testing.T) (*StepSpawnAdapter, *captureSpawner, string) {
	t.Helper()
	capDir := t.TempDir()
	writeExtLane(t, capDir, map[string]string{
		"grill":        "grill: ask the sharp questions",
		"fix":          "fix: implement the micro-spec",
		"verification": "verification: build, lint, test",
	})
	sp := &captureSpawner{}
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:           WIInfo{ID: "wi_sp", CapsuleDir: capDir, BundleName: "autopilot-x", SessionHandle: "ab12cd34"},
		Spawner:      sp,
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	return ad, sp, capDir
}

// grillDone lays out the post-grill capsule: the artifacts the next
// step reads by path convention, the handoff FILE at the convention path the next inherit step reads
// (artifacts/handoff-grill.md — polyforge#147: path convention, no
// digest), and grill's archived envelope (minimal contract).
func grillDone(t *testing.T, capDir string) {
	t.Helper()
	writeCapsuleFile(t, capDir, "artifacts/fix-memo.md", "the micro-spec body")
	writeCapsuleFile(t, capDir, "artifacts/grill-qa.md", "the q/a log")
	writeCapsuleFile(t, capDir, "artifacts/handoff-grill.md", "handoff from grill: the micro-spec is at artifacts/fix-memo.md")
	writeArchivedResult(t, capDir, "grill", 1, &StepResult{
		WIID:    "wi_sp",
		StepID:  "grill",
		SpawnN:  1,
		Outcome: OutcomeCompleted,
		Output:  json.RawMessage(`{}`),
	})
}

// ---------------------------------------------------------------------------
// Prompt assembly.

func TestSpawnStepFreshPromptOmitsHandoff(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	grillDone(t, capDir)
	writeCapsuleFile(t, capDir, "artifacts/fix-summary.md", "the fix summary")

	_, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 3})
	if err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	prompt := p.InitialPrompt
	if !strings.Contains(prompt, "verification: build, lint, test") {
		t.Fatalf("prompt missing the step definition (frontmatter stripped)\n%s", prompt)
	}
	if strings.Contains(prompt, "tools: [go]") {
		t.Fatalf("step-definition frontmatter must be stripped\n%s", prompt)
	}
	// Nothing is injected: a fresh step reads its upstream artifacts by
	// path convention (polyforge#147 — the inputs lane is retired).
	if strings.Contains(prompt, "the fix summary") {
		t.Fatalf("fresh prompt must not inject artifact content\n%s", prompt)
	}
	if strings.Contains(prompt, "the micro-spec body") || strings.Contains(prompt, "Handoff from step") {
		t.Fatalf("fresh prompt must not carry a handoff\n%s", prompt)
	}
	if !strings.Contains(prompt, probeStepHeader) || !strings.Contains(prompt, probeHandoffBody) {
		t.Fatalf("prompt missing the step header and the hand-off body template\n%s", prompt)
	}
}

// TestLatestJournalTranscriptFiltersByStep: the capsule journal
// interleaves every step's spawns; a resume of step X must never pick
// the higher-ordinal transcript of another step (the review-inside-
// another-step's-conversation bug), and lines without a step id never match.
func TestLatestJournalTranscriptFiltersByStep(t *testing.T) {
	ad, _, capDir := spawnEnv(t)
	writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
		`{"spawn_n":1,"step_id":"review","transcript":"/t/review1.jsonl"}
{"spawn_n":2,"step_id":"code","transcript":"/t/code2.jsonl"}
{"spawn_n":3,"step_id":"verification","transcript":"/t/ver3.jsonl"}
{"spawn_n":4,"transcript":"/t/untagged4.jsonl"}
{"spawn_n":5,"step_id":"review","transcript":""}
`)
	for step, want := range map[string]string{"review": "/t/review1.jsonl", "code": "/t/code2.jsonl", "verification": "/t/ver3.jsonl"} {
		e, err := ad.latestJournalEntry(step)
		if got := e.Transcript; err != nil || got != want {
			t.Fatalf("step %s: got (%q, %v) want %q", step, e.Transcript, err, want)
		}
	}
	if e, err := ad.latestJournalEntry("polish"); err != nil || e.Transcript != "" {
		t.Fatalf("a step with no journal line answers no transcript, got (%q, %v)", e.Transcript, err)
	}
}

func TestSpawnStepResumeUsesJournal(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	writeCapsuleFile(t, capDir, "artifacts/fix-summary.md", "the fix summary")
	journal := `{"spawn_n":1,"step_id":"verification","transcript":"/old/t1.jsonl","work_item":"wi_sp"}
{"spawn_n":3,"step_id":"verification","transcript":"/live/t3.jsonl","work_item":"wi_sp"}
`
	liveT3 := filepath.Join(capDir, "session", "live-t3.jsonl")
	writeCapsuleFile(t, capDir, "session/live-t3.jsonl", `{"t":"turn"}`+"\n")
	journal = strings.ReplaceAll(journal, "/live/t3.jsonl", liveT3)
	writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl", journal)

	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 4, Resume: true}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	if !p.Resume || p.TranscriptPath != liveT3 {
		t.Fatalf("resume mapping: resume=%v transcript=%q", p.Resume, p.TranscriptPath)
	}

	// a fresh derivation declares its own capsule-side session copy
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 5}); err != nil {
		t.Fatalf("SpawnStep fresh: %v", err)
	}
	p2, _ := sp.last()
	if p2.Resume {
		t.Fatal("fresh dispatch must not resume")
	}
	want := filepath.Join(capDir, capsule.DirSession, "session-ab12cd34#4.jsonl")
	if p2.TranscriptPath != want {
		t.Fatalf("fresh transcript: %q want %q", p2.TranscriptPath, want)
	}
}

func TestSpawnStepJournalCallback(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	writeCapsuleFile(t, capDir, "artifacts/fix-summary.md", "s")
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 7}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	if p.Journal == nil {
		t.Fatal("SpawnParams must carry the journal callback")
	}
	if err := p.Journal(99, "2026-10-06T01:02:03Z"); err != nil {
		t.Fatalf("journal callback: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(capDir, capsule.DirRuntimeState, "spawn-journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &line); err != nil {
		t.Fatalf("journal line unparsable: %v\n%s", err, raw)
	}
	for key, want := range map[string]any{
		"work_item": "wi_sp", "pid": float64(99), "spawn_n": float64(7),
		"resume": false, "transcript": p.TranscriptPath, "session_handle": "ab12cd34",
	} {
		if line[key] != want {
			t.Fatalf("journal %s = %v, want %v", key, line[key], want)
		}
	}
	if line["spawn_token"] != "ab12cd34#6" {
		t.Fatalf("journal spawn_token = %v", line["spawn_token"])
	}
}

func TestSpawnStepInputFormErrors(t *testing.T) {
	ad, _, capDir := spawnEnv(t)
	grillDone(t, capDir)

	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "nope", SpawnN: 1}); err == nil {
		t.Fatal("unknown step must fail")
	}
	// A retired declared-input file's absence is NO LONGER an error:
	// the step reads what it needs by path convention (polyforge#147).
	if err := os.Remove(filepath.Join(capDir, "artifacts", "grill-qa.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "fix", SpawnN: 2}); err != nil {
		t.Fatalf("a missing artifact must not break dispatch: %v", err)
	}

	// missing step definition
	if err := os.Remove(filepath.Join(capDir, "workflows", "autopilot-x", "steps", "fix.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "fix", SpawnN: 2}); err == nil ||
		!strings.Contains(err.Error(), "step definition") {
		t.Fatalf("missing step definition must fail, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// stepRun: hand-off collection (workflow contract 4: the call ledger).

// ledgerHandoff appends a dispatch's accepted `pf step handoff` to the
// capsule's call ledger as the daemon records it: the call line, then its
// ok result line carrying the hand-off.
func appendLedgerHandoff(t *testing.T, capDir, step string, spawn int64, status string) {
	t.Helper()
	call, err := stepcall.Append(capDir, stepcall.Line{Kind: stepcall.KindCall, Step: step, Round: 1, Spawn: spawn, Verb: stepcall.VerbHandoff})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stepcall.Append(capDir, stepcall.Line{Kind: stepcall.KindResult, Step: step, Round: 1, Spawn: spawn, Verb: stepcall.VerbHandoff, Call: call.Seq,
		Outcome: stepcall.OutcomeOK, Handoff: &stepcall.Handoff{Status: status}}); err != nil {
		t.Fatal(err)
	}
}

// TestStepRunWaitCollectsTheLedgersHandoff: the run answers the
// dispatch's accepted hand-off from the call ledger, tears the process
// down, and archives nothing (the ledger is the record).
func TestStepRunWaitCollectsTheLedgersHandoff(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	run, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 5})
	if err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	appendLedgerHandoff(t, capDir, "verification", 5, stepcall.StatusFail)
	res, err := run.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Handoff == nil || res.Handoff.Status != stepcall.StatusFail || res.StepID != "verification" || res.SpawnN != 5 || res.WIID != "wi_sp" || res.HandoffCall != 1 {
		t.Fatalf("collected hand-off: %+v", res)
	}
	if _, h := sp.last(); !h.terminated() {
		t.Fatal("the process must be torn down once its hand-off is collected")
	}
	if _, err := os.Stat(filepath.Join(capDir, "artifacts", "steps")); !os.IsNotExist(err) {
		t.Fatalf("nothing is archived: %v", err)
	}
}

// TestStepRunWaitIgnoresAnotherDispatchsHandoff: a hand-off of another
// spawn ordinal or another step is never this dispatch's — the process
// that dies without its own is an exit without a hand-off.
func TestStepRunWaitIgnoresAnotherDispatchsHandoff(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	run, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 7})
	if err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	appendLedgerHandoff(t, capDir, "verification", 4, stepcall.StatusDone)
	appendLedgerHandoff(t, capDir, "fix", 7, stepcall.StatusDone)
	_, h := sp.last()
	h.setDone()
	_, err = run.Wait(context.Background())
	var ee *ExitedError
	if !errors.As(err, &ee) || ee.SpawnN != 7 {
		t.Fatalf("another dispatch's hand-off must not settle this one: %v", err)
	}
}

// TestStepRunWaitHandoffLandingWhileWaiting: a hand-off recorded while
// the run polls is collected.
func TestStepRunWaitHandoffLandingWhileWaiting(t *testing.T) {
	ad, _, capDir := spawnEnv(t)
	run, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 8})
	if err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	go func() {
		time.Sleep(8 * time.Millisecond)
		appendLedgerHandoff(t, capDir, "verification", 8, stepcall.StatusDone)
	}()
	res, err := run.Wait(context.Background())
	if err != nil || res.Handoff == nil || res.Handoff.Status != stepcall.StatusDone {
		t.Fatalf("Wait = %+v %v", res, err)
	}
}

func TestStepRunWaitExitWithoutEnvelope(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	writeCapsuleFile(t, capDir, "artifacts/fix-summary.md", "s")
	run, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 6})
	if err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	_, h := sp.last()
	h.setDone() // the process died before delivering anything
	_, err = run.Wait(context.Background())
	var ee *ExitedError
	if err == nil || !errors.As(err, &ee) {
		t.Fatalf("exit without envelope must fail with an ExitedError, got %v", err)
	}
	if ee.Step != "verification" || ee.SpawnN != 6 {
		t.Fatalf("exit error identity: %+v", ee)
	}
}

func TestStepRunWaitContextCancel(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	writeCapsuleFile(t, capDir, "artifacts/fix-summary.md", "s")
	run, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 9})
	if err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(6 * time.Millisecond)
		cancel()
	}()
	if _, err := run.Wait(ctx); err == nil {
		t.Fatal("canceled Wait must fail")
	}
	_, h := sp.last()
	if h.terminated() {
		t.Fatal("Wait cancel must not tear the handle down (Run's error path owns that)")
	}
}

func TestSpawnAdapterConfigValidation(t *testing.T) {
	capDir := t.TempDir()
	if _, err := NewStepSpawnAdapter(StepSpawnAdapter{}); err == nil {
		t.Fatal("empty config must fail")
	}
	if _, err := NewStepSpawnAdapter(StepSpawnAdapter{WI: WIInfo{ID: "w", CapsuleDir: capDir}}); err == nil {
		t.Fatal("config without a spawner must fail")
	}
	// no bundle name/dir and no workflow: unresolvable
	if _, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:      WIInfo{ID: "w", CapsuleDir: capDir},
		Spawner: &captureSpawner{},
	}); err == nil {
		t.Fatal("config without a bundle name/dir must fail")
	}
}

func hashOfFile(t *testing.T, capDir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(capDir, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// Journal seeding + torn-tail repair (astra r2 #2/#3 regressions).

// TestAppendSpawnJournalRepairsTornTail is the astra-r2 #2 regression:
// a torn tail must never GLUE onto the next record — the repair isolates
// the half-line as its own line and the appended record stays parsable,
// so the next journal read sees it and the relaunch does not re-derive
// the same ordinal.
func TestAppendSpawnJournalRepairsTornTail(t *testing.T) {
	ad, _, capDir := spawnEnv(t)
	jpath := capDir + "/" + capsule.DirRuntimeState + "/spawn-journal.jsonl"
	writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
		"{\"spawn_n\":7}\n{\"spawn_n\":8, \"trans") // torn: no trailing newline

	p := spawn.Params{WorkItemID: "wi_sp", SpawnN: 9, TranscriptPath: "t"}
	if err := ad.appendSpawnJournal(p, "code", "s9", "", 4242, nowStampX()); err != nil {
		t.Fatalf("appendSpawnJournal: %v", err)
	}

	raw, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("torn tail + repair + record must yield 3 lines, got %d: %q", len(lines), string(raw))
	}
	// line 1 must survive the repair UNTOUCHED (an offset-0 clobber would
	// eat its opening brace)
	if lines[0] != `{"spawn_n":7}` {
		t.Fatalf("the first record must survive the repair verbatim, got %q", lines[0])
	}
	// the appended record must be its OWN line (not glued onto the tail)
	var rec struct {
		SpawnN int64 `json:"spawn_n"`
	}
	if json.Unmarshal([]byte(lines[2]), &rec) != nil || rec.SpawnN != 9 {
		t.Fatalf("the appended record must be its own parsable line with spawn 9, got %q", lines[2])
	}
	// and the ledger now seeds past the crashed dispatch's ordinal
	if got, err := capsule.SpawnJournalMaxN(capDir); got != 9 || err != nil {
		t.Fatalf("after repair the ledger must seed 9, got (%d, %v)", got, err)
	}
}

func nowStampX() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// TestSpawnStepFreshRetryCarriesPreviousTranscriptAsReference pins the
// 2nd+ retry shape (v2-converge 2.3): a fresh conversation — no resume —
// with the previous transcript, when readable, handed over as read-only
// reference in the prompt; a missing/empty transcript is simply omitted.
func TestSpawnStepFreshRetryCarriesPreviousTranscriptAsReference(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	writeCapsuleFile(t, capDir, "artifacts/fix-summary.md", "the fix summary")
	prev := filepath.Join(capDir, "session", "prev.jsonl")
	writeCapsuleFile(t, capDir, "session/prev.jsonl", `{"t":"turn"}`+"\n")
	writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
		`{"spawn_n":3,"step_id":"verification","transcript":"`+prev+`","work_item":"wi_sp"}`+"\n")

	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 4, Fresh: true}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	if p.Resume {
		t.Fatal("a fresh retry must not resume the old conversation")
	}
	if p.TranscriptPath == prev {
		t.Fatal("a fresh retry declares its own new transcript")
	}
	if !strings.Contains(p.InitialPrompt, "Previous attempt (read-only reference)") || !strings.Contains(p.InitialPrompt, prev) {
		t.Fatalf("the prompt must carry the previous transcript as reference:\n%s", p.InitialPrompt)
	}

	// an unreadable previous transcript is omitted, never an error
	if err := os.Remove(prev); err != nil {
		t.Fatal(err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 5, Fresh: true}); err != nil {
		t.Fatalf("SpawnStep (no transcript): %v", err)
	}
	p2, _ := sp.last()
	if strings.Contains(p2.InitialPrompt, "read-only reference") {
		t.Fatalf("no reference may be named when the transcript is gone:\n%s", p2.InitialPrompt)
	}
}

// TestSpawnStepNeverResumesOrReferencesAnotherStepsTranscript: with a
// journal whose highest ordinal belongs to a different step, a resume
// of "verification" attaches its OWN transcript and a fresh dispatch
// references only its own — never the foreign one.
func TestSpawnStepNeverResumesOrReferencesAnotherStepsTranscript(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	writeCapsuleFile(t, capDir, "artifacts/fix-summary.md", "the fix summary")
	own := filepath.Join(capDir, "session", "own.jsonl")
	foreign := filepath.Join(capDir, "session", "foreign.jsonl")
	writeCapsuleFile(t, capDir, "session/own.jsonl", `{"t":"turn"}`+"\n")
	writeCapsuleFile(t, capDir, "session/foreign.jsonl", `{"t":"turn"}`+"\n")
	writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
		`{"spawn_n":1,"step_id":"verification","transcript":"`+own+`"}`+"\n"+
			`{"spawn_n":2,"step_id":"other","transcript":"`+foreign+`"}`+"\n")

	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 3, Resume: true}); err != nil {
		t.Fatalf("SpawnStep resume: %v", err)
	}
	p, _ := sp.last()
	if !p.Resume || p.TranscriptPath != own {
		t.Fatalf("resume must use the step's own transcript: resume=%v transcript=%q", p.Resume, p.TranscriptPath)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 4, Fresh: true}); err != nil {
		t.Fatalf("SpawnStep fresh: %v", err)
	}
	p, _ = sp.last()
	if p.Resume || p.TranscriptPath == foreign || strings.Contains(p.InitialPrompt, foreign) || !strings.Contains(p.InitialPrompt, own) {
		t.Fatalf("fresh dispatch must reference only its own transcript: %+v\n%s", p, p.InitialPrompt)
	}
}

// TestSpawnStepResumeWithoutReadableTranscriptDegradesToFresh: a resume
// whose declared transcript was never written (the dispatch died before
// the harness created its session) starts the conversation fresh instead
// of handing the harness a path it cannot resume.
func TestSpawnStepResumeWithoutReadableTranscriptDegradesToFresh(t *testing.T) {
	ad, sp, capDir := spawnEnv(t)
	writeCapsuleFile(t, capDir, "artifacts/fix-summary.md", "s")
	writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
		`{"spawn_n":3,"step_id":"verification","transcript":"/does/not/exist.jsonl","work_item":"wi_sp"}`+"\n")
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 4, Resume: true}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	if p.Resume {
		t.Fatalf("an unreadable transcript cannot be resumed: %+v", p)
	}
}

// TestSpawnStepResumeWithoutJournalLineDegradesToFresh: a resume for a
// step with no spawn-journal line of its own (other steps' lines, or no
// journal at all) is a plain fresh dispatch, not an error.
func TestSpawnStepResumeWithoutJournalLineDegradesToFresh(t *testing.T) {
	for name, journal := range map[string]string{
		"other-step-only": `{"spawn_n":3,"step_id":"other","transcript":"/t/o.jsonl"}` + "\n" + `{"spawn_n":4,"transcript":"/t/untagged.jsonl"}` + "\n",
		"no-journal":      "",
	} {
		t.Run(name, func(t *testing.T) {
			ad, sp, capDir := spawnEnv(t)
			writeCapsuleFile(t, capDir, "artifacts/fix-summary.md", "s")
			if journal != "" {
				writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl", journal)
			}
			if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 4, Resume: true}); err != nil {
				t.Fatalf("resume without a journal line must degrade, not fail: %v", err)
			}
			p, _ := sp.last()
			if p.Resume || strings.Contains(p.TranscriptPath, "/t/") {
				t.Fatalf("expected a fresh dispatch: %+v", p)
			}
		})
	}
}

// rewindStepSpawn is the rewind re-entry shape the driver's
// prepareDispatch hands the adapter: a resume of the previous dispatch's
// conversation on a pending rewind target (the ledger's rewind cause
// derives the resume;).
func rewindStepSpawn(harnessID string) StepSpawn {
	return StepSpawn{StepID: "verification", SpawnN: 3, Resume: true, Harness: harnessID}
}

// lastJournal answers the capsule spawn journal's last entry (the shape
// the journal callback wrote for the dispatch under test).
func lastJournal(t *testing.T, capDir string) capsule.SpawnJournalEntry {
	t.Helper()
	entries, err := capsule.ReadSpawnJournal(capDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("spawn journal: %v %+v", err, entries)
	}
	return entries[len(entries)-1]
}

// TestSpawnStepRewindReentryResumesAndDegrades: the rewind
// re-entry is a Resume spawn at the adapter too — the same
// latestJournalEntry path every resume uses — and it resumes the previous
// dispatch's journal identity on BOTH harnesses: pi continues the previous
// transcript, claude continues its native session id; the spawn journal
// records resume: true on both (#211: writes = looks). A resume that is
// impossible — the previous dispatch ran on another harness, or the
// native session/transcript is gone — degrades to fresh (the previous
// transcript as read-only reference when one is readable), the journal
// recording resume: false: the same single rewind slot, never a
// mis-marking and never an error.
func TestSpawnStepRewindReentryResumesAndDegrades(t *testing.T) {
	t.Run("pi transcript continues", func(t *testing.T) {
		ad, sp, capDir := spawnEnv(t)
		prev := filepath.Join(capDir, "session", "prev-light.jsonl")
		writeCapsuleFile(t, capDir, "session/prev-light.jsonl", `{"t":"turn"}`+"\n")
		writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
			`{"spawn_n":2,"step_id":"verification","transcript":"`+prev+`","harness":"pi","work_item":"wi_sp"}`+"\n")
		if _, err := ad.SpawnStep(context.Background(), rewindStepSpawn("")); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		if !p.Resume || p.TranscriptPath != prev {
			t.Fatalf("the rewind re-entry must resume the previous pi transcript: resume=%v transcript=%q", p.Resume, p.TranscriptPath)
		}
		if strings.Contains(p.InitialPrompt, "# Rewind input") || strings.Contains(p.InitialPrompt, "the verdict in full") {
			t.Fatalf("workflow contract 4: the verdict is artifacts/handoff.md's, never a prompt section:\n%s", p.InitialPrompt)
		}
		if err := p.Journal(99, nowStampX()); err != nil {
			t.Fatalf("journal: %v", err)
		}
		if e := lastJournal(t, capDir); !e.Resume || e.Transcript != prev {
			t.Fatalf("the journal must record the resume: %+v", e)
		}
	})
	t.Run("claude native session continues", func(t *testing.T) {
		ad, sp, capDir := spawnEnv(t)
		ad.ClaudeConfigDir = filepath.Join(capDir, "claude-config")
		id := "01234567-89ab-cdef-0123-456789abcdef"
		native := filepath.Join("claude-config", "projects", harness.ClaudeProjectDirName(capDir), id+".jsonl")
		writeCapsuleFile(t, capDir, native, `{"t":"turn"}`+"\n")
		writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
			`{"spawn_n":2,"step_id":"verification","transcript":"`+filepath.Join(capDir, "session", "prev-claude.jsonl")+`","harness":"claude","harness_session":"`+id+`","work_item":"wi_sp"}`+"\n")
		if _, err := ad.SpawnStep(context.Background(), rewindStepSpawn("claude")); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		if !p.Resume || p.HarnessSession != id {
			t.Fatalf("the rewind re-entry must resume the previous claude session: resume=%v session=%q", p.Resume, p.HarnessSession)
		}
		if strings.Contains(p.InitialPrompt, "# Rewind input") || strings.Contains(p.InitialPrompt, "the verdict in full") {
			t.Fatalf("workflow contract 4: the verdict is artifacts/handoff.md's, never a prompt section:\n%s", p.InitialPrompt)
		}
		if err := p.Journal(99, nowStampX()); err != nil {
			t.Fatalf("journal: %v", err)
		}
		if e := lastJournal(t, capDir); !e.Resume || e.HarnessSession != id {
			t.Fatalf("the journal must record the resumed native session: %+v", e)
		}
	})
	t.Run("other harness degrades fresh with reference", func(t *testing.T) {
		ad, sp, capDir := spawnEnv(t)
		ad.ClaudeConfigDir = filepath.Join(capDir, "claude-config")
		id := "01234567-89ab-cdef-0123-456789abcdef"
		native := filepath.Join("claude-config", "projects", harness.ClaudeProjectDirName(capDir), id+".jsonl")
		writeCapsuleFile(t, capDir, native, `{"t":"turn"}`+"\n")
		prev := filepath.Join(capDir, "session", "prev-claude.jsonl")
		writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
			`{"spawn_n":2,"step_id":"verification","transcript":"`+prev+`","harness":"claude","harness_session":"`+id+`","work_item":"wi_sp"}`+"\n")
		// the light re-entry runs on pi: a claude session cannot be resumed there
		if _, err := ad.SpawnStep(context.Background(), rewindStepSpawn("")); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		if p.Resume {
			t.Fatalf("another harness's session cannot be resumed: %+v", p)
		}
		if !strings.Contains(p.InitialPrompt, "Previous attempt (read-only reference)") || !strings.Contains(p.InitialPrompt, prev) {
			t.Fatalf("the fallback must carry the previous transcript as reference:\n%s", p.InitialPrompt)
		}
		if err := p.Journal(99, nowStampX()); err != nil {
			t.Fatalf("journal: %v", err)
		}
		if e := lastJournal(t, capDir); e.Resume {
			t.Fatalf("the fallback must be recorded fresh: %+v", e)
		}
	})
	t.Run("claude session gone degrades fresh with reference", func(t *testing.T) {
		ad, sp, capDir := spawnEnv(t)
		ad.ClaudeConfigDir = filepath.Join(capDir, "claude-config")
		id := "01234567-89ab-cdef-0123-456789abcdef"
		// the native session dir is gone; the capsule-side copy (synced at
		// the dispatch's commit boundary) is the readable reference
		prev := filepath.Join(capDir, "session", "prev-claude.jsonl")
		writeCapsuleFile(t, capDir, "session/prev-claude.jsonl", `{"t":"turn"}`+"\n")
		writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
			`{"spawn_n":2,"step_id":"verification","transcript":"`+prev+`","harness":"claude","harness_session":"`+id+`","work_item":"wi_sp"}`+"\n")
		if _, err := ad.SpawnStep(context.Background(), rewindStepSpawn("claude")); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		if p.Resume || p.HarnessSession == id {
			t.Fatalf("a gone native session cannot be resumed: %+v", p)
		}
		if !strings.Contains(p.InitialPrompt, "Previous attempt (read-only reference)") || !strings.Contains(p.InitialPrompt, prev) {
			t.Fatalf("the fallback must carry the capsule copy as reference:\n%s", p.InitialPrompt)
		}
		if err := p.Journal(99, nowStampX()); err != nil {
			t.Fatalf("journal: %v", err)
		}
		if e := lastJournal(t, capDir); e.Resume {
			t.Fatalf("the fallback must be recorded fresh: %+v", e)
		}
	})
	t.Run("pi transcript gone degrades fresh", func(t *testing.T) {
		ad, sp, capDir := spawnEnv(t)
		writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
			`{"spawn_n":2,"step_id":"fix","transcript":"/does/not/exist.jsonl","harness":"pi","work_item":"wi_sp"}`+"\n")
		if _, err := ad.SpawnStep(context.Background(), rewindStepSpawn("")); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		if p.Resume || strings.Contains(p.TranscriptPath, "/does/not/") {
			t.Fatalf("an unreadable transcript cannot be resumed: %+v", p)
		}
		if strings.Contains(p.InitialPrompt, "read-only reference") {
			t.Fatalf("no reference may be named when no transcript is readable:\n%s", p.InitialPrompt)
		}
		if err := p.Journal(99, nowStampX()); err != nil {
			t.Fatalf("journal: %v", err)
		}
		if e := lastJournal(t, capDir); e.Resume {
			t.Fatalf("the fallback must be recorded fresh: %+v", e)
		}
	})
}

// TestSpawnStepWiresHarnessTranslatorOntoTheLink: the step process's
// stdout records reach the dispatch's tracker through the harness
// translator, and the process exit (code + stderr tail) is announced.
func TestSpawnStepWiresHarnessTranslatorOntoTheLink(t *testing.T) {
	capDir := t.TempDir()
	writeExtLane(t, capDir, nil)
	sp := &captureSpawner{}
	link := supervise.NewLink("wi_sp")
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:      WIInfo{ID: "wi_sp", CapsuleDir: capDir, BundleName: "autopilot-x", SessionHandle: "ab12cd34"},
		Spawner: sp, Link: link, PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", SpawnN: 1})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := sp.last()
	if p.OnRecord == nil || p.OnExit == nil {
		t.Fatal("the dispatch must hand the spawner its record tap and exit callback")
	}
	meta := run.(MetaRun).Meta()
	if meta.Harness != "pi" || meta.Granularity != supervise.GranularityToken || meta.PID != 4242 {
		t.Fatalf("run meta %+v", meta)
	}
	tr := link.Tracker(1)
	p.OnRecord(`{"type":"extension_ui_request"}`)
	if s := tr.Snapshot(time.Now()); s.WorkSignals != 0 || s.NoiseCount != 1 {
		t.Fatalf("UI noise must not count as work: %+v", s)
	}
	p.OnRecord(`{"type":"turn_start"}`)
	p.OnRecord(`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"401 unauthorized"}]}`)
	p.OnExit(spawn.ExitInfo{Code: 1, StderrTail: "fatal: auth failed"})
	s := tr.Snapshot(time.Now())
	if !s.TurnError || s.Exit == nil || s.Exit.Code != 1 {
		t.Fatalf("the tracker must hold the error turn and the exit: %+v", s)
	}
	// the driver's classification reads both: the last signal was an error turn
	_, h := sp.last()
	h.setDone()
	_, werr := run.Wait(context.Background())
	var ee *ExitedError
	if !errors.As(werr, &ee) || !ee.TurnError || !strings.Contains(ee.LastError, "401") {
		t.Fatalf("exit error %+v, want the error turn + its text", werr)
	}
	if cause, _, _ := classifyWaitFailure(werr); cause != FailExitModel {
		t.Fatalf("an error-turn exit is model-class, got %q", cause)
	}
}

// copySharedMemoryTemplates copies the shipped memory-duty templates
// into the capsule's workflows/_shared/, as materialization does.
func copySharedMemoryTemplates(t *testing.T, capDir string) {
	t.Helper()
	for _, name := range []string{"memory.md", "memory-wrap.md"} {
		raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, name))
		if err != nil {
			t.Fatal(err)
		}
		writeCapsuleFile(t, capDir, "workflows/"+workflow.SharedDir+"/"+name, string(raw))
	}
}

// w109RecallSteps is the owner-approved recall set: the
// decision/authoring steps carry the recall duty; the gates (review,
// verification, deliver, grill-review) and the release lane's steps do
// not. develop's build is the one decision/authoring step
// of its lane: it plans, authors the tests and implements.
var w109RecallSteps = map[string]bool{
	"grill": true, "spec": true, "plan": true, "repro": true,
	"root-cause": true, "e2e-author": true, "code": true, "fix": true,
	"build": true,
}

// TestPromptBindsCapsuleRelativePathsToAbsoluteRoot (polyforge#244): the
// prompt must not merely mention the capsule root — it must bind every
// capsule-relative path to it, so a step whose shell cwd drifted into
// repos/<name> still writes artifacts/<x> at the capsule. The contract's
// artifacts/ rule is rendered absolute.
func TestPromptBindsCapsuleRelativePathsToAbsoluteRoot(t *testing.T) {
	root := "/ws/capsules/demo-1"
	prompt := assembleStepPrompt(promptParts{
		WIID: "wi_x", StepID: "root-cause", SpawnN: 1, CapsuleRoot: root,
		Definition: "Write `artifacts/root-cause.md`.",
	})
	for _, want := range []string{
		"Capsule root: `" + root + "`",
		"resolves against this root, NEVER against your current working directory",
		"`artifacts/<name>` means `" + root + "/artifacts/<name>`",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt must bind capsule-relative paths to the root: missing %q", want)
		}
	}
	// the anchor precedes the step definition it governs
	if strings.Index(prompt, "Capsule root:") > strings.Index(prompt, "# Step definition") {
		t.Errorf("the capsule-root binding must come before the step definition")
	}
}

// ---------------------------------------------------------------------------
// The re-dispatch fast path.

// stubTreeHash swaps the package-level hashing seam for one test.
func stubTreeHash(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	old := treeHashFn
	t.Cleanup(func() { treeHashFn = old })
	treeHashFn = fn
}

// fastPathEnv is the adapter over a capsule whose spawn journal holds a
// readable previous pi transcript for the verification step.
func fastPathEnv(t *testing.T) (*StepSpawnAdapter, *captureSpawner, string, string) {
	t.Helper()
	ad, sp, capDir := spawnEnv(t)
	writeCapsuleFile(t, capDir, "artifacts/fix-summary.md", "s")
	prev := filepath.Join(capDir, "session", "prev.jsonl")
	writeCapsuleFile(t, capDir, "session/prev.jsonl", `{"t":"turn"}`+"\n")
	writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
		`{"spawn_n":3,"step_id":"verification","transcript":"`+prev+`","harness":"pi","work_item":"wi_sp"}`+"\n")
	return ad, sp, capDir, prev
}

func assertContinueInPlace(t *testing.T, prompt, ref string) {
	t.Helper()
	for _, want := range []string{
		"Previous attempt (read-only reference)",
		"byte-identical",
		"continue the previous attempt",
		"transcript tail",
		"environment setup",
		ref,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the fast-path prompt misses %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "start this attempt's work from scratch") {
		t.Errorf("the fast-path prompt still carries the start-from-scratch wording:\n%s", prompt)
	}
}

func assertColdStartReference(t *testing.T, prompt, ref string) {
	t.Helper()
	if !strings.Contains(prompt, ref) || !strings.Contains(prompt, "start this attempt's work from scratch") {
		t.Errorf("the prompt must carry today's from-scratch reference naming %q:\n%s", ref, prompt)
	}
}

// TestAssembleStepPromptContinueInPlace pins the two reference
// instructions: ContinueInPlace renders the fast-path paragraph, its
// absence renders today's sentence verbatim.
func TestAssembleStepPromptContinueInPlace(t *testing.T) {
	base := promptParts{
		WIID: "wi_x", StepID: "fix", SpawnN: 2, CapsuleRoot: "/c",
		Definition: "do it", Reference: "/c/session/prev.jsonl",
	}
	fast := base
	fast.ContinueInPlace = true
	assertContinueInPlace(t, assembleStepPrompt(fast), "/c/session/prev.jsonl")
	assertColdStartReference(t, assembleStepPrompt(base), "/c/session/prev.jsonl")
}

// TestSpawnStepFastPathDecision drives the adapter's decision: the fast
// path needs a new conversation, a death-triggered retry, a readable
// reference and a matching non-empty recorded hash; every other shape
// keeps today's wording.
func TestSpawnStepFastPathDecision(t *testing.T) {
	t.Run("fresh retry with matching hash continues in place", func(t *testing.T) {
		ad, sp, _, prev := fastPathEnv(t)
		stubTreeHash(t, func(string) (string, error) { return "h1", nil })
		if _, err := ad.SpawnStep(context.Background(), StepSpawn{
			StepID: "verification", SpawnN: 4, Fresh: true, RecordedTreeHash: "h1", RetryTriggered: true,
		}); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		if p.Resume {
			t.Fatalf("a fresh retry must not resume: %+v", p)
		}
		assertContinueInPlace(t, p.InitialPrompt, prev)
	})

	t.Run("the same claim's marker before the attempt continues in place", func(t *testing.T) {
		ad, sp, capDir, prev := fastPathEnv(t)
		writeCapsuleFile(t, capDir, materializeMarkerRel,
			`{"bundle_name":"b","targets":["x"],"completed_at":"2026-01-01T00:00:00Z"}`)
		stubTreeHash(t, func(string) (string, error) { return "h1", nil })
		if _, err := ad.SpawnStep(context.Background(), StepSpawn{
			StepID: "verification", SpawnN: 4, Fresh: true, RecordedTreeHash: "h1", RetryTriggered: true,
			RecordedAttemptAt: "2026-01-02T00:00:00Z",
		}); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		assertContinueInPlace(t, p.InitialPrompt, prev)
	})

	t.Run("a re-claimed capsule keeps today's wording", func(t *testing.T) {
		ad, sp, capDir, prev := fastPathEnv(t)
		// the fresh materialization postdates the recorded failure attempt:
		// a re-claim's ignored build directories did not survive, so the
		// fast path's "environment is intact" claim would be false.
		writeCapsuleFile(t, capDir, materializeMarkerRel,
			`{"bundle_name":"b","targets":["x"],"completed_at":"2026-01-03T00:00:00Z"}`)
		stubTreeHash(t, func(string) (string, error) { return "h1", nil })
		if _, err := ad.SpawnStep(context.Background(), StepSpawn{
			StepID: "verification", SpawnN: 4, Fresh: true, RecordedTreeHash: "h1", RetryTriggered: true,
			RecordedAttemptAt: "2026-01-02T00:00:00Z",
		}); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		assertColdStartReference(t, p.InitialPrompt, prev)
	})

	t.Run("degraded resume with matching hash continues in place", func(t *testing.T) {
		ad, sp, _, prev := fastPathEnv(t)
		stubTreeHash(t, func(string) (string, error) { return "h1", nil })
		if _, err := ad.SpawnStep(context.Background(), StepSpawn{
			StepID: "verification", SpawnN: 4, Resume: true, Harness: harness.Claude,
			RecordedTreeHash: "h1", RetryTriggered: true,
		}); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		if p.Resume {
			t.Fatalf("the cross-harness resume must degrade to a new conversation: %+v", p)
		}
		assertContinueInPlace(t, p.InitialPrompt, prev)
	})

	t.Run("hash mismatch keeps today's wording", func(t *testing.T) {
		ad, sp, _, prev := fastPathEnv(t)
		stubTreeHash(t, func(string) (string, error) { return "h2", nil })
		if _, err := ad.SpawnStep(context.Background(), StepSpawn{
			StepID: "verification", SpawnN: 4, Fresh: true, RecordedTreeHash: "h1", RetryTriggered: true,
		}); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		assertColdStartReference(t, p.InitialPrompt, prev)
	})

	t.Run("missing recorded hash keeps today's wording", func(t *testing.T) {
		ad, sp, _, prev := fastPathEnv(t)
		stubTreeHash(t, func(string) (string, error) { return "h1", nil })
		if _, err := ad.SpawnStep(context.Background(), StepSpawn{
			StepID: "verification", SpawnN: 4, Fresh: true, RetryTriggered: true,
		}); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		assertColdStartReference(t, p.InitialPrompt, prev)
	})

	t.Run("an answer continuation keeps today's wording", func(t *testing.T) {
		ad, sp, _, prev := fastPathEnv(t)
		stubTreeHash(t, func(string) (string, error) { return "h1", nil })
		if _, err := ad.SpawnStep(context.Background(), StepSpawn{
			StepID: "verification", SpawnN: 4, Fresh: true, RecordedTreeHash: "h1", RetryTriggered: false,
		}); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		assertColdStartReference(t, p.InitialPrompt, prev)
	})

	t.Run("a hash error keeps today's wording", func(t *testing.T) {
		ad, sp, _, prev := fastPathEnv(t)
		stubTreeHash(t, func(string) (string, error) { return "", errors.New("boom") })
		if _, err := ad.SpawnStep(context.Background(), StepSpawn{
			StepID: "verification", SpawnN: 4, Fresh: true, RecordedTreeHash: "h1", RetryTriggered: true,
		}); err != nil {
			t.Fatalf("SpawnStep: %v", err)
		}
		p, _ := sp.last()
		assertColdStartReference(t, p.InitialPrompt, prev)
	})
}

// TestCapsuleReclaimed pins the re-claim detection ( owner
// ruling): a materialization marker that postdates the recorded failure
// attempt is a re-claim (the fast path is refused); a marker that
// predates it is the same claim; a missing or unparsable marker fails
// closed; no recorded stamp is not a re-claim.
func TestCapsuleReclaimed(t *testing.T) {
	cases := []struct {
		name       string
		recordedAt string
		marker     string // "" = no marker file
		want       bool
	}{
		{"no recorded stamp is not a re-claim", "", "2026-01-01T00:00:00Z", false},
		{"marker before the attempt is the same claim", "2026-01-02T00:00:00Z", "2026-01-01T00:00:00Z", false},
		{"marker after the attempt is a re-claim", "2026-01-02T00:00:00Z", "2026-01-03T00:00:00Z", true},
		{"missing marker fails closed", "2026-01-02T00:00:00Z", "", true},
		{"unparsable marker fails closed", "2026-01-02T00:00:00Z", "not-a-stamp", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if c.marker != "" {
				writeCapsuleFile(t, root, materializeMarkerRel,
					`{"bundle_name":"b","targets":["x"],"completed_at":"`+c.marker+`"}`)
			}
			if got := capsuleReclaimed(root, c.recordedAt); got != c.want {
				t.Fatalf("capsuleReclaimed(%q, %q) = %v, want %v", root, c.recordedAt, got, c.want)
			}
		})
	}
}

// w109DispatchPrompt dispatches one step of a shipped bundle through the
// spawn adapter over a capsule carrying the materialized bundle, the
// whole shared material (step definitions, fragments, memory templates)
// and a work item with a project, and answers the assembled prompt.
func w109DispatchPrompt(t *testing.T, bundle, stepID string, done map[string]bool) string {
	t.Helper()
	p, err := w109Dispatch(t, w109Capsule(t, bundle), bundle, StepSpawn{StepID: stepID, SpawnN: 1})
	if err != nil {
		t.Fatalf("SpawnStep %s/%s: %v", bundle, stepID, err)
	}
	return p
}

// w109Capsule materializes a shipped bundle, the whole shared material
// and a work item with a project into a fresh capsule.
func w109Capsule(t *testing.T, bundle string) string {
	t.Helper()
	capDir := capsuleWithShippedBundle(t, bundle)
	root := workflowsRootFor(t, bundle)
	shared, _ := filepath.Glob(filepath.Join(root, workflow.SharedDir, "*.md"))
	stepDefs, _ := filepath.Glob(filepath.Join(root, workflow.SharedDir, "steps", "*.md"))
	for _, p := range append(shared, stepDefs...) {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, p)
		writeCapsuleFile(t, capDir, "workflows/"+filepath.ToSlash(rel), string(raw))
	}
	writeCapsuleFile(t, capDir, capsule.WorkItemJSON, `{"id":"wi_w109","slug":"demo#9","project":"demo","goal":"g"}`)
	return capDir
}

// w109Dispatch runs one SpawnStep over capDir and answers the assembled
// prompt, or the dispatch error.
func w109Dispatch(t *testing.T, capDir, bundle string, spawn StepSpawn) (string, error) {
	t.Helper()
	sp := &captureSpawner{}
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:           WIInfo{ID: "wi_w109", CapsuleDir: capDir, BundleName: bundle, SessionHandle: "ab12cd34"},
		Spawner:      sp,
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	if _, err := ad.SpawnStep(context.Background(), spawn); err != nil {
		return "", err
	}
	p, _ := sp.last()
	return p.InitialPrompt, nil
}
