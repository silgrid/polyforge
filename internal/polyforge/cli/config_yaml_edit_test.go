package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

func TestEditSummarizeYAMLReplacesOnlyTheTargetLine(t *testing.T) {
	before := "# operator comment\nsummarize:\n  models: [\"pi:zeta\", \"pi:alpha\"]\n  interval_sec: 30\n\nsteps:\n  e2e:\n    default: { models: [\"pi:zeta\"] }\n"
	after := editSummarizeYAML(before, "summarize.interval_sec", "10", true)
	if after == before {
		t.Fatal("the edit did not land")
	}
	if n := diffLineCount(before, after); n != 1 {
		t.Fatalf("expected exactly one changed line, got %d:\n%s", n, after)
	}
	for _, keep := range []string{"# operator comment", "models: [\"pi:zeta\", \"pi:alpha\"]", "steps:", "default: { models: [\"pi:zeta\"] }"} {
		if !strings.Contains(after, keep) {
			t.Fatalf("edit dropped %q:\n%s", keep, after)
		}
	}
}

func TestEditSummarizeYAMLUnsetModelsRemovesTheEntry(t *testing.T) {
	before := "summarize:\n  models: [\"pi:a\"]\n  language: \"English\"\n\nsteps:\n  e2e:\n"
	after := editSummarizeYAML(before, "summarize.models", "", false)
	if strings.Contains(after, "summarize:") || strings.Contains(after, "models:") {
		t.Fatalf("unset models must remove the whole entry:\n%s", after)
	}
	if !strings.Contains(after, "steps:") {
		t.Fatalf("the rest of the file must survive:\n%s", after)
	}
}

func diffLineCount(a, b string) int {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	n := 0
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			n++
		}
	}
	return n
}

// TestWorkspaceLockStaysLayoutClean pins that the summarize writer's flock
// lives at a dot-entry `pf doctor` tolerates: `.polyforge.yaml.lock` would be
// flagged as a disposable backup, so the lock must not use that prefix.
func TestWorkspaceLockStaysLayoutClean(t *testing.T) {
	ws := t.TempDir()
	yamlPath := filepath.Join(ws, workspace.FileName)
	if err := os.WriteFile(yamlPath, []byte("summarize:\n  models: [\"pi:a\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := lockConfigFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	unlockConfigFile(lock)
	if _, err := os.Stat(filepath.Join(ws, ".polyforge.lock")); err != nil {
		t.Fatalf("the workspace lock must be the tolerated .polyforge.lock: %v", err)
	}
	if _, err := os.Stat(yamlPath + ".lock"); err == nil {
		t.Fatal("the workspace lock must not be the doctor-flagged <yaml>.lock")
	}
	strays, err := workspace.StrayEntries(ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range strays {
		if strings.HasPrefix(s.Path, workspace.FileName+".") || s.Path == "polyforge.lock" {
			t.Fatalf("the lock must not be a stray entry: %v", strays)
		}
	}
}

// denseStepsYAML is a comment-dense workspace yaml the step editor must
// keep byte-identical outside the addressed line.
const denseStepsYAML = `# header comment
summarize:
  models: ["pi:s"]
  # summarize comment
steps:
  alpha:
    # grill comment
    grill:
      models: ["pi:before"]
      attrs:
        question_batch_size: 2
    # code comment
    code:
      models: ["pi:code"]
    default: { models: ["pi:d"] }
memory_policy:
  default: { record: [a] }
`

// TestStepsChainWritePreservesBytes pins the byte-preserving step editor:
// a chain edit changes exactly its line; an inserted entry adds lines but
// rewrites none.
func TestStepsChainWritePreservesBytes(t *testing.T) {
	after := editStepsYAML(denseStepsYAML, stepKey{project: "alpha", step: "grill"}, `["claude:x", "pi:y"]`, true)
	if n := diffLineCount(denseStepsYAML, after); n != 1 || !strings.Contains(after, `      models: ["claude:x", "pi:y"]`) {
		t.Fatalf("want exactly the grill models line changed, got %d:\n%s", n, after)
	}
	added := editStepsYAML(denseStepsYAML, stepKey{project: "beta", step: "fix"}, `["pi:f"]`, true)
	if !strings.HasPrefix(added, strings.TrimSuffix(denseStepsYAML[:strings.Index(denseStepsYAML, "memory_policy:")], "")) {
		t.Fatalf("a new project must be appended inside the steps block without rewriting it:\n%s", added)
	}
	if !strings.Contains(added, "  beta:\n    fix:\n      models: [\"pi:f\"]\nmemory_policy:") {
		t.Fatalf("new entry not created in block style inside steps:\n%s", added)
	}
	attr := editStepsYAML(denseStepsYAML, stepKey{project: "alpha", step: "grill", attr: "question_batch_size"}, "3", true)
	if n := diffLineCount(denseStepsYAML, attr); n != 1 || !strings.Contains(attr, "        question_batch_size: 3\n") {
		t.Fatalf("an attr edit must change only its line (%d):\n%s", n, attr)
	}
	fresh := editStepsYAML("", stepKey{project: "alpha", step: "grill", attr: "question_batch_size"}, "1", true)
	if fresh != "steps:\n  alpha:\n    grill:\n      attrs:\n        question_batch_size: 1\n" {
		t.Fatalf("an empty file must gain a block-style entry, got:\n%q", fresh)
	}
}

// TestStepsFlowEntryExpandsOnce pins that a flow-style step entry is
// expanded into block style keeping every value it declares.
func TestStepsFlowEntryExpandsOnce(t *testing.T) {
	after := editStepsYAML(denseStepsYAML, stepKey{project: "alpha", step: "default"}, `["pi:new"]`, true)
	if !strings.Contains(after, "    default:\n      models: [\"pi:new\"]\nmemory_policy:") {
		t.Fatalf("flow default entry not expanded and set:\n%s", after)
	}
	kept := editStepsYAML("steps:\n  alpha:\n    grill: { models: [pi:a], attrs: { question_batch_size: 2 } }\n", stepKey{project: "alpha", step: "grill"}, `["pi:b"]`, true)
	if kept != "steps:\n  alpha:\n    grill:\n      models: [\"pi:b\"]\n      attrs:\n        question_batch_size: 2\n" {
		t.Fatalf("flow expansion must keep the attrs:\n%q", kept)
	}
}

// TestStepsChainUnsetDeletesEntry pins the clear semantics: a chain-only
// entry disappears (and an emptied project/section with it), an
// attrs-carrying entry keeps its attrs, an attr unset drops an emptied map.
func TestStepsChainUnsetDeletesEntry(t *testing.T) {
	after := editStepsYAML(denseStepsYAML, stepKey{project: "alpha", step: "code"}, "", false)
	if strings.Contains(after, "    code:") || !strings.Contains(after, "    # code comment\n    default:") {
		t.Fatalf("a chain-only entry must be deleted:\n%s", after)
	}
	kept := editStepsYAML(denseStepsYAML, stepKey{project: "alpha", step: "grill"}, "", false)
	if !strings.Contains(kept, "    grill:\n      attrs:\n        question_batch_size: 2\n") || strings.Contains(kept, `"pi:before"`) {
		t.Fatalf("a chain clear must keep the attrs:\n%s", kept)
	}
	noAttr := editStepsYAML(kept, stepKey{project: "alpha", step: "grill", attr: "question_batch_size"}, "", false)
	if strings.Contains(noAttr, "grill:") {
		t.Fatalf("an entry left declaring nothing must go:\n%s", noAttr)
	}
	only := "steps:\n  alpha:\n    fix: { models: [pi:a] }\nsummarize: { models: [] }\n"
	if got := editStepsYAML(only, stepKey{project: "alpha", step: "fix"}, "", false); got != "summarize: { models: [] }\n" {
		t.Fatalf("an emptied project and steps section must go, got %q", got)
	}
	if got := editStepsYAML(denseStepsYAML, stepKey{project: "zeta", step: "fix"}, "", false); got != denseStepsYAML {
		t.Fatal("unsetting an absent key must be a byte-identical no-op")
	}
}
