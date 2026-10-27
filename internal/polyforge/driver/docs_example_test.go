package driver

// docs_example_test.go: the worked example of docs/bundles.md (the
// bundle-authoring chapter) cannot drift from the actual bundle
// format. This test extracts the chapter's fenced example files the
// way a reader copies them out, plants them as a bundle and runs them
// through the loader the daemon actually runs bundles with — the same
// code path, not a parallel one: the candidate-chain read the claim
// composes (workflow.ReadFrontmatter, CheckCandidates included),
// LoadWorkflow (the one structural check of a bundle: validateWorkflow's
// gate-fresh, on_fail-only-on-a-gate-pointing-at-an-earlier-step and
// closed context_policy checks), every step definition rendered with
// the vars its documented workflow.md entry declares (ReadStepDefinition,
// fail closed both ways), and each step's declared output schema
// compiled through the driver's own path (loadStepOutputSchema). A
// documented example the daemon would refuse to load — a gate that
// inherits, an on_fail that points forward, a var that mismatches its
// placeholders — fails here, not in a user's first run.

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

	// The workflow.md fence: the frontmatter-bearing block that declares
	// steps — the example's one workflow.md. More than one is a chapter
	// defect (the example is one bundle), none leaves nothing to load.
	stepRe := regexp.MustCompile(`(?m)^steps:`)
	var wfFence string
	for i, f := range fences {
		front, _, ok := workflow.SplitFrontmatter([]byte(f))
		if !ok || !stepRe.MatchString(string(front)) {
			continue
		}
		if wfFence != "" {
			t.Fatalf("docs/bundles.md's worked example presents more than one workflow.md-shaped fenced block (block %d) — the example is one bundle, with one workflow.md", i+1)
		}
		wfFence = f
	}
	if wfFence == "" {
		t.Fatal("docs/bundles.md's worked example must present the custom bundle's workflow.md — a fenced block whose frontmatter declares steps: — so the daemon's loader can load it; no such block exists")
	}

	// Plant the documented example as the bundle a reader would end up
	// with: workflows/<bundle>/workflow.md + steps/<id>.md per step, the
	// shared principles stub beside it ({{principles}} renders from
	// there).
	root := t.TempDir()
	bundle := filepath.Join(root, "workflows", "docs-documented-example")
	shared := filepath.Join(root, "workflows", workflow.SharedDir)
	if err := os.MkdirAll(filepath.Join(bundle, "steps"), 0o755); err != nil {
		t.Fatalf("plant the documented example's bundle dir: %v", err)
	}
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatalf("plant the documented example's shared dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shared, workflow.PrinciplesFile), []byte("docs example stub principles\n"), 0o644); err != nil {
		t.Fatalf("plant the shared principles stub: %v", err)
	}
	wfPath := filepath.Join(bundle, "workflow.md")
	if err := os.WriteFile(wfPath, []byte(wfFence), 0o644); err != nil {
		t.Fatalf("plant the documented workflow.md example: %v", err)
	}

	// The candidate-chain read the claim composes: the documented
	// defaults / step model_candidates must parse as <harness>:<model>
	// (ReadFrontmatter runs CheckCandidates).
	if _, err := workflow.ReadFrontmatter(wfPath); err != nil {
		t.Fatalf("docs/bundles.md's worked example (the workflow.md fence) must parse with the candidate-chain read the claim composes — it does not: %v", err)
	}

	// The loader the daemon runs bundles with: LoadWorkflow parses the
	// frontmatter and runs validateWorkflow (a gate must run fresh;
	// on_fail only on a gate and pointing at an earlier step; the closed
	// context_policy set; no duplicate or missing step ids).
	wf, err := LoadWorkflow(bundle)
	if err != nil {
		t.Fatalf("docs/bundles.md's worked example must load with driver.LoadWorkflow — the loader the daemon runs bundles with — it does not: %v", err)
	}
	if len(wf.Steps) == 0 {
		t.Fatal("docs/bundles.md's worked example presents a workflow.md but loads to no steps — the example has drifted from the actual format")
	}

	// Every step the workflow.md names needs its fenced step definition
	// (a frontmatter-bearing block whose name: is the step id), rendered
	// with the vars its workflow.md entry declares — never vars
	// synthesized from the placeholders found — and its declared output
	// schema compiled through the driver's own path.
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
		p := filepath.Join(bundle, "steps", s.ID+".md")
		if err := os.WriteFile(p, []byte(def), 0o644); err != nil {
			t.Fatalf("plant the documented step definition for %s: %v", s.ID, err)
		}
		// The vars the documented workflow.md entry declares for this
		// step (spec.Vars) are the render's only input: a placeholder
		// without a var, and a var no placeholder uses, are both load
		// errors — a documented mismatch fails here.
		if _, err := readStepDefinition(bundle, s.ID, s.Vars); err != nil {
			t.Fatalf("docs/bundles.md's documented step definition for %s must render with the vars its workflow.md entry declares (%v) — it does not: %v", s.ID, s.Vars, err)
		}
		// The declared output schema compiles through the driver's path
		// (loadStepOutputSchema — the same compile the prompt shows and
		// the completed result validates against).
		schema, err := loadStepOutputSchema(bundle, s.ID, s.Vars)
		if err != nil {
			t.Fatalf("docs/bundles.md's documented step definition for %s: its declared output schema must compile through the driver's own path — it does not: %v", s.ID, err)
		}
		if schema == nil {
			t.Fatalf("docs/bundles.md's documented step definition for %s declares no output schema — the chapter's step-definition format gives every step an output: block, and the example's do", s.ID)
		}
	}
}
