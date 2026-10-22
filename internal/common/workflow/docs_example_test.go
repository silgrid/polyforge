package workflow

// docs_example_test.go: the documented worked example of docs/bundles.md
// (the bundle-authoring chapter) cannot drift from the actual bundle
// format. This test extracts the chapter's fenced example files the way
// a reader copies them out and parses/renders them with the real loader
// — the same code the daemon loads bundles with: a fence that no longer
// parses with ReadFrontmatter, a step the example's workflow.md names
// without a fenced definition, or a definition that no longer renders
// with ReadStepDefinition, fails here instead of in a user's first run.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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

// docsExampleStepIDs answers the step ids of the chapter's workflow.md
// fences (frontmatter-bearing blocks declaring steps:), each parsed
// with the real loader.
func docsExampleStepIDs(t *testing.T, fences []string) []string {
	t.Helper()
	stepRe := regexp.MustCompile(`(?m)^steps:`)
	var ids []string
	seen := map[string]bool{}
	for i, f := range fences {
		front, _, ok := SplitFrontmatter([]byte(f))
		if !ok || !stepRe.MatchString(string(front)) {
			continue
		}
		tmp := filepath.Join(t.TempDir(), "workflow.md")
		if err := os.WriteFile(tmp, []byte(f), 0o644); err != nil {
			t.Fatalf("plant the documented workflow.md example: %v", err)
		}
		fm, err := ReadFrontmatter(tmp)
		if err != nil {
			t.Fatalf("docs/bundles.md's worked example (fenced block %d) is the real format's workflow.md and must parse with the real workflow loader — it does not: %v", i+1, err)
		}
		if fm == nil || len(fm.Steps) == 0 {
			t.Fatalf("docs/bundles.md's worked example (fenced block %d) presents a workflow.md but parses to no steps — the example has drifted from the actual format", i+1)
		}
		for _, s := range fm.Steps {
			if s.ID == "" {
				t.Fatalf("docs/bundles.md's worked example (fenced block %d) names a step entry with no id", i+1)
			}
			if !seen[s.ID] {
				seen[s.ID] = true
				ids = append(ids, s.ID)
			}
		}
	}
	if len(ids) == 0 {
		t.Fatal("docs/bundles.md's worked example must present the custom bundle's workflow.md — a fenced block whose frontmatter declares steps: — so the real loader can parse it; no such block exists")
	}
	return ids
}

// TestDocsExampleParsesWithTheRealLoader: the chapter's worked example
// is loader-valid — every workflow.md-shaped fence parses with
// ReadFrontmatter, every step it names has a fenced step definition
// (frontmatter bearing `name: <id>`), and every definition renders
// with ReadStepDefinition ({{principles}} from the shared stub, every
// other placeholder satisfied).
func TestDocsExampleParsesWithTheRealLoader(t *testing.T) {
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
	stepIDs := docsExampleStepIDs(t, fences)

	nameRe := func(id string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^name:\s*` + regexp.QuoteMeta(id) + `\s*$`)
	}
	placeholderRe := regexp.MustCompile(`\{\{([a-z][a-z0-9_]*)\}\}`)

	root := t.TempDir()
	bundle := filepath.Join(root, "workflows", "docs-documented-example")
	shared := filepath.Join(root, "workflows", SharedDir)
	if err := os.MkdirAll(filepath.Join(bundle, "steps"), 0o755); err != nil {
		t.Fatalf("plant the documented example's bundle dir: %v", err)
	}
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatalf("plant the documented example's shared dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shared, PrinciplesFile), []byte("docs example stub principles\n"), 0o644); err != nil {
		t.Fatalf("plant the shared principles stub: %v", err)
	}
	for _, id := range stepIDs {
		var def string
		for _, f := range fences {
			front, _, ok := SplitFrontmatter([]byte(f))
			if ok && nameRe(id).MatchString(string(front)) {
				def = f
				break
			}
		}
		if def == "" {
			t.Fatalf("docs/bundles.md's worked example names step %s but presents no fenced step definition for it (a frontmatter-bearing block whose name: is %s) — the example has drifted from the actual format", id, id)
		}
		p := filepath.Join(bundle, "steps", id+".md")
		if err := os.WriteFile(p, []byte(def), 0o644); err != nil {
			t.Fatalf("plant the documented step definition for %s: %v", id, err)
		}
		vars := map[string]string{}
		for _, m := range placeholderRe.FindAllStringSubmatch(def, -1) {
			if m[1] == PrinciplesVar {
				continue // the loader renders it from _shared/principles.md
			}
			vars[m[1]] = "docs-example-value"
		}
		if _, err := ReadStepDefinition(bundle, id, vars); err != nil {
			t.Fatalf("docs/bundles.md's documented step definition for %s must render with the real workflow loader — it does not: %v", id, err)
		}
	}
}
