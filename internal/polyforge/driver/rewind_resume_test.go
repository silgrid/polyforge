package driver

// rewind_resume_test.go: every gate→target rewind re-enters a non-gate
// target as a resume of its previous round — structural findings
// included — on the same candidate; the resume degrades to a new
// conversation only when the session is gone, the harness changed or the
// previous dispatch handed nothing off; either way the dispatch reads the
// findings in artifacts/handoff.md (workflow contract 4).

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

const structuralVerdict = `{"verdict":"fail","fail_reason":"the retry loop never backs off","findings":[{"severity":"blocking","repair_target":"code","detail":"the retry loop never backs off"}]}`

// structuralRewind drives build (on cand) → review FAIL with a structural
// finding on a two-candidate chain; the build round's dispatch ran on
// cand (the second candidate models a failover inside that round).
func structuralRewind(t *testing.T, cand string) (*Driver, string) {
	t.Helper()
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec(reviewStepID, "build"))
	d.cands = map[string][]string{"build": {"pi:prov/m1", "claude:opus"}}
	mustInit(t, d)
	if err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: "build", Model: "m", Candidate: cand, SpawnN: 1}); err != nil {
		t.Fatal(err)
	}
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 1,
		Result: completedResult("build", 1, `{"summary":"round one: added the retry loop"}`)}); err != nil {
		t.Fatal(err)
	}
	mustStart(t, d, reviewStepID, 2)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: reviewStepID, SpawnN: 2,
		Result: &StepResult{StepID: reviewStepID, SpawnN: 2, Outcome: OutcomeGateFailed, Output: json.RawMessage(structuralVerdict)}}); err != nil {
		t.Fatal(err)
	}
	return d, execPath
}

func TestRewindResumeKeepsTheCandidate(t *testing.T) {
	d, execPath := structuralRewind(t, "claude:opus")
	x := readExec(t, execPath)
	if got := x.Steps["build"]; got.Status != capsule.StepPending || got.Candidate != "claude:opus" {
		t.Fatalf("a resuming target keeps its candidate across the rewind: %+v", got)
	}
	sp, _, err := d.prepareDispatch("build")
	if err != nil {
		t.Fatal(err)
	}
	if !sp.Resume || !sp.RewindResume || sp.Fresh {
		t.Fatalf("a structural review FAIL re-enters its target as a rewind resume: %+v", sp)
	}
	if sp.Candidate != "claude:opus" || sp.Harness != "claude" {
		t.Fatalf("the re-entry stays on the candidate (and harness) the round ran on: %q %q", sp.Candidate, sp.Harness)
	}
}

// Negative: a gate target never resumes, and its candidate resets.
func TestRewindIntoAGateReentersFresh(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("check", "build"), gateSpec("audit", "check"))
	d.cands = map[string][]string{"check": {"pi:prov/m1", "claude:opus"}}
	mustInit(t, d)
	mustStart(t, d, "build", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 1, Result: completedResult("build", 1, `{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: "check", Model: "m", Candidate: "claude:opus", SpawnN: 2}); err != nil {
		t.Fatal(err)
	}
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "check", SpawnN: 2, Result: completedResult("check", 2, `{"verdict":"pass"}`)}); err != nil {
		t.Fatal(err)
	}
	mustStart(t, d, "audit", 3)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "audit", SpawnN: 3,
		Result: &StepResult{StepID: "audit", SpawnN: 3, Outcome: OutcomeGateFailed, Output: json.RawMessage(structuralVerdict)}}); err != nil {
		t.Fatal(err)
	}
	if c := readExec(t, execPath).Steps["check"].Candidate; c != "" {
		t.Fatalf("a gate target's pointer returns to the list head: %q", c)
	}
	sp, _, err := d.prepareDispatch("check")
	if err != nil {
		t.Fatal(err)
	}
	if sp.Resume || sp.RewindResume {
		t.Fatalf("a judge never resumes the judgment its verdict closed: %+v", sp)
	}
}

// rewindReentrySpawn is the adapter-side order for build's rewind
// re-entry (spawn 3) after a structural FAIL.
func rewindReentrySpawn(harnessID string) StepSpawn {
	return StepSpawn{StepID: "fix", SpawnN: 3, Resume: true, RewindResume: true, Harness: harnessID}
}

func TestSpawnStepRewindReentryResumesOrDegradesWithFindings(t *testing.T) {
	journal := func(t *testing.T, capDir, transcript, h string) {
		t.Helper()
		writeCapsuleFile(t, capDir, capsule.DirRuntimeState+"/spawn-journal.jsonl",
			`{"spawn_n":2,"step_id":"fix","transcript":"`+transcript+`","harness":"`+h+`","work_item":"wi_sp"}`+"\n")
	}
	delivered := func(t *testing.T, capDir string) {
		t.Helper()
		appendLedgerHandoff(t, capDir, "fix", 2, stepcall.StatusDone)
	}
	// workflow contract 4: the findings are the gate's section of
	// artifacts/handoff.md, which the step header tells every dispatch to
	// read first — the prompt carries no rewind input of its own
	requireFindings := func(t *testing.T, prompt string) {
		t.Helper()
		if !strings.Contains(prompt, probeStepHeader) || strings.Contains(prompt, "# Rewind input") || strings.Contains(prompt, "the retry loop never backs off") {
			t.Fatalf("the dispatch reads the findings in handoff.md, never from a prompt section:\n%s", prompt)
		}
	}

	t.Run("session present resumes", func(t *testing.T) {
		ad, sp, capDir := spawnEnv(t)
		grillDone(t, capDir)
		prev := filepath.Join(capDir, "session", "prev.jsonl")
		writeCapsuleFile(t, capDir, "session/prev.jsonl", `{"t":"turn"}`+"\n")
		journal(t, capDir, prev, "pi")
		delivered(t, capDir)
		if _, err := ad.SpawnStep(context.Background(), rewindReentrySpawn("")); err != nil {
			t.Fatal(err)
		}
		p, _ := sp.last()
		if !p.Resume || p.TranscriptPath != prev {
			t.Fatalf("a structural rewind re-entry resumes the previous round: resume=%v transcript=%q", p.Resume, p.TranscriptPath)
		}
		requireFindings(t, p.InitialPrompt)
		if strings.Contains(p.InitialPrompt, "# Your previous round") {
			t.Fatalf("a resumed round already holds its own account:\n%s", p.InitialPrompt)
		}
	})
	t.Run("session gone degrades with findings and the prior round", func(t *testing.T) {
		ad, sp, capDir := spawnEnv(t)
		grillDone(t, capDir)
		journal(t, capDir, "/does/not/exist.jsonl", "pi")
		delivered(t, capDir)
		if _, err := ad.SpawnStep(context.Background(), rewindReentrySpawn("")); err != nil {
			t.Fatal(err)
		}
		p, _ := sp.last()
		if p.Resume {
			t.Fatalf("a gone session cannot be resumed: %+v", p)
		}
		requireFindings(t, p.InitialPrompt)
		if strings.Contains(p.InitialPrompt, "# Your previous round") || strings.Contains(p.InitialPrompt, "round one: added the retry loop") {
			t.Fatalf("the prior round is handoff.md's, not the prompt's:\n%s", p.InitialPrompt)
		}
	})
	t.Run("another harness degrades", func(t *testing.T) {
		ad, sp, capDir := spawnEnv(t)
		grillDone(t, capDir)
		prev := filepath.Join(capDir, "session", "prev.jsonl")
		writeCapsuleFile(t, capDir, "session/prev.jsonl", `{"t":"turn"}`+"\n")
		journal(t, capDir, prev, "claude")
		delivered(t, capDir)
		if _, err := ad.SpawnStep(context.Background(), rewindReentrySpawn("")); err != nil {
			t.Fatal(err)
		}
		p, _ := sp.last()
		if p.Resume {
			t.Fatalf("another harness's session cannot be resumed: %+v", p)
		}
		requireFindings(t, p.InitialPrompt)
		if !strings.Contains(p.InitialPrompt, prev) {
			t.Fatalf("the degraded dispatch carries the round's transcript as reference:\n%s", p.InitialPrompt)
		}
	})
	t.Run("previous dispatch delivered no result degrades", func(t *testing.T) {
		ad, sp, capDir := spawnEnv(t)
		grillDone(t, capDir)
		prev := filepath.Join(capDir, "session", "prev.jsonl")
		writeCapsuleFile(t, capDir, "session/prev.jsonl", `{"t":"turn"}`+"\n")
		journal(t, capDir, prev, "pi") // no archived result for s2
		if _, err := ad.SpawnStep(context.Background(), rewindReentrySpawn("")); err != nil {
			t.Fatal(err)
		}
		p, _ := sp.last()
		if p.Resume || p.TranscriptPath == prev {
			t.Fatalf("a conversation that ended without a result is not resumed: %+v", p)
		}
		requireFindings(t, p.InitialPrompt)
		if err := p.Journal(99, nowStampX()); err != nil {
			t.Fatal(err)
		}
		if e := lastJournal(t, capDir); e.Resume {
			t.Fatalf("the degradation is journaled fresh: %+v", e)
		}
	})
	// Negative control: an ordinary resume (an answer continuation, no
	// rewind) never applies the delivered-result condition.
	t.Run("ordinary resume ignores the delivered-result condition", func(t *testing.T) {
		ad, sp, capDir := spawnEnv(t)
		grillDone(t, capDir)
		prev := filepath.Join(capDir, "session", "prev.jsonl")
		writeCapsuleFile(t, capDir, "session/prev.jsonl", `{"t":"turn"}`+"\n")
		journal(t, capDir, prev, "pi")
		if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "fix", SpawnN: 3, Resume: true}); err != nil {
			t.Fatal(err)
		}
		if p, _ := sp.last(); !p.Resume {
			t.Fatalf("a crash retry resumes its own dying conversation as before: %+v", p)
		}
	})
}

// A Run-driven review FAIL re-enters its target as a rewind resume.
func TestRunStructuralRewindReentryResumes(t *testing.T) {
	d, sp, _ := runEnv(t, spec("build"), gateSpec(reviewStepID, "build"))
	sp.script = []fakeEntry{
		done(`{"summary":"round one"}`),
		gateFAIL(),
		done(`{"summary":"round two"}`),
		done(`{"verdict":"pass"}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	re := sp.spawnAt(2)
	if re.StepID != "build" || !re.Resume || !re.RewindResume {
		t.Fatalf("the structural FAIL's re-entry resumes: %+v", re)
	}
}
