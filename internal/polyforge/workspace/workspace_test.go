package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// stepsDir writes an installed-workflows dir whose bundles declare the
// step ids code, fix, review and spec, and answers it.
func stepsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "steps:\n  - { id: code }\n  - { id: fix }\n  - { id: review }\n  - { id: spec }\n"
	if err := os.WriteFile(filepath.Join(dir, "b", workflow.WorkflowFile), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeYaml(t *testing.T, content string) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return ws
}

// TestLoadAbsentAndEmptyAreNil pins the absent-equivalence rule: a
// missing file and an empty/comments-only file both answer (nil, nil).
func TestLoadAbsentAndEmptyAreNil(t *testing.T) {
	cfg, err := Load(t.TempDir(), nil)
	if err != nil || cfg != nil {
		t.Fatalf("absent yaml: %+v, %v; want nil, nil", cfg, err)
	}
	for name, content := range map[string]string{
		"empty":          "",
		"whitespace":     "\n\n   \n",
		"comments only":  "# a comment\n# another comment\n",
		"empty document": "---\n",
		"null steps":     "steps:\n",
	} {
		cfg, err := Load(writeYaml(t, content), nil)
		if err != nil || cfg != nil {
			t.Fatalf("%s: %+v, %v; want nil, nil (absent-equivalent)", name, cfg, err)
		}
	}
}

// TestLoadValidGrammars pins the valid shapes and the decoded content.
func TestLoadValidGrammars(t *testing.T) {
	cfg, err := Load(writeYaml(t, "steps:\n  e2e:\n    default: { models: [pi:a, pi:b] }\n    code: { models: [pi:c] }\n    review: { models: [] }\n  other:\n    spec: { models: [pi:d] }\n"), nil)
	if err != nil || cfg == nil {
		t.Fatalf("valid yaml refused: %v", err)
	}
	e2e := cfg.Models["e2e"]
	if len(e2e["default"]) != 2 || e2e["code"][0] != "pi:c" {
		t.Fatalf("e2e decoded wrong: %+v", e2e)
	}
	if got, ok := e2e["review"]; !ok || got == nil || len(got) != 0 {
		t.Fatalf("declared-empty list must be present and non-nil: %#v", got)
	}
	if cfg.Models["other"]["spec"][0] != "pi:d" {
		t.Fatalf("other decoded wrong: %+v", cfg.Models["other"])
	}
}

// TestLoadRefusals mirrors every strict-schema refusal family: the
// typed error names the file (and, where pinned, the offending key).
func TestLoadRefusals(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		needles []string
	}{
		{"torn yaml", "steps:\n  e2e:\n    code: [\n", nil},
		{"old shape: projects section", "projects:\n  e2e:\n    repos: []\n", []string{"projects"}},
		{"old shape: workflow_defaults", "workflow_defaults:\n  model_candidates: [a]\n", []string{"workflow_defaults"}},
		{"old shape: step_kinds", "workflow_defaults:\n  step_kinds:\n    code: [a]\n", []string{"workflow_defaults"}},
		{"retired release section", "release:\n  cadence: weekly\n", []string{"release"}},
		{"unknown top-level key beside steps", "steps:\n  e2e:\n    code: { models: [pi:a] }\nmodel: x\n", []string{"model"}},
		// the root summarize and memory_policy sections
		// moved to cli.toml — either refuses the whole file, naming the
		// line and the migrate command.
		{"retired summarize section", "summarize: { models: [pi:a] }\n", []string{"line 1: `summarize:` moved to cli.toml", "pf config migrate"}},
		{"retired memory_policy section", "steps:\n  e2e:\n    fix: { models: [pi:a] }\nmemory_policy:\n  default: { record: [a] }\n", []string{"line 4: `memory_policy:` moved to cli.toml", "pf config migrate"}},
		{"both retired sections", "summarize: { models: [pi:a] }\nmemory_policy:\n  e2e: { record: [a] }\n", []string{"line 1: `summarize:`", "line 2: `memory_policy:`", "pf config migrate"}},
		{"retired section even malformed", "summarize: [\n", nil},
		{"retired section null", "memory_policy:\n", []string{"`memory_policy:` moved to cli.toml", "pf config migrate"}},
		{"empty model id in default", "steps:\n  e2e:\n    default: { models: [\"\"] }\n", []string{"e2e", "default"}},
		{"blank model id in step list", "steps:\n  e2e:\n    code: { models: [pi:a, \"  \"] }\n", []string{"e2e", "code"}},
		{"null project entry", "steps:\n  e2e:\n", []string{"e2e"}},
		{"null list", "steps:\n  e2e:\n    code:\n", []string{"e2e", "code"}},
		{"steps not a map", "steps: 42\n", []string{"steps: must be a mapping of project → step entries"}},
		{"steps a list", "steps: [a]\n", []string{"steps: must be a mapping"}},
		{"project not a map", "steps:\n  e2e: [a]\n", []string{"steps.e2e: must be a mapping of step entries"}},
		{"entry a scalar", "steps:\n  e2e:\n    code: a\n", []string{"steps.e2e.code: must be `{ models: [...] }`"}},
		{"list of non-strings", "steps:\n  e2e:\n    code: { models: [[pi:a]] }\n", []string{"steps.e2e.code.models[0]: must be a plain `<harness>:<model>` string"}},
		{"null model item", "steps:\n  e2e:\n    code: { models: [pi:a, ~] }\n", []string{"steps.e2e.code.models[1]"}},
		{"numeric model item", "steps:\n  e2e:\n    code: { models: [pi:a, 42] }\n", []string{"steps.e2e.code.models[1]: must be a plain `<harness>:<model>` string"}},
		{"duplicate model id after trim", "steps:\n  e2e:\n    code: { models: [pi:a, \" pi:a \"] }\n", []string{"steps.e2e.code.models[1]: repeats \"pi:a\""}},
		{"models a scalar", "steps:\n  e2e:\n    code: { models: a }\n", []string{"steps.e2e.code.models: must be a list of `<harness>:<model>` candidates"}},
		{"duplicate project key", "steps:\n  e2e:\n    code: { models: [pi:a] }\n  e2e:\n    code: { models: [pi:b] }\n", []string{"e2e"}},
		{"duplicate step key", "steps:\n  e2e:\n    code: { models: [pi:a] }\n    code: { models: [pi:b] }\n", []string{"code"}},
		{"duplicate steps key", "steps:\n  e2e:\n    code: { models: [pi:a] }\nsteps:\n  x:\n    code: { models: [pi:b] }\n", []string{"steps"}},
		{"legacy top-level models", "models:\n  e2e:\n    default: [a]\n", []string{`unknown top-level key "models" (only ` + "`steps` and `tiers`)"}},
		{"legacy models beside steps", "steps:\n  e2e:\n    default: { models: [pi:a] }\nmodels:\n  e2e:\n    code: [b]\n", []string{`unknown top-level key "models"`}},
		{"unknown sub-key", "steps:\n  e2e:\n    fix: { models: [pi:a], modles: [b] }\n", []string{"steps.e2e.fix.modles", "a step entry's one key is `models`"}},
		// polyforge#175: the retired harness key and the candidate format
		{"retired harness key", "steps:\n  e2e:\n    fix: { models: [claude:claude-opus-5-5], harness: claude }\n", []string{"steps.e2e.fix.harness: the `harness` key was removed (polyforge#175) — move it into each model candidate's prefix"}},
		{"retired harness key alone", "steps:\n  e2e:\n    fix: { harness: pi }\n", []string{"steps.e2e.fix.harness: the `harness` key was removed"}},
		{"unprefixed candidate", "steps:\n  polyforge:\n    code: { models: [pi:a, example-gateway/model-a] }\n", []string{`steps.polyforge.code.models[1]: "example-gateway/model-a" needs a harness prefix (pi:… or claude:…)`}},
		{"bare claude id", "steps:\n  e2e:\n    review: { models: [claude-opus-5-5] }\n", []string{`steps.e2e.review.models[0]: "claude-opus-5-5" needs a harness prefix`}},
		{"unknown prefix", "steps:\n  e2e:\n    fix: { models: [codex:gpt-6] }\n", []string{`steps.e2e.fix.models[0]: "codex:gpt-6" has an unknown harness prefix "codex"`}},
		{"provider as prefix", "steps:\n  e2e:\n    fix: { models: [\"ollama/llama3:8b\"] }\n", []string{`steps.e2e.fix.models[0]: "ollama/llama3:8b" has an unknown harness prefix`}},
		{"empty model after prefix", "steps:\n  e2e:\n    fix: { models: [\"claude:\"] }\n", []string{`steps.e2e.fix.models[0]: "claude:" names no model`}},
		{"entry without models", "steps:\n  e2e:\n    fix: {}\n", []string{"steps.e2e.fix: entry is empty"}},
		{"merge key at top level", "base: &b {}\n<<: *b\nsteps:\n  e2e:\n    fix: { models: [pi:a] }\n", []string{"top level: YAML merge keys (`<<`) are not supported", "{ models: *x }"}},
		{"merge key at project level", "steps:\n  base: &p\n    fix: { models: [pi:a] }\n  e2e:\n    <<: *p\n", []string{"steps.e2e: YAML merge keys (`<<`) are not supported"}},
		{"merge key at entry level", "steps:\n  e2e:\n    fix: &f { models: [pi:a] }\n    <<: { code: *f }\n", []string{"steps.e2e: YAML merge keys"}},
		{"merge key inside an entry", "steps:\n  e2e:\n    fix: { <<: { models: [pi:a] } }\n", []string{"steps.e2e.fix: YAML merge keys"}},
		{"merge key list form", "steps:\n  base: &p\n    fix: { models: [pi:a] }\n  other: &q\n    code: { models: [pi:b] }\n  e2e:\n    <<: [*p, *q]\n", []string{"steps.e2e: YAML merge keys (`<<`) are not supported"}},
		{"second yaml document", "steps:\n  e2e:\n    fix: { models: [pi:a] }\n---\nsteps: {}\n", []string{"exactly one YAML document"}},
		{"null project key", "steps:\n  ~:\n    fix: { models: [pi:a] }\n", []string{"steps: a null key"}},
		{"null step key", "steps:\n  e2e:\n    null: { models: [pi:a] }\n", []string{"steps.e2e: a null key"}},
		{"alias item in a list", "steps:\n  e2e:\n    fix: { models: [&m pi:a, *m] }\n", []string{"steps.e2e.fix.models[1]: must be a plain `<harness>:<model>` string"}},
		{"tagged item in a list", "steps:\n  e2e:\n    fix: { models: [!!binary aGk=] }\n", []string{"steps.e2e.fix.models[0]"}},
		{"bare list instead of entry", "steps:\n  e2e:\n    fix: [a]\n", []string{"steps.e2e.fix: must be `{ models: [...] }`"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ws := writeYaml(t, c.yaml)
			cfg, err := Load(ws, nil)
			if err == nil {
				t.Fatalf("invalid yaml accepted: %+v", cfg)
			}
			if cfg != nil {
				t.Fatalf("refused but returned a config: %+v", cfg)
			}
			for _, n := range append([]string{FileName}, c.needles...) {
				if !strings.Contains(err.Error(), n) {
					t.Fatalf("refusal %q does not name %q", err.Error(), n)
				}
			}
			wsErr, ok := err.(*Error)
			if !ok {
				t.Fatalf("refusal is not a *workspace.Error: %T", err)
			}
			if wsErr.Path != filepath.Join(ws, FileName) {
				t.Fatalf("refusal path %q, want the workspace yaml path", wsErr.Path)
			}
		})
	}
}

// TestUnreadableYamlIsTyped pins the unreadable-file branch (a
// directory at the yaml path is the sharpest unreadable shape).
func TestUnreadableYamlIsTyped(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ws, nil); err == nil {
		t.Fatal("a directory at the yaml path must refuse")
	}
}

// TestLoadStepKeysNeedTheInstalledSet: no workflows dir skips only the
// step-id judgement (StaleKeys), an unreadable one fails it; yaml
// anchors let one list serve several step ids.
func TestLoadStepKeysNeedTheInstalledSet(t *testing.T) {
	ws := writeYaml(t, "steps:\n  e2e:\n    code: { models: &c [pi:a, pi:b] }\n    fix: { models: *c }\n    bogus: { models: *c }\n")
	cfg, err := Load(ws, nil)
	if err != nil || len(cfg.Models["e2e"]["fix"]) != 2 {
		t.Fatalf("the anchored lists: %+v, %v", cfg, err)
	}
	if stale, err := StaleKeys(cfg, workflow.LaneSet{Builtin: stepsDir(t)}); err != nil || strings.Join(stale, ",") != "steps.e2e.bogus" {
		t.Fatalf("the stale key: %v, %v", stale, err)
	}
	if _, err := StaleKeys(cfg, workflow.LaneSet{Builtin: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("an unreadable workflows dir cannot judge the keys")
	}
}

// TestStaleKeysOverAnEmptyWorkflowsDir: an existing workflows dir with no
// readable bundle makes every step key stale; `default` and `task` never
// are.
func TestStaleKeysOverAnEmptyWorkflowsDir(t *testing.T) {
	empty := t.TempDir()
	cfg, err := Load(writeYaml(t, "steps:\n  e2e:\n    default: { models: [pi:a] }\n    task: { models: [pi:a] }\n    fix: { models: [pi:a] }\n    code: { models: [pi:b] }\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if stale, err := StaleKeys(cfg, workflow.LaneSet{Builtin: empty}); err != nil || strings.Join(stale, ",") != "steps.e2e.code,steps.e2e.fix" {
		t.Fatalf("over an empty workflows dir: %v, %v", stale, err)
	}
}

// TestLoadProjectsMustBeMapped: a project key under steps that cli.toml
// does not map to this workspace refuses, naming it and the mapped set;
// nil skips the check. Model ids are trimmed on load.
func TestLoadProjectsMustBeMapped(t *testing.T) {
	ws := writeYaml(t, "steps:\n  e2e:\n    fix: { models: [\" pi:a \"] }\n  ghost:\n    default: { models: [pi:b] }\n")
	_, err := Load(ws, []string{"e2e", "other"})
	if err == nil || !strings.Contains(err.Error(), "project(s) ghost are not mapped to this workspace in cli.toml (it maps: e2e, other)") {
		t.Fatalf("unmapped project: %v", err)
	}
	cfg, err := Load(ws, nil)
	if err != nil || cfg.Models["e2e"]["fix"][0] != "pi:a" {
		t.Fatalf("nil project set: %+v, %v; want loaded with trimmed ids", cfg, err)
	}
}

// TestLoadRefusesFirstDefectInFileOrder: with several defects the
// refusal is the first one in file order — deterministic, never Go map
// iteration order.
func TestLoadRefusesFirstDefectInFileOrder(t *testing.T) {
	yaml := "steps:\n  a:\n    fix: { models: [pi:x] }\n  b:\n    fix: [x]\n  c:\n    fix: { bogus: [x] }\n  d:\n    fix: { models: [~] }\n"
	for i := 0; i < 20; i++ {
		_, err := Load(writeYaml(t, yaml), nil)
		if err == nil || !strings.Contains(err.Error(), "steps.b.fix: must be `{ models: [...] }`") {
			t.Fatalf("run %d: %v, want the first defect in file order (steps.b.fix)", i, err)
		}
	}
	yaml = "steps:\n  e2e:\n    zz: { models: [pi:x] }\n    fix: { models: [~] }\n    aa: { models: [pi:y, pi:y] }\n"
	if _, err := Load(writeYaml(t, yaml), nil); err == nil || !strings.Contains(err.Error(), "steps.e2e.fix.models[0]") {
		t.Fatalf("within one project: %v, want steps.e2e.fix.models[0] first", err)
	}
}

// TestLoadZeroProjectWorkspaceRefusesProjectKeys: a mapped workspace with
// no projects answers a non-nil empty set, so every project key refuses.
func TestLoadZeroProjectWorkspaceRefusesProjectKeys(t *testing.T) {
	_, err := Load(writeYaml(t, "steps:\n  e2e:\n    fix: { models: [pi:a] }\n"), []string{})
	if err == nil || !strings.Contains(err.Error(), "project(s) e2e are not mapped") {
		t.Fatalf("zero-project workspace: %v", err)
	}
}

// TestLoadMixedHarnessChain pins the polyforge#175 format: one list may
// mix harnesses, the first `:` splits (the model keeps its own), and the
// list comes back canonical.
func TestLoadMixedHarnessChain(t *testing.T) {
	cfg, err := Load(writeYaml(t, "steps:\n  e2e:\n    review: { models: [claude:claude-opus-5-5, \" pi : example-gateway/model-a\", \"pi:ollama/llama3:8b\"] }\n"), nil)
	if err != nil || cfg == nil {
		t.Fatalf("valid yaml refused: %v", err)
	}
	want := []string{"claude:claude-opus-5-5", "pi:example-gateway/model-a", "pi:ollama/llama3:8b"}
	if got := cfg.Models["e2e"]["review"]; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("review models %v, want %v", got, want)
	}
}

// polyforge#175: a bundle whose candidates are refused (unprefixed, a
// retired harness key) still contributes its step ids — its defect is
// the bundle's own, never a yaml key's.
func TestLoadStepIDsOfARefusedBundleStillCount(t *testing.T) {
	dir := stepsDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "smoke"), 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "steps:\n  - { id: smoke, harness: claude, model_candidates: [claude-opus-5-5] }\n"
	if err := os.WriteFile(filepath.Join(dir, "smoke", workflow.WorkflowFile), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(writeYaml(t, "steps:\n  e2e:\n    smoke: { models: [claude:claude-opus-5-5] }\n"), nil)
	if err != nil || cfg == nil {
		t.Fatalf("a yaml key of a refused bundle's step id: %+v, %v", cfg, err)
	}
	if stale, err := StaleKeys(cfg, workflow.LaneSet{Builtin: dir}); err != nil || len(stale) != 0 {
		t.Fatalf("the refused bundle's step id is no stale key: %v, %v", stale, err)
	}
}

// TestWorkspaceLoadRefusesStepAttrs pins the removal of the step attrs
// (workflow contract 4,): an `attrs` key under any step
// entry — a step id, `default` or `task`; beside `models`, alone, or
// empty — refuses the whole file at its yaml path with the rewrite.
func TestWorkspaceLoadRefusesStepAttrs(t *testing.T) {
	for name, yaml := range map[string]string{
		"beside models": "steps:\n  e2e:\n    fix: { models: [pi:a], attrs: { question_batch_size: 1 } }\n",
		"alone":         "steps:\n  e2e:\n    fix:\n      attrs: { question_batch_size: 1 }\n",
		"empty":         "steps:\n  e2e:\n    fix:\n      attrs: {}\n",
		"under default": "steps:\n  e2e:\n    default:\n      models: [pi:a]\n      attrs: { question_batch_size: 1 }\n",
		"under task":    "steps:\n  e2e:\n    task:\n      attrs: { x: y }\n",
	} {
		_, err := Load(writeYaml(t, yaml), nil)
		key := map[string]string{"under default": DefaultKey, "under task": TaskKey}[name]
		if key == "" {
			key = "fix"
		}
		want := "steps.e2e." + key + ".attrs: " + AttrsRemovedHint
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err %v, want it to name %q", name, err, want)
		}
	}
}
