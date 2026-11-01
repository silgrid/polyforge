package driver

// attr_prompt_test.go: — the rendered prompt is the oracle of
// the question_batch_size attr: an override that differs from the default
// renders the shared material through the step-var substitution as its own
// section; 0 (explicit or absent) leaves the shipped grill prompt
// byte-identical; the attr is scoped to the step id that declares it.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// shippedGrillAttrs composes the attrs a project with the given yaml
// override gets for the shipped bundle (the runtime's exact chain:
// ComposeAttrs over the bundle frontmatter and the parsed yaml).
func shippedGrillAttrs(t *testing.T, bundle string, n int) map[string]string {
	t.Helper()
	fm, err := workflow.ReadFrontmatter(filepath.Join(shippedWorkflowsDir, bundle, "workflow.md"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &workspace.Config{Attrs: map[string]map[string]map[string]any{"p": {"grill": {"question_batch_size": n}}}}
	return workspace.ComposeAttrs(fm, cfg, "p")["grill"]
}

func TestGrillPromptCarriesOverride(t *testing.T) {
	for _, bundle := range []string{"feature-full", "autopilot"} {
		attrs := shippedGrillAttrs(t, bundle, 3)
		if attrs["question_batch_size"] != "3" {
			t.Fatalf("%s: the shipped grill must declare question_batch_size (got %v)", bundle, attrs)
		}
		p := spawnStepPromptWith(t, bundle, StepSpawn{StepID: "grill", SpawnN: 1, Attrs: attrs})
		if !strings.Contains(p, "# Step attribute: question_batch_size = 3") || !strings.Contains(p, "Ask at most 3 rows per round") {
			t.Fatalf("%s: the override section is missing from the grill prompt", bundle)
		}
		if strings.Contains(p, "{{question_batch_size}}") {
			t.Fatalf("%s: the material rendered with an unsubstituted placeholder", bundle)
		}
		if strings.Index(p, "# Step attribute") < strings.Index(p, "# Step definition") {
			t.Fatalf("%s: the attr section must follow the step definition", bundle)
		}
	}
}

func TestGrillPromptByteIdenticalAtN0(t *testing.T) {
	for _, bundle := range []string{"feature-full", "autopilot"} {
		if attrs := shippedGrillAttrs(t, bundle, 0); attrs != nil {
			t.Fatalf("%s: N=0 must compose no override, got %v", bundle, attrs)
		}
		base := spawnStepPromptWith(t, bundle, StepSpawn{StepID: "grill", SpawnN: 1})
		zero := spawnStepPromptWith(t, bundle, StepSpawn{StepID: "grill", SpawnN: 1, Attrs: shippedGrillAttrs(t, bundle, 0)})
		if normCapsule(base) != normCapsule(zero) {
			t.Fatalf("%s: the N=0 prompt differs from the undeclared prompt", bundle)
		}
		if strings.Contains(base, "# Step attribute") || strings.Contains(base, "question_batch_size") {
			t.Fatalf("%s: the default prompt must carry no attr material", bundle)
		}
		if !strings.Contains(base, "BATCH your questions") || !strings.Contains(base, "ONE needs_input envelope") {
			t.Fatalf("%s: the all-at-once grill text is gone from the default prompt", bundle)
		}
	}
}

func TestGrillPromptBatchesAtN2(t *testing.T) {
	p := spawnStepPromptWith(t, "feature-full", StepSpawn{StepID: "grill", SpawnN: 1, Attrs: shippedGrillAttrs(t, "feature-full", 2)})
	for _, want := range []string{
		"Ask at most 2 rows per round",
		"order them by\n  `重点关注程度` descending",
		"go in the FIRST round, ahead of the ordered rows",
		"a `skipped-with-rationale`\n  e2e-author mode",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("N=2 prompt lacks %q", want)
		}
	}
	one := spawnStepPromptWith(t, "autopilot", StepSpawn{StepID: "grill", SpawnN: 1, Attrs: shippedGrillAttrs(t, "autopilot", 1)})
	if !strings.Contains(one, "Ask at most 1 rows per round") || !strings.Contains(one, "A size of 1 is the one-question-at-a-time grill") {
		t.Error("N=1 prompt does not state the one-row grill")
	}
}

// TestGrillReviewPromptUnaffected pins the scope: the attr belongs to the
// grill step id; grill-review (which includes the same batch fragment)
// declares none, so no composition ever hands it an override and its
// prompt is untouched.
func TestGrillReviewPromptUnaffected(t *testing.T) {
	fm, err := workflow.ReadFrontmatter(filepath.Join(shippedWorkflowsDir, "feature-full", "workflow.md"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &workspace.Config{Attrs: map[string]map[string]map[string]any{"p": {"grill": {"question_batch_size": 2}}}}
	got := workspace.ComposeAttrs(fm, cfg, "p")
	if _, ok := got["grill-review"]; ok {
		t.Fatalf("grill-review must get no override: %v", got)
	}
	base := spawnStepPrompt(t, "feature-full", "grill-review")
	with := spawnStepPromptWith(t, "feature-full", StepSpawn{StepID: "grill-review", SpawnN: 1, Attrs: got["grill-review"]})
	if normCapsule(base) != normCapsule(with) || strings.Contains(with, "question_batch_size") {
		t.Fatal("the grill-review prompt changed under a grill override")
	}
}

// TestAttrMaterialMissingFailsClosed pins that an override whose material
// is missing is a dispatch error, never a silently stored-only attr.
func TestAttrMaterialMissingFailsClosed(t *testing.T) {
	if _, err := attrSections(t.TempDir(), map[string]string{"nonexistent_knob": "2"}); err == nil || !strings.Contains(err.Error(), "attr_nonexistent_knob.md") {
		t.Fatalf("a missing material must fail the dispatch naming the file: %v", err)
	}
}

// TestAttrsComposedAtEveryDispatch pins the driver half: the Attrs closure
// is called at each dispatch beside Compose, its step's map rides the
// StepSpawn, and a failure blocks the workflow like a compose failure.
func TestAttrsComposedAtEveryDispatch(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	seedExecution(t, execPath, "grill", capsule.StepInProgress, nil)
	calls := 0
	var fail error
	val := "2"
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_attr", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: []StepSpec{{ID: "grill", ContextPolicy: "fresh"}}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:  &fakeSpawner{},
		Log:      &eventLog{},
		Attrs: func() (map[string]map[string]string, error) {
			calls++
			return map[string]map[string]string{"grill": {"question_batch_size": val}}, fail
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2", "5"} {
		val = want
		if blocked, err := d.composeDispatch("grill"); err != nil || blocked {
			t.Fatalf("composeDispatch = %v, %v", blocked, err)
		}
		sp, _, err := d.prepareDispatch("grill")
		if err != nil || sp == nil || sp.Attrs["question_batch_size"] != want {
			t.Fatalf("dispatch attrs = %+v, %v; want %s", sp, err, want)
		}
	}
	if calls != 2 {
		t.Fatalf("Attrs called %d times, want once per dispatch", calls)
	}
	fail = errors.New("DAEMON-414: undeclared attr")
	if blocked, err := d.composeDispatch("grill"); err != nil || !blocked {
		t.Fatalf("an attr composition failure must block: %v, %v", blocked, err)
	}
	if r := d.Snapshot().BlockedReason; !strings.Contains(r, "step attr composition failed") || !strings.Contains(r, "DAEMON-414") {
		t.Fatalf("blocked reason %q", r)
	}
}

// normCapsule replaces the per-test capsule dir in a prompt with a fixed
// token, so two dispatches over two temp capsules compare byte for byte.
func normCapsule(p string) string {
	const marker = "Capsule root: `"
	i := strings.Index(p, marker)
	if i < 0 {
		return p
	}
	root := p[i+len(marker):]
	root = root[:strings.Index(root, "`")]
	return strings.ReplaceAll(p, root, "<capsule>")
}
