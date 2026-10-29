package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// StepDefinitionPath: the bundle's own steps/<id>.md wins; without it the
// shared workflows/_shared/steps/<id>.md is used; with neither the
// bundle's own path is answered so the read error names it.
func TestStepDefinitionPathPrecedence(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "b")
	write := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	own := filepath.Join(bundle, "steps", "deliver.md")
	shared := filepath.Join(root, SharedDir, "steps", "deliver.md")

	if got := StepDefinitionPath(bundle, "deliver"); got != own {
		t.Fatalf("neither present: %s, want %s", got, own)
	}
	write(shared)
	if got := StepDefinitionPath(bundle, "deliver"); got != shared {
		t.Fatalf("shared only: %s, want %s", got, shared)
	}
	write(own)
	if got := StepDefinitionPath(bundle, "deliver"); got != own {
		t.Fatalf("own must win: %s, want %s", got, own)
	}
}

// RenderStepVars fills {{name}} from vars (a block scalar's trailing
// newline dropped, values never re-scanned) and is fail-closed both ways.
func TestRenderStepVars(t *testing.T) {
	got, err := RenderStepVars([]byte("a {{x}} b {{y_2}}"), map[string]string{"x": "one\n", "y_2": "{{x}}"})
	if err != nil || string(got) != "a one b {{x}}" {
		t.Fatalf("render = %q, %v", got, err)
	}
	if _, err := RenderStepVars([]byte("a {{x}} {{x}}"), nil); err == nil || !strings.Contains(err.Error(), "undeclared vars [x] ") {
		t.Fatalf("missing var: %v", err)
	}
	if _, err := RenderStepVars([]byte("a"), map[string]string{"x": "1"}); err == nil || !strings.Contains(err.Error(), "never uses") {
		t.Fatalf("unused var: %v", err)
	}
	if got, err := RenderStepVars([]byte("{{ x }} {{X}}"), nil); err != nil || string(got) != "{{ x }} {{X}}" {
		t.Fatalf("non-placeholder braces must pass through: %q, %v", got, err)
	}
}

// ReadStepDefinition renders {{principles}} from the shared principles
// file without a declared var, refuses a step entry declaring a var that
// shadows a shared fragment, and needs no fragment file for a definition
// that includes none. A fragment's text is inserted verbatim — a
// placeholder inside it is never re-scanned.
func TestReadStepDefinitionPrinciples(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "b")
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(bundle, "steps", "plain.md"), "no principles {{x}}")
	if got, err := ReadStepDefinition(bundle, "plain", map[string]string{"x": "1"}); err != nil || string(got) != "no principles 1" {
		t.Fatalf("plain = %q, %v", got, err)
	}
	write(filepath.Join(bundle, "steps", "code.md"), "use them:\n{{principles}}\n{{x}}")
	if _, err := ReadStepDefinition(bundle, "code", map[string]string{"x": "1"}); err == nil || !strings.Contains(err.Error(), PrinciplesFile) {
		t.Fatalf("missing principles file: %v", err)
	}
	write(filepath.Join(root, SharedDir, PrinciplesFile), "1. Simple.\n")
	if got, err := ReadStepDefinition(bundle, "code", map[string]string{"x": "1"}); err != nil || string(got) != "use them:\n1. Simple.\n1" {
		t.Fatalf("code = %q, %v", got, err)
	}
	if _, err := ReadStepDefinition(bundle, "plain", map[string]string{"x": "1", PrinciplesVar: "mine"}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("declared principles var: %v", err)
	}
}

// ReadStepDefinition renders any shared fragment the definition includes
// without a declared var: {{<name>}} renders
// workflows/_shared/<name>.md exactly like {{principles}}, a declared var
// shadowing a fragment file is a reserved-var error, a placeholder with
// neither a var nor a fragment file fails closed, and a fragment's own
// {{placeholder}} text is inserted verbatim, never re-scanned.
func TestReadStepDefinitionSharedFragments(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "b")
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, SharedDir, "fast_checks.md"), "run the fast checks.\n")
	write(filepath.Join(bundle, "steps", "one.md"), "## Fast checks\n\n{{fast_checks}}\n\nthen {{x}}")
	got, err := ReadStepDefinition(bundle, "one", map[string]string{"x": "1"})
	if err != nil || string(got) != "## Fast checks\n\nrun the fast checks.\n\nthen 1" {
		t.Fatalf("fragment render = %q, %v", got, err)
	}
	// A fragment is never re-scanned: its own {{y}} is inserted verbatim.
	write(filepath.Join(root, SharedDir, "fast_checks.md"), "literal {{y}} inside\n")
	got, err = ReadStepDefinition(bundle, "one", map[string]string{"x": "1"})
	if err != nil || string(got) != "## Fast checks\n\nliteral {{y}} inside\n\nthen 1" {
		t.Fatalf("verbatim fragment = %q, %v", got, err)
	}
	// A declared var may not shadow a fragment file.
	if _, err := ReadStepDefinition(bundle, "one", map[string]string{"x": "1", "fast_checks": "mine"}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("declared fragment-shadowing var: %v", err)
	}
	// A placeholder with neither a var nor a fragment file fails closed.
	write(filepath.Join(bundle, "steps", "two.md"), "{{missing_frag}}\n{{x}}")
	if _, err := ReadStepDefinition(bundle, "two", map[string]string{"x": "1"}); err == nil || !strings.Contains(err.Error(), "missing_frag.md") {
		t.Fatalf("missing fragment file: %v", err)
	}
	// A definition that includes no fragment never reads one: the
	// bundle beside an empty _shared/steps only renders its own vars.
	write(filepath.Join(bundle, "steps", "three.md"), "plain {{x}}")
	if got, err := ReadStepDefinition(bundle, "three", map[string]string{"x": "1"}); err != nil || string(got) != "plain 1" {
		t.Fatalf("no-fragment definition = %q, %v", got, err)
	}
}
