package driver

// prompt_c4_test.go (workflow contract 4, design #483 §8): every dispatch's
// prompt is exactly the six fixed parts, in order — (1) the dispatch: its
// identity and this dispatch's facts (a pi dispatch runs no sub-agent;
// where raw downloads go), (2) the prohibited actions, (3) the step header
// and the hand-off body template, (4) the step definition, (5) the skill,
// (6) on a continuation, what it continues from — and nothing else (no
// inputs, verify-first, output schema, rewind input, memories or delivery
// contract). Part 1 names the dispatch's agent language.
// The snapshots pin the whole text, one per language
// (PF_WRITE_PROMPT_GOLDEN=1 rewrites them).

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// topHeadings answers the prompt's level-1 headings, in order (fenced
// blocks skipped).
func topHeadings(prompt string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(line, "# ") {
			out = append(out, line)
		}
	}
	return out
}

// c4PromptEnv is the probe lane with a skill on fix and fix's definition
// including the sub-agent rules (so a pi dispatch carries the fact).
func c4PromptEnv(t *testing.T) (*StepSpawnAdapter, *captureSpawner, string) {
	t.Helper()
	ad, sp, capDir := spawnEnv(t)
	writeCapsuleFile(t, capDir, "workflows/"+workflow.SharedDir+"/build_subagents.md", "Dispatch sub-agents for independent units.")
	writeCapsuleFile(t, capDir, "workflows/autopilot-x/steps/fix.md", extDef("fix", "fix: implement the micro-spec\n{{build_subagents}}"))
	writeCapsuleFile(t, capDir, "workflows/"+workflow.SharedDir+"/skills/tidy.md", "---\nname: tidy\ndescription: tidy\ncommands: [pf-run]\n---\n\n1. Run `pf-run --json --out artifacts/run/result.json` as written.\n")
	lane := filepath.Join(capDir, "workflows", "autopilot-x", workflow.WorkflowFile)
	raw, err := os.ReadFile(lane)
	if err != nil {
		t.Fatal(err)
	}
	writeCapsuleFile(t, capDir, "workflows/autopilot-x/"+workflow.WorkflowFile, strings.Replace(string(raw), "  - id: fix\n", "  - id: fix\n    skill: tidy\n", 1))
	wf, err := LoadWorkflow(filepath.Join(capDir, "workflows", "autopilot-x"))
	if err != nil {
		t.Fatal(err)
	}
	ad.Workflow = wf
	return ad, sp, capDir
}

func TestPromptIsTheSixFixedParts(t *testing.T) {
	ad, sp, capDir := c4PromptEnv(t)
	writeCapsuleFile(t, capDir, capsule.RecalledMemoriesJSON, `{"items":[{"id":"mem_1","type":"doc","title":"a recalled memory"}]}`)
	msg := capsule.AskLine{At: "2026-11-11T10:00:00.000000001Z", Step: "fix", Spawn: 3, Kind: capsule.AskKindMessage, Text: "keep the tests", By: "owner"}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "fix", SpawnN: 4, Round: 2, Harness: harness.Pi,
		PriorEndedWithoutResult: true, OwnerMessages: []capsule.AskLine{msg}}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	prompt := p.InitialPrompt
	want := []string{dispatchHeading, "# Prohibited actions", "# How a step runs (probe)", "# The hand-off body (probe)", definitionHeading, skillHeading, continuationHeading}
	if got := topHeadings(prompt); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the prompt's parts:\n%s\nwant exactly:\n%s\n--- prompt ---\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), prompt)
	}
	part1 := prompt[:strings.Index(prompt, "# Prohibited actions")]
	for _, fact := range []string{
		"You are step `fix` of polyforge work item `wi_sp`: round 2, dispatch 4",
		"Capsule root: `" + capDir + "`",
		"Raw downloads (CI artifacts, traces, screenshots and video bundles) go under `" + capsule.DirDownloads + "/`",
		piNoSubagents,
	} {
		if !strings.Contains(part1, fact) {
			t.Errorf("part 1 lacks the dispatch fact %q:\n%s", fact, part1)
		}
	}
	part6 := prompt[strings.Index(prompt, continuationHeading):]
	for _, w := range []string{"## Owner messages", "keep the tests", "## Your previous turn ended without `pf step handoff`"} {
		if !strings.Contains(part6, w) {
			t.Errorf("part 6 lacks %q:\n%s", w, part6)
		}
	}
	for _, gone := range []string{"a recalled memory", "# Inputs", "# Verify first", "# Output schema", "# Result delivery", "pf result submit", "envelope", "Context policy", "# Project memory"} {
		if strings.Contains(prompt, gone) {
			t.Errorf("the prompt carries %q, which contract 4 removed", gone)
		}
	}
	// the default language: the English instruction, in part 1
	assertLanguageSentence(t, prompt, c4LanguageEN, c4LanguageZhCN)
	assertPromptSnapshot(t, prompt, capDir, "prompt_c4.golden")
}

// The two language sentences part 1 carries, pinned here
// and by the snapshots.
const (
	c4LanguageEN = "Language: English. Work in English throughout this step: your replies and narration, every question to the owner, " +
		"the prose of the files you write, and your hand-off body under the English headings of the hand-off body template below."
	c4LanguageZhCN = "Language: Simplified Chinese (zh-CN). Work in Simplified Chinese (简体中文) throughout this step: your replies and narration, " +
		"every question to the owner, the prose of the files you write, and your hand-off body under the Chinese headings of the hand-off body template below. " +
		"Code, commands, identifiers and commit messages follow the project's conventions."
)

// assertLanguageSentence: part 1 (before the prohibited actions) carries
// the want sentence and the prompt carries no other language's.
func assertLanguageSentence(t *testing.T, prompt, want, other string) {
	t.Helper()
	cut := strings.Index(prompt, "# Prohibited actions")
	if cut < 0 {
		t.Fatalf("the prompt has no prohibited actions part:\n%s", prompt)
	}
	if !strings.Contains(prompt[:cut], want) {
		t.Errorf("part 1 lacks the language instruction %q:\n%s", want, prompt[:cut])
	}
	if strings.Contains(prompt, other) {
		t.Errorf("the prompt carries the other language's instruction %q", other)
	}
}

// assertPromptSnapshot compares the whole prompt, the capsule root
// normalized, with testdata/<name>.
func assertPromptSnapshot(t *testing.T, prompt, capDir, name string) {
	t.Helper()
	got := strings.ReplaceAll(prompt, capDir, "<capsule>")
	golden := filepath.Join("testdata", name)
	if os.Getenv("PF_WRITE_PROMPT_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wantRaw, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("the prompt snapshot %s: %v (PF_WRITE_PROMPT_GOLDEN=1 writes it)", golden, err)
	}
	if got != string(wantRaw) {
		t.Fatalf("the prompt differs from its snapshot %s (PF_WRITE_PROMPT_GOLDEN=1 rewrites it):\n%s", golden, got)
	}
}

// AC2: the same dispatch under `zh-CN` carries the Chinese instruction in
// part 1, and nothing else changes but that sentence (its snapshot).
func TestPromptLanguageInstructionZhCN(t *testing.T) {
	ad, sp, capDir := c4PromptEnv(t)
	ad.Language = func() string { return "zh-CN" }
	msg := capsule.AskLine{At: "2026-11-11T10:00:00.000000001Z", Step: "fix", Spawn: 3, Kind: capsule.AskKindMessage, Text: "keep the tests", By: "owner"}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "fix", SpawnN: 4, Round: 2, Harness: harness.Pi,
		PriorEndedWithoutResult: true, OwnerMessages: []capsule.AskLine{msg}}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	assertLanguageSentence(t, p.InitialPrompt, c4LanguageZhCN, c4LanguageEN)
	assertPromptSnapshot(t, p.InitialPrompt, capDir, "prompt_c4_zh-CN.golden")
}

// A first dispatch has no continuation part; a claude dispatch carries no
// sub-agent fact; a step without a skill has no skill part.
func TestPromptPartsOfAFirstDispatch(t *testing.T) {
	ad, sp, _ := c4PromptEnv(t)
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", SpawnN: 1, Round: 1, Harness: harness.Claude}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	want := []string{dispatchHeading, "# Prohibited actions", "# How a step runs (probe)", "# The hand-off body (probe)", definitionHeading}
	if got := topHeadings(p.InitialPrompt); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("a first dispatch's parts %v, want %v", got, want)
	}
	if strings.Contains(p.InitialPrompt, piNoSubagents) {
		t.Fatal("a claude dispatch of a step without the sub-agent rules carries no pi fact")
	}
}

// The step header is part of every dispatch: a lane snapshot without it
// fails the dispatch closed, naming the file.
func TestPromptStepHeaderMissingFailsTheDispatch(t *testing.T) {
	ad, _, capDir := c4PromptEnv(t)
	if err := os.Remove(filepath.Join(capDir, "workflows", workflow.SharedDir, "handoff.md")); err != nil {
		t.Fatal(err)
	}
	_, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", SpawnN: 1})
	if err == nil || !regexp.MustCompile(`step header _shared/handoff\.md`).MatchString(err.Error()) {
		t.Fatalf("a missing hand-off template must fail the dispatch naming it, got %v", err)
	}
}
