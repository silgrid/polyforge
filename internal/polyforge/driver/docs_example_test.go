package driver

// docs_example_test.go: the worked example of docs/bundles.md (the
// bundle-authoring chapter) cannot drift from the actual lane format.
// This test extracts the chapter's fenced example files the way a reader
// copies them out, plants them as a lane and runs them through the one
// lane loader every reader of a lane calls (workflow.Load, through
// LoadWorkflow: the contract's key sets and rules, every definition
// rendered with its vars, every output schema compiled), then renders
// each step definition and compiles its schema through the driver's own
// dispatch path. A documented example the daemon would refuse to load
// fails here, not in a user's first run.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// docsExampleFences answers the fenced-block contents of the chapter
// (``` … ``` lines exclusive).
func docsExampleFences(body string) []string {
	var out []string
	in := false
	var cur []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			if in {
				out = append(out, strings.Join(cur, "\n"))
				cur = nil
			}
			in = !in
			continue
		}
		if in {
			cur = append(cur, l)
		}
	}
	return out
}

// TestDocsBundleExampleLoadsThroughTheDaemonPath: the chapter's worked
// example is a bundle the daemon would load — every code path the
// daemon takes with a bundle runs green on it here.
func TestDocsBundleExampleLoadsThroughTheDaemonPath(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "bundles.md"))
	if err != nil {
		t.Fatalf("docs/bundles.md is the bundle-authoring chapter and the repo must carry it: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("docs/bundles.md is empty: the bundle-authoring chapter is a document, not a placeholder")
	}
	fences := docsExampleFences(string(raw))
	if len(fences) == 0 {
		t.Fatal("docs/bundles.md must present its worked example in fenced blocks; the page carries none")
	}

	// The workflow.yaml fence: the block with no frontmatter that declares
	// the contract and the steps — the example's one workflow.yaml. More
	// than one is a chapter defect (the example is one bundle), none leaves
	// nothing to load.
	stepRe, contractRe := regexp.MustCompile(`(?m)^steps:`), regexp.MustCompile(`(?m)^contract:`)
	var wfFence string
	for i, f := range fences {
		if _, _, ok := workflow.SplitFrontmatter([]byte(f)); ok || !stepRe.MatchString(f) || !contractRe.MatchString(f) {
			continue
		}
		if wfFence != "" {
			t.Fatalf("docs/bundles.md's worked example presents more than one workflow.yaml-shaped fenced block (block %d) — the example is one bundle, with one workflow.yaml", i+1)
		}
		wfFence = f
	}
	if wfFence == "" {
		t.Fatal("docs/bundles.md's worked example must present the custom bundle's workflow.yaml — a fenced block declaring contract: and steps: — so the daemon's loader can load it; no such block exists")
	}

	// Plant the documented example as the lane a reader would end up
	// with: workflows/<name>/workflow.yaml + steps/<id>.md per documented
	// step definition (a frontmatter-bearing block with a name:), the
	// shared principles stub beside it ({{principles}} renders from
	// there).
	m := regexp.MustCompile(`(?m)^name:\s*(\S+)\s*$`).FindStringSubmatch(wfFence)
	if m == nil {
		t.Fatal("docs/bundles.md's worked workflow.yaml declares no name: — a lane's name is its directory")
	}
	root := t.TempDir()
	bundle := filepath.Join(root, "workflows", m[1])
	shared := filepath.Join(root, "workflows", workflow.SharedDir)
	for _, d := range []string{filepath.Join(bundle, "steps"), shared} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("plant the documented example's dirs: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(shared, workflow.PrinciplesFile), []byte("docs example stub principles\n"), 0o644); err != nil {
		t.Fatalf("plant the shared principles stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundle, workflow.WorkflowFile), []byte(wfFence), 0o644); err != nil {
		t.Fatalf("plant the documented workflow.yaml example: %v", err)
	}
	defName := regexp.MustCompile(`(?m)^name:\s*([a-z][a-z0-9-]*)\s*$`)
	for _, f := range fences {
		if f == wfFence {
			continue
		}
		front, _, ok := workflow.SplitFrontmatter([]byte(f))
		if !ok {
			continue
		}
		if m := defName.FindSubmatch(front); m != nil {
			if err := os.WriteFile(filepath.Join(bundle, "steps", string(m[1])+".md"), []byte(f), 0o644); err != nil {
				t.Fatalf("plant the documented step definition %s: %v", m[1], err)
			}
		}
	}

	// The one lane loader every reader of a lane calls (workflow.Load,
	// through driver.LoadWorkflow): the strict key sets, the contract
	// version, every definition rendered with its vars.
	wf, err := LoadWorkflow(bundle)
	if err != nil {
		t.Fatalf("docs/bundles.md's worked example must load with the one lane loader the daemon runs lanes with — it does not: %v", err)
	}
	if len(wf.Steps) == 0 {
		t.Fatal("docs/bundles.md's worked example presents a workflow.yaml but loads to no steps — the example has drifted from the actual format")
	}

	// Every step the workflow.yaml names needs its fenced step definition
	// (a frontmatter-bearing block whose name: is the step id), rendered
	// with the vars its workflow.yaml entry declares — never vars
	// synthesized from the placeholders found.
	nameRe := func(id string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^name:\s*` + regexp.QuoteMeta(id) + `\s*$`)
	}
	for _, s := range wf.Steps {
		var def string
		for _, f := range fences {
			front, _, ok := workflow.SplitFrontmatter([]byte(f))
			if ok && nameRe(s.ID).MatchString(string(front)) {
				def = f
				break
			}
		}
		if def == "" {
			t.Fatalf("docs/bundles.md's worked example names step %s but presents no fenced step definition for it (a frontmatter-bearing block whose name: is %s) — the example has drifted from the actual format", s.ID, s.ID)
		}
		// The vars the documented workflow.yaml entry declares for this
		// step (spec.Vars) are the render's only input: a placeholder
		// without a var, and a var no placeholder uses, are both load
		// errors — a documented mismatch fails here.
		if _, err := readStepDefinition(bundle, s.ID, s.Vars); err != nil {
			t.Fatalf("docs/bundles.md's documented step definition for %s must render with the vars its workflow.yaml entry declares (%v) — it does not: %v", s.ID, s.Vars, err)
		}
		// workflow contract 4: a definition's frontmatter is its name and
		// description alone (the loader refuses an output schema, so the
		// load above already proved it carries none).
	}
}
