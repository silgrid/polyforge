package driver

// workflow_ext_test.go: the extended frontmatter read surface — the
// context_policy / handoff parse, the inherit default, the
// gate-must-be-fresh rule, and the deliberate IGNORANCE of the retired
// keys (polyforge#147: inputs / outputs / schema / silence_minutes /
// tool_minutes / include_verdict / on_fail action are no longer read at
// all). requires_command_evidence is NOT retired: it feeds the V4
// command-evidence gate.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// extExample is the task's frontmatter extension example, rounded out
// into a full bundle (a handoff producer, an inherit consumer with
// declared inputs, a fresh gate with a scalar-form input).
const extExample = `
name: autopilot-x
description: extended-frontmatter probe bundle
rewind:
  max_total: 3
  max_per_step: 2
defaults:
  model_candidates: [pi:prov/a, pi:prov/b]
steps:
  - id: grill
    context_policy: fresh
    handoff: true
    recall: true
  - id: fix
    context_policy: inherit
    handoff: true
    recall: true
  - id: verification
    gate: true
    context_policy: fresh
    on_fail: { target: fix }
`

func mustParseExt(t *testing.T, front string) *Workflow {
	t.Helper()
	wf, err := parseWorkflow([]byte(front))
	if err != nil {
		t.Fatalf("parseWorkflow: %v", err)
	}
	if err := validateWorkflow(wf); err != nil {
		t.Fatalf("validateWorkflow: %v", err)
	}
	return wf
}

func TestParseExtendedFrontmatter(t *testing.T) {
	wf := mustParseExt(t, extExample)
	if wf.MaxTotal != 3 || wf.MaxPerStep != 2 {
		t.Fatalf("rewind bounds: %d/%d", wf.MaxTotal, wf.MaxPerStep)
	}
	if len(wf.Steps) != 3 {
		t.Fatalf("steps: %d", len(wf.Steps))
	}
	grill := wf.step("grill")
	if grill == nil || grill.Gate {
		t.Fatalf("grill (a non-gate spec step): %+v", grill)
	}
	if !grill.Handoff || grill.ContextPolicy != ContextFresh {
		t.Fatalf("grill handoff/policy: %v/%q", grill.Handoff, grill.ContextPolicy)
	}
	fix := wf.step("fix")
	if fix.ContextPolicy != ContextInherit || !fix.Handoff {
		t.Fatalf("fix policy/handoff: %q/%v", fix.ContextPolicy, fix.Handoff)
	}
	ver := wf.step("verification")
	if !ver.Gate || ver.ContextPolicy != ContextFresh {
		t.Fatalf("verification gate/policy: %v/%q", ver.Gate, ver.ContextPolicy)
	}
	// recall is declared per step entry, absent = false.
	if !grill.Recall || !fix.Recall || ver.Recall {
		t.Fatalf("recall grill/fix/verification: %v/%v/%v, want true/true/false", grill.Recall, fix.Recall, ver.Recall)
	}
	if ver.OnFail == nil || ver.OnFail.Target != "fix" {
		t.Fatalf("verification on_fail: %+v", ver.OnFail)
	}
}

func TestParseContextPolicyDefaultsToInherit(t *testing.T) {
	wf := mustParseExt(t, "steps:\n  - { id: solo }\n")
	s := wf.step("solo")
	if s.ContextPolicy != ContextInherit {
		t.Fatalf("default policy: %q", s.ContextPolicy)
	}
}

func TestValidateGateMustBeFresh(t *testing.T) {
	// a gate without a declaration inherits — and a gate must be fresh,
	// so the missing declaration is a validation error, not a default.
	wf, err := parseWorkflow([]byte("steps:\n  - { id: g, gate: true }\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = validateWorkflow(wf)
	if err == nil || !strings.Contains(err.Error(), "fresh") {
		t.Fatalf("gate defaulting to inherit must be refused, got %v", err)
	}
	// an explicit inherit on a gate is refused the same way
	wf, err = parseWorkflow([]byte("steps:\n  - { id: g, gate: true, context_policy: inherit }\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err = validateWorkflow(wf); err == nil || !strings.Contains(err.Error(), ContextFresh) {
		t.Fatalf("inherit gate must be refused, got %v", err)
	}
	// declared fresh passes
	mustParseExt(t, "steps:\n  - { id: g, gate: true, context_policy: fresh }\n")
}

func TestValidateUnknownContextPolicy(t *testing.T) {
	wf, err := parseWorkflow([]byte("steps:\n  - { id: s, context_policy: sometimes }\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = validateWorkflow(wf)
	if err == nil || !strings.Contains(err.Error(), "sometimes") {
		t.Fatalf("unknown policy must be refused, got %v", err)
	}
}

// TestRetiredFrontmatterKeysAreIgnored: the parser no longer reads the
// retired keys (polyforge#147 C5) — a bundle that still carries them
// parses and validates with the keys having NO effect at all (the loose
// read ignores unknown keys; there is no field left to fill, no jail to
// enforce, no schema vocabulary to police).
func TestRetiredFrontmatterKeysAreIgnored(t *testing.T) {
	front := "steps:\n" +
		"  - id: s\n" +
		"    inputs:\n" +
		"      - artifact: ../escape.md\n" +
		"      - /abs/also-escapes.md\n" +
		"    outputs:\n" +
		"      - { path: ../out.md, schema: dictionary }\n" +
		"    silence_minutes: 11\n" +
		"    tool_minutes: 22\n" +
		"  - id: g\n" +
		"    gate: true\n" +
		"    context_policy: fresh\n" +
		"    on_fail: { action: terminate, target: s, include_verdict: false }\n"
	wf := mustParseExt(t, front)
	if got := len(wf.Steps); got != 2 {
		t.Fatalf("steps: %d", got)
	}
	// Nothing of the retired keys survives into the step spec: the only
	// on_fail face left is the rewind target (action/include_verdict are
	// gone, and a declared "terminate" no longer buys a terminate edge).
	g := wf.step("g")
	if g.OnFail == nil || g.OnFail.Target != "s" {
		t.Fatalf("on_fail target is the only surviving face: %+v", g.OnFail)
	}
}

func TestValidateStepIdentity(t *testing.T) {
	// duplicate ids
	wf, err := parseWorkflow([]byte("steps:\n  - { id: a }\n  - { id: a }\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := validateWorkflow(wf); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate ids must be refused, got %v", err)
	}
	// empty workflow
	if err := validateWorkflow(&Workflow{}); err == nil {
		t.Fatal("empty workflow must be refused")
	}
	if err := validateWorkflow(nil); err == nil {
		t.Fatal("nil workflow must be refused")
	}
	// missing id
	wf, err = parseWorkflow([]byte("steps:\n  - { gate: true }\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := validateWorkflow(wf); err == nil {
		t.Fatal("missing id must be refused")
	}
}

func TestLoadWorkflowFromBundleDir(t *testing.T) {
	dir := t.TempDir()
	raw := "---\n" + strings.TrimPrefix(extExample, "\n") + "---\n"
	if err := os.WriteFile(filepath.Join(dir, "workflow.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := LoadWorkflow(dir)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if len(wf.Steps) != 3 || wf.step("fix") == nil {
		t.Fatalf("loaded steps: %+v", wf.Steps)
	}

	// missing bundle
	if _, err := LoadWorkflow(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("missing workflow.md must be an error")
	}
	// no frontmatter block
	nofm := filepath.Join(dir, "nofm")
	if err := os.MkdirAll(nofm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nofm, "workflow.md"), []byte("# just a body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkflow(nofm); err == nil {
		t.Fatal("workflow.md without a frontmatter block must be an error")
	}
}

// A bundle whose workflow.md the driver cannot honor refuses New (the
// parse error is surfaced, never swallowed into a degraded driver).
func TestNewSurfacesWorkflowParseError(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "workflows", "b")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nsteps:\n  - { id: g, gate: true, context_policy: inherit }\n---\n"
	if err := os.WriteFile(filepath.Join(bundle, "workflow.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New(Config{
		WI:      WIInfo{ID: "wi", CapsuleDir: dir, BundleName: "b"},
		Spawner: &fakeSpawner{},
	})
	if err == nil || !strings.Contains(err.Error(), ContextFresh) {
		t.Fatalf("New must surface the workflow validation error, got %v", err)
	}
}

// TestLoadWorkflowIgnoresRetiredKind (polyforge#169): the step `kind`
// is retired, but a bundle snapshot materialized before the change (an
// existing capsule recovered by the same daemon) still carries it — the
// load must succeed and parse exactly like the kind-free bundle.
func TestLoadWorkflowIgnoresRetiredKind(t *testing.T) {
	dir := t.TempDir()
	old := "---\nsteps:\n  - { id: fix, kind: code, context_policy: inherit }\n  - { id: verification, kind: gate, gate: true, context_policy: fresh, on_fail: { target: fix } }\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "workflow.md"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := LoadWorkflow(dir)
	if err != nil {
		t.Fatalf("an old snapshot carrying kind: must load, got %v", err)
	}
	want := mustParseExt(t, "steps:\n  - { id: fix, context_policy: inherit }\n  - { id: verification, gate: true, context_policy: fresh, on_fail: { target: fix } }\n")
	if !reflect.DeepEqual(wf, want) {
		t.Fatalf("kind-carrying parse %+v differs from the kind-free %+v", wf, want)
	}
}

// TestValidateOnFail (#118): on_fail only on a gate, and its target must
// be a strictly earlier step — checked at load, not at the gate's first
// FAIL.
func TestValidateOnFail(t *testing.T) {
	for name, front := range map[string]string{
		"non-gate":       "steps:\n  - { id: a }\n  - { id: b, on_fail: { target: a } }\n",
		"missing target": "steps:\n  - { id: a }\n  - { id: g, gate: true, context_policy: fresh, on_fail: { target: e2e-run } }\n",
		"self target":    "steps:\n  - { id: a }\n  - { id: g, gate: true, context_policy: fresh, on_fail: { target: g } }\n",
		"later target":   "steps:\n  - { id: g, gate: true, context_policy: fresh, on_fail: { target: b } }\n  - { id: b }\n",
		"empty target":   "steps:\n  - { id: a }\n  - { id: g, gate: true, context_policy: fresh, on_fail: { target: \"\" } }\n",
	} {
		wf, err := parseWorkflow([]byte(front))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if err := validateWorkflow(wf); err == nil || !strings.Contains(err.Error(), "on_fail") {
			t.Fatalf("%s: must be refused, got %v", name, err)
		}
	}
	mustParseExt(t, "steps:\n  - { id: a }\n  - { id: g, gate: true, context_policy: fresh, on_fail: { target: a } }\n")
}

// TestShippedBundlesValidate: every shipped workflow bundle passes the
// load-time validation.
func TestShippedBundlesValidate(t *testing.T) {
	for _, dir := range shippedBundleDirs(t) {
		if _, err := LoadWorkflow(dir); err != nil {
			t.Errorf("%s: %v", filepath.Base(dir), err)
		}
	}
}

// TestNewRefusesExecutionWithRemovedSteps (polyforge#165 rollout): an
// in-flight feature-full WI's execution.json carries an entry for EVERY
// step of the bundle it was claimed on (the skeleton seeds them all),
// including e2e-baseline and e2e-run. Same-daemon recovery relaunches
// from the capsule's own materialized snapshot (the old bundle) and is
// unaffected; a re-claim materializes the daemon's current checkout,
// where those steps no longer exist. The driver must refuse that start
// loudly, naming every removed step (the claim then lands the WI
// blocked with this reason) — never crash or continue on a mismatched
// graph.
func TestNewRefusesExecutionWithRemovedSteps(t *testing.T) {
	capDir := t.TempDir()
	execJSON := `{"work_item_id":"wi_old","workflow_status":"blocked","current_step":"code","steps":{` +
		`"grill":{"status":"done"},"spec":{"status":"done"},"grill-review":{"status":"done"},"plan":{"status":"done"},` +
		`"e2e-author":{"status":"done"},"e2e-baseline":{"status":"done"},"code":{"status":"in_progress"},` +
		`"e2e-run":{"status":"pending"},"review":{"status":"pending"},"verification":{"status":"pending"},"deliver":{"status":"pending"}}}`
	execPath := filepath.Join(capDir, "execution.json")
	if err := os.WriteFile(execPath, []byte(execJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New(Config{
		WI:        WIInfo{ID: "wi_old", CapsuleDir: capDir, BundleName: "feature-full"},
		ExecPath:  execPath,
		BundleDir: filepath.Join("..", "..", "..", "workflows", "feature-full"),
		Spawner:   &fakeSpawner{},
		Log:       &eventLog{},
	})
	if err == nil {
		t.Fatal("an execution.json naming removed steps must refuse the driver start")
	}
	for _, want := range []string{`"e2e-baseline", "e2e-run"`, `"feature-full"`, "cancel it and re-file"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal must name %s: %v", want, err)
		}
	}
}

// TestParseMechanicalFixSwitch (polyforge#260): the mechanical_fix key
// parses into StepSpec.MechanicalFix; absent = false = off (the default
// every unarmed lane rides).
func TestParseMechanicalFixSwitch(t *testing.T) {
	wf := mustParseExt(t, "steps:\n  - { id: code }\n  - id: verification\n    gate: true\n    context_policy: fresh\n    mechanical_fix: true\n    on_fail: { target: code }\n")
	ver := wf.step("verification")
	if ver == nil || !ver.MechanicalFix {
		t.Fatalf("verification mechanical_fix must parse true: %+v", ver)
	}
	if s := wf.step("code"); s == nil || s.MechanicalFix {
		t.Fatalf("a step without the key must parse false: %+v", s)
	}
	wf = mustParseExt(t, "steps:\n  - { id: solo }\n")
	if s := wf.step("solo"); s == nil || s.MechanicalFix {
		t.Fatalf("absent mechanical_fix must default to false: %+v", s)
	}
}

// TestValidateMechanicalFixPlacement (polyforge#261 NB7): the
// mechanical_fix switch is a verification-entry-only key. Loading a
// workflow that arms it anywhere else must fail loud at load time —
// before NB7 the flag parsed and was then silently ignored everywhere
// it matters (spawnStep gates on the verification step id).
func TestValidateMechanicalFixPlacement(t *testing.T) {
	for name, front := range map[string]string{
		"non-verification":          "steps:\n  - { id: code, mechanical_fix: true }\n  - { id: verification, gate: true, context_policy: fresh }\n",
		"gate but not verification": "steps:\n  - { id: review, gate: true, context_policy: fresh, mechanical_fix: true }\n",
	} {
		wf, err := parseWorkflow([]byte(front))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		err = validateWorkflow(wf)
		if err == nil || !strings.Contains(err.Error(), "mechanical_fix") {
			t.Fatalf("%s: must be refused, got %v", name, err)
		}
	}
	mustParseExt(t, "steps:\n  - { id: code }\n  - { id: verification, gate: true, context_policy: fresh, mechanical_fix: true }\n")
}

// TestShippedMechanicalFixArming (polyforge#260): the fast lane ships
// default-off — feature-full's verification step entry declares
// mechanical_fix: true and no other shipped bundle's does.
func TestShippedMechanicalFixArming(t *testing.T) {
	armed := 0
	for _, dir := range shippedBundleDirs(t) {
		wf, err := LoadWorkflow(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range wf.Steps {
			if !s.MechanicalFix {
				continue
			}
			armed++
			if s.ID != verificationStepID {
				t.Errorf("%s: step %q declares mechanical_fix — the key belongs to the verification step entry alone",
					filepath.Base(dir), s.ID)
			}
			if filepath.Base(dir) != "feature-full" {
				t.Errorf("%s: step %q declares mechanical_fix — the fast lane ships armed in feature-full only",
					filepath.Base(dir), s.ID)
			}
		}
	}
	if armed != 1 {
		t.Fatalf("shipped bundles arm the fast lane %d times, want exactly feature-full's verification (1)", armed)
	}
}
