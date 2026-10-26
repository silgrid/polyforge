package workflow

// workflow_test: the minimal frontmatter read surface — splitting, the
// defaults + per-step candidate fields, and the typed absent conditions.
// Fixture ids are deliberately provider-qualified placeholders; no real
// model or vendor names appear anywhere in this package.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fmFixture = `---
name: demo
defaults:
  model_candidates: [pi:prov-a/model-a1, pi:prov-a/model-a2, claude:prov-b/model-b1]
steps:
  - id: draft
  - id: gate
    model_candidates: [" claude:prov-b/model-b2 "]
---

# body narrative

- the frontmatter array is the only order
`

func TestSplitFrontmatter(t *testing.T) {
	front, _, ok := SplitFrontmatter([]byte(fmFixture))
	if !ok {
		t.Fatal("a well-formed fence must split")
	}
	if !strings.Contains(string(front), "model_candidates") {
		t.Fatalf("front block must carry the candidate keys: %q", front)
	}
	if _, _, ok := SplitFrontmatter([]byte("# no frontmatter\n")); ok {
		t.Fatal("a file without a fence must not split")
	}
	if _, _, ok := SplitFrontmatter([]byte("---\nname: torn")); ok {
		t.Fatal("an unterminated fence must not split")
	}
}

func TestReadFrontmatterParsesCandidates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "workflow.md")
	if err := os.WriteFile(p, []byte(fmFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	fm, err := ReadFrontmatter(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if fm == nil {
		t.Fatal("a present frontmatter must parse")
	}
	want := []string{"pi:prov-a/model-a1", "pi:prov-a/model-a2", "claude:prov-b/model-b1"}
	if fm.Defaults == nil || len(fm.Defaults.ModelCandidates) != len(want) {
		t.Fatalf("defaults candidates = %+v, want %v", fm.Defaults, want)
	}
	for i := range want {
		if fm.Defaults.ModelCandidates[i] != want[i] {
			t.Fatalf("defaults candidates = %v, want %v", fm.Defaults.ModelCandidates, want)
		}
	}
	// the per-step override is carried in canonical form
	var gate *FrontmatterStep
	for i := range fm.Steps {
		if fm.Steps[i].ID == "gate" {
			gate = &fm.Steps[i]
		}
	}
	if gate == nil || len(gate.ModelCandidates) != 1 || gate.ModelCandidates[0] != "claude:prov-b/model-b2" {
		t.Fatalf("per-step override = %+v, want [claude:prov-b/model-b2]", gate)
	}
}

func TestReadFrontmatterAbsentConditions(t *testing.T) {
	dir := t.TempDir()
	// missing file → typed absent, never an error
	fm, err := ReadFrontmatter(filepath.Join(dir, "workflow.md"))
	if fm != nil || err != nil {
		t.Fatalf("missing file must answer (nil, nil), got (%v, %v)", fm, err)
	}
	// frontmatter-less file → typed absent
	p := filepath.Join(dir, "bare.md")
	if err := os.WriteFile(p, []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fm, err = ReadFrontmatter(p)
	if fm != nil || err != nil {
		t.Fatalf("bare file must answer (nil, nil), got (%v, %v)", fm, err)
	}
	// torn YAML → an error (the caller decides fatality)
	p2 := filepath.Join(dir, "torn.md")
	if err := os.WriteFile(p2, []byte("---\nsteps: [unclosed\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fm, err = ReadFrontmatter(p2); fm != nil || err == nil {
		t.Fatalf("torn yaml must answer (nil, err), got (%v, %v)", fm, err)
	}
}

// TestStepIDs pins the legal per-step keys of the workspace yaml: the
// union of the step ids of the installed bundles. A bundle snapshot still
// carrying the retired `kind:` reads unchanged; a torn bundle contributes
// nothing; an unreadable dir is an error.
func TestStepIDs(t *testing.T) {
	dir := t.TempDir()
	write := func(bundle, body string) {
		if err := os.MkdirAll(filepath.Join(dir, bundle), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, bundle, "workflow.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a", "---\nsteps:\n  - { id: fix, kind: code }\n  - { id: review, kind: review }\n---\n")
	write("b", "---\nsteps:\n  - { id: fix }\n  - { id: deploy }\n---\n")
	write("torn", "---\nsteps: [unclosed\n---\n")
	ids, err := StepIDs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || !ids["fix"] || !ids["review"] || !ids["deploy"] {
		t.Fatalf("step ids = %v, want fix, review, deploy", ids)
	}
	if _, err := StepIDs(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("an unreadable workflows dir must be an error")
	}
}

// TestShippedBundlesCarryNoKind: the step `kind` concept is retired
// (polyforge#169); no shipped bundle may still declare it.
func TestShippedBundlesCarryNoKind(t *testing.T) {
	paths, _ := filepath.Glob("../../../workflows/*/workflow.md")
	if len(paths) == 0 {
		t.Fatal("no shipped bundles found")
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		front, _, _ := SplitFrontmatter(raw)
		if strings.Contains(string(front), "kind:") {
			t.Errorf("%s: frontmatter still declares the retired step kind", p)
		}
	}
}

// polyforge#175: an unprefixed candidate, an unknown prefix, an empty
// model and a retired `harness` key each refuse the bundle, naming the
// file and the step.
func TestReadFrontmatterRefusesCandidateFormat(t *testing.T) {
	cases := []struct{ front, want string }{
		{"defaults:\n  model_candidates: [pi:a/b, prov-a/model-a1]\nsteps:\n  - id: x\n",
			`defaults.model_candidates[1]: "prov-a/model-a1" needs a harness prefix (pi:… or claude:…)`},
		{"steps:\n  - id: review\n    model_candidates: [codex:gpt-6]\n",
			`step review: model_candidates[0]: "codex:gpt-6" has an unknown harness prefix "codex"`},
		{"steps:\n  - id: review\n    model_candidates: [\"claude:\"]\n",
			`step review: model_candidates[0]: "claude:" names no model`},
		{"steps:\n  - id: review\n    harness: claude\n",
			"step review: harness: the `harness` key was removed (polyforge#175) — move it into each model candidate's prefix"},
		{"defaults:\n  harness: pi\nsteps:\n  - id: x\n",
			"defaults.harness: the `harness` key was removed"},
	}
	for _, c := range cases {
		p := filepath.Join(t.TempDir(), "workflow.md")
		if err := os.WriteFile(p, []byte("---\n"+c.front+"---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		fm, err := ReadFrontmatter(p)
		if err == nil || fm != nil || !strings.Contains(err.Error(), p+": ") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("front %q: fm %v err %v, want %q", c.front, fm, err, c.want)
		}
	}
}
