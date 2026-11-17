package driver

// pf101_concise_rule_test.go (polyforge#101, AC3): every step dispatch's
// prompt carries the concise output-discipline rule (harness.ConciseRule
// — the one source) exactly once — under the prohibited actions, ahead
// of the step definition — for every shipped lane's every step, on pi
// and on claude, on a first dispatch, a fresh retry and a resume that
// degraded to a new conversation. The two short resumes (the ended-
// without-result reminder, the owner-message relay) re-send no part of
// the full prompt, so they carry no second copy. The rule rides part 2
// as its closing `## ` subsection: the six level-1 headings of workflow
// contract 4 stay six.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
)

// pf101RulePosition asserts the rule rides the prompt exactly once,
// between the prohibited actions and the step definition.
func pf101RulePosition(t *testing.T, key, prompt string) {
	t.Helper()
	rule := harness.ConciseRule()
	if n := strings.Count(prompt, rule); n != 1 {
		t.Errorf("%s: the dispatched prompt carries the concise rule %d times, want 1", key, n)
	}
	prohibited := strings.Index(prompt, "# Prohibited actions")
	definition := strings.Index(prompt, definitionHeading)
	at := strings.Index(prompt, rule)
	if prohibited < 0 || definition < 0 || at < 0 {
		t.Fatalf("%s: the prompt lacks a part (prohibited=%d definition=%d rule=%d)", key, prohibited, definition, at)
	}
	if at < prohibited || at > definition {
		t.Errorf("%s: the rule belongs after the prohibited actions and before the step definition, at %d (prohibited %d, definition %d)", key, at, prohibited, definition)
	}
}

// TestPF101StepPromptCarriesConciseRuleOnce: the three full-prompt
// dispatch shapes, every shipped lane's every step, both harnesses —
// and the six level-1 headings stay six (the rule is part 2's own
// closing `## ` subsection, not a seventh part).
func TestPF101StepPromptCarriesConciseRuleOnce(t *testing.T) {
	for _, dir := range shippedBundleDirs(t) {
		bundle := filepath.Base(dir)
		wf, err := LoadWorkflow(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, s := range wf.Steps {
			for _, h := range []string{harness.Pi, harness.Claude} {
				// the first dispatch
				prompt := spawnStepPromptWith(t, bundle, StepSpawn{StepID: s.ID, SpawnN: 1, Harness: h})
				pf101RulePosition(t, bundle+"/"+s.ID+"/"+h+" first", prompt)
				// a fresh retry: a NEW conversation (a 2nd+ failure, a
				// candidate switch) — the full prompt again
				prompt = spawnStepPromptWith(t, bundle, StepSpawn{StepID: s.ID, SpawnN: 2, Harness: h, Fresh: true, RetryTriggered: true})
				pf101RulePosition(t, bundle+"/"+s.ID+"/"+h+" fresh retry", prompt)
			}
		}
	}
	// the six level-1 headings of workflow contract 4 stay six
	if prompt := spawnStepPrompt(t, "develop", "build"); len(topHeadings(prompt)) != 6 {
		t.Errorf("the prompt's level-1 headings = %d, want the contract's six:\n%v", len(topHeadings(prompt)), topHeadings(prompt))
	}
}

// TestPF101DegradedResumeCarriesTheRuleOnce: a resume whose previous
// session is gone (no journal line) degrades to a new conversation and
// sends the full prompt — the rule exactly once.
func TestPF101DegradedResumeCarriesTheRuleOnce(t *testing.T) {
	ad, sp, _ := spawnEnv(t)
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 1, Harness: harness.Pi}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	// no journal entry: the previous session is gone, so the resume
	// degrades to a new conversation with the full prompt
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 2, Harness: harness.Claude,
		Resume: true, RetryTriggered: true}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	if p.Resume {
		t.Fatalf("the dispatch must degrade to a new conversation: %+v", p)
	}
	pf101RulePosition(t, "degraded resume", p.InitialPrompt)
}

// TestPF101ShortResendsCarryNoRule: the two short resumes re-send no
// part of the full prompt — neither copy of the rule.
func TestPF101ShortResendsCarryNoRule(t *testing.T) {
	rule := harness.ConciseRule()
	ad, sp, capDir := spawnEnv(t)
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 1, Harness: harness.Pi}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p1, _ := sp.last()
	writeCapsuleFile(t, capDir, strings.TrimPrefix(p1.TranscriptPath, capDir+"/"), "{\"type\":\"user\"}\n")
	if err := ad.appendSpawnJournal(p1, "grill", "tok1", "", 1, "2026-10-25T00:00:00Z"); err != nil {
		t.Fatalf("appendSpawnJournal: %v", err)
	}
	// the EWR short reminder
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 2, Harness: harness.Pi,
		Resume: true, PriorEndedWithoutResult: true, RetryTriggered: true}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p2, _ := sp.last()
	if !p2.Resume {
		t.Fatalf("the EWR retry must resume the same session: %+v", p2)
	}
	if strings.Contains(p2.InitialPrompt, rule) {
		t.Errorf("the EWR short reminder carries a copy of the rule:\n%s", p2.InitialPrompt)
	}
	// the owner-message relay alone
	msg := capsule.AskLine{At: "2026-11-17T10:00:00.000000001Z", Step: "grill", Spawn: 2, Kind: capsule.AskKindMessage, Text: "keep the tests", By: "owner"}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", WIID: "wi_sp", SpawnN: 3, Harness: harness.Pi,
		Resume: true, MessageResume: true, OwnerMessages: []capsule.AskLine{msg}}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p3, _ := sp.last()
	if strings.Contains(p3.InitialPrompt, rule) {
		t.Errorf("the message-relay resume carries a copy of the rule:\n%s", p3.InitialPrompt)
	}
}
