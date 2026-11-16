package workflow_test

// params_test.go (polyforge#78 AC1): a step entry's `params` — the
// step's declared parameters, each name to its lane-default value —
// load, render the definition's {{<name>}} placeholders and stay legal
// when the definition never uses them; a name that is not
// ^[a-z][a-z0-9_]{0,63}$, a value that is not one line of at most
// MaxParamValueBytes bytes, and a name colliding with a var (vars,
// vars_files) or a shared fragment refuse the load.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// writeParamStep writes the lane demo under root whose one step `fix`
// declares the front's step keys, with fix.md as its definition (its
// body defBody) and answers the lane directory. The lane is NOT judged:
// a refused lane keeps its defect.
func writeParamStep(t *testing.T, root, front, defBody string) string {
	t.Helper()
	dir := workflowtest.WriteLaneUnchecked(t, root, "demo", front)
	if err := os.MkdirAll(filepath.Join(dir, "steps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "steps", "fix.md"),
		[]byte(fmt.Sprintf("---\nname: fix\ndescription: test definition fix\n---\n\n%s\n", defBody)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestW78StepParamsLoad pins the load of a step that declares params:
// the lane loads with no defect, Step.Params carries the lane defaults
// (the empty default included), the definition's {{<name>}} renders with
// the default at load, a parameter the definition never uses stays
// legal, and the dispatch-time read takes final values over the
// defaults.
func TestW78StepParamsLoad(t *testing.T) {
	dir := writeParamStep(t, t.TempDir(),
		"steps:\n  - id: fix\n    params:\n      image: base-img\n      aux: \"\"\n",
		"Run fix with {{image}}.")
	lane, errs := workflow.Load(dir)
	if len(errs) != 0 {
		t.Fatalf("a step declaring params loads with no defect, got %v", errs)
	}
	s := lane.Step("fix")
	if s == nil {
		t.Fatal("the lane declares no step fix")
	}
	if got := fmt.Sprint(s.Params); got != "map[aux: image:base-img]" {
		t.Fatalf("Step.Params = %v, want image:base-img and the empty aux default", s.Params)
	}
	// the load-time render: the definition's {{image}} resolved (a load
	// that failed to resolve it would have been a WFC-195 defect above)
	def, err := workflow.ReadDefinition(dir, "fix", s.Vars, s.Params)
	if err != nil || !strings.Contains(def.Body, "Run fix with base-img.") {
		t.Fatalf("rendered definition = %q, %v; want the default value in the body", def, err)
	}
	// the dispatch-time read takes the FINAL values, not the defaults
	def, err = workflow.ReadDefinition(dir, "fix", s.Vars, map[string]string{"image": "final-img"})
	if err != nil || !strings.Contains(def.Body, "Run fix with final-img.") {
		t.Fatalf("rendered definition = %q, %v; want the final value in the body", def, err)
	}
}

// TestW78StepParamsRefused pins every params defect as its own one-defect
// load: a name outside ^[a-z][a-z0-9_]{0,63}$, a value that is not one
// line of at most MaxParamValueBytes bytes, and a name colliding with a
// var (vars or vars_files) or a shared fragment.
func TestW78StepParamsRefused(t *testing.T) {
	over := strings.Repeat("x", 257) // MaxParamValueBytes is 256 (owner ruling 2026-11-16)
	cases := []struct {
		name, front, body, code, want string
	}{
		{"a param name that is not a name", "steps:\n  - id: fix\n    params: { 9bad: v }\n",
			"Run fix.", workflow.CodeInvalidValue, "is not a param name"},
		{"a param name with a dash", "steps:\n  - id: fix\n    params: { not-a-name: v }\n",
			"Run fix.", workflow.CodeInvalidValue, "is not a param name"},
		{"a param value with a newline", "steps:\n  - id: fix\n    params:\n      image: |\n        two\n        lines\n",
			"Run fix.", workflow.CodeInvalidValue, "one line"},
		{"a param value over the cap", "steps:\n  - id: fix\n    params:\n      image: " + over + "\n",
			"Run fix.", workflow.CodeInvalidValue, "256 bytes"},
		{"a param colliding with a var", "steps:\n  - id: fix\n    vars: { image: a }\n    params: { image: b }\n",
			"Run fix with {{image}}.", workflow.CodePlaceholder, "collides with the var"},
		{"a param colliding with a vars_files var", "steps:\n  - id: fix\n    vars_files: { frag: fragments/frag.md }\n    params: { frag: b }\n",
			"Run fix with {{frag}}.", workflow.CodePlaceholder, "collides with the var"},
		{"a param shadowing a fragment", "steps:\n  - id: fix\n    params: { fast_checks: v }\n",
			"Run fix.", workflow.CodePlaceholder, "shadows the fragment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// the vars_files and fragment cases need their files
			if err := os.MkdirAll(filepath.Join(root, "demo", "fragments"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "demo", "fragments", "frag.md"), []byte("frag text"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, workflow.SharedDir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, workflow.SharedDir, "fast_checks.md"), []byte("run them"), 0o644); err != nil {
				t.Fatal(err)
			}
			dir := writeParamStep(t, root, tc.front, tc.body)
			_, errs := workflow.Load(dir)
			if len(errs) != 1 {
				t.Fatalf("defects %v, want exactly one", errs)
			}
			e := errs[0]
			if e.Code != tc.code {
				t.Fatalf("code %s, want %s (%s)", e.Code, tc.code, e.Msg)
			}
			if !strings.HasPrefix(e.Path, "steps[0](fix).params") {
				t.Fatalf("path %q does not point at the params entry", e.Path)
			}
			if e.Line == 0 {
				t.Fatalf("the defect names no workflow.yaml line: %+v", e)
			}
			if !strings.Contains(e.Msg, tc.want) {
				t.Fatalf("message %q does not say %q", e.Msg, tc.want)
			}
		})
	}
}
