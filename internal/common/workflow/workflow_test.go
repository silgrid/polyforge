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

// TestReadFrontmatterReadsAttrDecls pins the attrs declaration surface: a
// step's `attrs: { <name>: { type, default } }` is read with its typed
// default, and a malformed declaration is the bundle's own load error.
func TestReadFrontmatterReadsAttrDecls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.md")
	write := func(front string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("---\n"+front+"---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("steps:\n  - id: grill\n    attrs:\n      question_batch_size: { type: int, default: 0 }\n      tone: { type: string, default: plain }\n  - id: spec\n")
	fm, err := ReadFrontmatter(path)
	if err != nil || fm == nil {
		t.Fatalf("valid attrs refused: %v", err)
	}
	got := fm.Steps[0].Attrs
	if got["question_batch_size"].Type != AttrTypeInt || got["question_batch_size"].Default != 0 {
		t.Fatalf("int attr decoded wrong: %+v", got)
	}
	if got["tone"].Type != AttrTypeString || got["tone"].Default != "plain" {
		t.Fatalf("string attr decoded wrong: %+v", got)
	}
	if fm.Steps[1].Attrs != nil {
		t.Fatalf("a step declaring no attrs must carry none: %+v", fm.Steps[1].Attrs)
	}
	for name, tc := range map[string]struct{ front, want string }{
		"unknown type":       {"steps:\n  - id: grill\n    attrs:\n      n: { type: float, default: 0 }\n", "attrs.n.type"},
		"missing default":    {"steps:\n  - id: grill\n    attrs:\n      n: { type: int }\n", "attrs.n.default: is required"},
		"default type":       {"steps:\n  - id: grill\n    attrs:\n      n: { type: int, default: two }\n", "attrs.n.default: must be an integer"},
		"negative default":   {"steps:\n  - id: grill\n    attrs:\n      n: { type: int, default: -1 }\n", "attrs.n.default: must be a non-negative integer"},
		"string default int": {"steps:\n  - id: grill\n    attrs:\n      n: { type: string, default: 2 }\n", "attrs.n.default: must be a string"},
		"bad name":           {"steps:\n  - id: grill\n    attrs:\n      Bad-Name: { type: int, default: 0 }\n", "attrs.Bad-Name"},
	} {
		write(tc.front)
		_, err := ReadFrontmatter(path)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "step grill") {
			t.Fatalf("%s: err %v, want a refusal naming step grill and %q", name, err, tc.want)
		}
	}
}

// TestCheckAttrValue pins the attr value domain: an int attr takes a
// non-negative integer (0 legal), a string attr a string; the negative
// refusal does not reach string attrs.
func TestCheckAttrValue(t *testing.T) {
	intDecl := AttrDecl{Type: AttrTypeInt, Default: 0}
	strDecl := AttrDecl{Type: AttrTypeString, Default: "plain"}
	for _, tc := range []struct {
		name string
		decl AttrDecl
		v    any
		want string
	}{
		{"int zero", intDecl, 0, ""},
		{"int positive", intDecl, 2, ""},
		{"int negative", intDecl, -1, "must be a non-negative integer (declared type int), got -1"},
		{"int as string", intDecl, "2", "must be an integer"},
		{"string", strDecl, "-1", ""},
		{"string as int", strDecl, -1, "must be a string"},
	} {
		got := CheckAttrValue(tc.decl, tc.v)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Fatalf("%s: CheckAttrValue = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestStepAttrs pins the union of the installed bundles' attr
// declarations keyed by step id; a malformed declaration contributes
// nothing.
func TestStepAttrs(t *testing.T) {
	dir := t.TempDir()
	for name, front := range map[string]string{
		"a": "steps:\n  - id: grill\n    attrs:\n      question_batch_size: { type: int, default: 0 }\n  - id: code\n",
		"b": "steps:\n  - id: grill\n    attrs:\n      question_batch_size: { type: int, default: 0 }\n  - id: fix\n    attrs:\n      broken: { type: float, default: 1 }\n",
	} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "workflow.md"), []byte("---\n"+front+"---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := StepAttrs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["grill"]["question_batch_size"].Type != AttrTypeInt {
		t.Fatalf("StepAttrs = %+v, want only grill.question_batch_size (int)", got)
	}
	if _, err := StepAttrs(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("an unreadable workflows dir must be an error")
	}
}
