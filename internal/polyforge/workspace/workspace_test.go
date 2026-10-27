package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stepsDir writes an installed-workflows dir whose bundles declare the
// step ids code, fix, review and spec, and answers it.
func stepsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nsteps:\n  - { id: code }\n  - { id: fix }\n  - { id: review }\n  - { id: spec }\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "b", "workflow.md"), []byte(fm), 0o644); err != nil {
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
	cfg, err := Load(t.TempDir(), stepsDir(t), nil)
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
		cfg, err := Load(writeYaml(t, content), stepsDir(t), nil)
		if err != nil || cfg != nil {
			t.Fatalf("%s: %+v, %v; want nil, nil (absent-equivalent)", name, cfg, err)
		}
	}
}

// TestLoadValidGrammars pins the valid shapes and the decoded content.
func TestLoadValidGrammars(t *testing.T) {
	cfg, err := Load(writeYaml(t, "steps:\n  e2e:\n    default: { models: [pi:a, pi:b] }\n    code: { models: [pi:c] }\n    review: { models: [] }\n  other:\n    spec: { models: [pi:d] }\nsummarize: { models: [pi:s, claude:s2] }\n"), stepsDir(t), nil)
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
	// polyforge#242: the root summarize key decodes like a step entry's
	// list and lands in Config.Summarize, never under a project.
	if got := cfg.Summarize; len(got) != 2 || got[0] != "pi:s" || got[1] != "claude:s2" {
		t.Fatalf("summarize decoded wrong: %#v", got)
	}
	if _, ok := e2e[SummarizeKey]; ok {
		t.Fatalf("the root summarize list leaked into a project's entries: %+v", e2e)
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
		{"unknown step id", "steps:\n  e2e:\n    build: { models: [pi:a] }\n", []string{"steps.e2e.build"}},
		{"every unknown step id named", "steps:\n  e2e:\n    zz: { models: [pi:a] }\n    fix: { models: [pi:a] }\n  b:\n    gate: { models: [pi:a] }\n", []string{"steps.b.gate, steps.e2e.zz"}},
		// polyforge#242: the old per-project summarize key is no longer
		// reserved — it refuses like any other non-step-id key, with the
		// generic message (no "moved to root" hint).
		{"old per-project summarize key", "steps:\n  e2e:\n    summarize: { models: [pi:a] }\n", []string{"unknown key(s) steps.e2e.summarize: a key is \"default\", \"task\" or a step id of the installed workflows"}},
		{"old per-project summarize beside root", "summarize: { models: [pi:a] }\nsteps:\n  e2e:\n    summarize: { models: [pi:a] }\n", []string{"steps.e2e.summarize"}},
		// polyforge#242: the root summarize entry is as strict as a step entry.
		{"root summarize extra key", "summarize: { models: [pi:a], modles: [pi:b] }\n", []string{"summarize.modles: not allowed"}},
		{"root summarize retired harness", "summarize: { models: [pi:a], harness: pi }\n", []string{"summarize.harness: the `harness` key was removed"}},
		{"root summarize unprefixed candidate", "summarize: { models: [glm-5.3] }\n", []string{`summarize.models[0]: "glm-5.3" needs a harness prefix`}},
		{"root summarize duplicate candidate", "summarize: { models: [pi:a, pi:a] }\n", []string{`summarize.models[1]: repeats "pi:a"`}},
		{"root summarize without models", "summarize: {}\n", []string{"summarize: entry is empty"}},
		{"root summarize a bare list", "summarize: [pi:a]\n", []string{"summarize: must be `{ models: [...] }`"}},
		{"root summarize null", "summarize:\n", []string{"summarize: must be `{ models: [...] }`"}},
		{"root summarize models a scalar", "summarize: { models: pi:a }\n", []string{"summarize.models: must be a list"}},
		// polyforge#243: summarize.language is a trimmed, non-empty,
		// single-line plain string, and never stands without models.
		{"root summarize language empty", "summarize: { models: [pi:a], language: \"\" }\n", []string{"summarize.language: is empty"}},
		{"root summarize language whitespace-only", "summarize: { models: [pi:a], language: \"  \\t \" }\n", []string{"summarize.language: is empty"}},
		{"root summarize language multi-line", "summarize:\n  models: [pi:a]\n  language: |\n    English\n    French\n", []string{"summarize.language: must be a single line"}},
		{"root summarize language a number", "summarize: { models: [pi:a], language: 42 }\n", []string{"summarize.language: must be a plain string"}},
		{"root summarize language a list", "summarize: { models: [pi:a], language: [English] }\n", []string{"summarize.language: must be a plain string"}},
		{"root summarize language a map", "summarize: { models: [pi:a], language: { name: English } }\n", []string{"summarize.language: must be a plain string"}},
		{"root summarize language null", "summarize: { models: [pi:a], language: ~ }\n", []string{"summarize.language: must be a plain string"}},
		{"root summarize language without models", "summarize: { language: English }\n", []string{"summarize: entry is empty — declare `models`"}},
		// polyforge#250: summarize.interval_sec is a YAML integer in
		// 10-3600 inclusive (never clamped, no 0-means-default), and never
		// stands without models.
		{"root summarize interval_sec too small", "summarize: { models: [pi:a], interval_sec: 9 }\n", []string{"summarize.interval_sec: must be an integer in 10-3600"}},
		{"root summarize interval_sec too large", "summarize: { models: [pi:a], interval_sec: 3601 }\n", []string{"summarize.interval_sec: must be an integer in 10-3600"}},
		{"root summarize interval_sec zero", "summarize: { models: [pi:a], interval_sec: 0 }\n", []string{"summarize.interval_sec: must be an integer in 10-3600"}},
		{"root summarize interval_sec negative", "summarize: { models: [pi:a], interval_sec: -30 }\n", []string{"summarize.interval_sec: must be an integer in 10-3600"}},
		{"root summarize interval_sec quoted", "summarize: { models: [pi:a], interval_sec: \"60\" }\n", []string{"summarize.interval_sec: must be an integer in 10-3600"}},
		{"root summarize interval_sec a float", "summarize: { models: [pi:a], interval_sec: 60.5 }\n", []string{"summarize.interval_sec: must be an integer in 10-3600"}},
		{"root summarize interval_sec a duration", "summarize: { models: [pi:a], interval_sec: 1m }\n", []string{"summarize.interval_sec: must be an integer in 10-3600"}},
		{"root summarize interval_sec a bool", "summarize: { models: [pi:a], interval_sec: true }\n", []string{"summarize.interval_sec: must be an integer in 10-3600"}},
		{"root summarize interval_sec null", "summarize: { models: [pi:a], interval_sec: ~ }\n", []string{"summarize.interval_sec: must be an integer in 10-3600"}},
		{"root summarize interval_sec a list", "summarize: { models: [pi:a], interval_sec: [60] }\n", []string{"summarize.interval_sec: must be an integer in 10-3600"}},
		{"root summarize interval_sec a map", "summarize: { models: [pi:a], interval_sec: { sec: 60 } }\n", []string{"summarize.interval_sec: must be an integer in 10-3600"}},
		{"root summarize interval_sec without models", "summarize: { interval_sec: 30 }\n", []string{"summarize: entry is empty — declare `models`"}},
		{"root summarize unknown key names interval_sec", "summarize: { models: [pi:a], every: 30 }\n", []string{"summarize.every: not allowed", "`interval_sec`"}},
		{"misspelled default", "steps:\n  e2e:\n    defaults: { models: [pi:a] }\n", []string{"defaults"}},
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
		{"legacy top-level models", "models:\n  e2e:\n    default: [a]\n", []string{`unknown top-level key "models" (only ` + "`steps`, `summarize` and `memory_policy`)"}},
		{"legacy models beside steps", "steps:\n  e2e:\n    default: { models: [pi:a] }\nmodels:\n  e2e:\n    code: [b]\n", []string{`unknown top-level key "models"`}},
		{"unknown sub-key", "steps:\n  e2e:\n    fix: { models: [pi:a], modles: [b] }\n", []string{"steps.e2e.fix.modles", "a step entry's only key is `models`"}},
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
			cfg, err := Load(ws, stepsDir(t), nil)
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

// TestLoadRootSummarize pins the root summarize key's presence
// semantics (polyforge#242): a file declaring only `summarize:` is a
// present config, a declared-empty list is present and empty (the lane
// off), and a file without the key answers a nil list.
func TestLoadRootSummarize(t *testing.T) {
	cfg, err := Load(writeYaml(t, "summarize: { models: [pi:a, claude:b] }\n"), stepsDir(t), nil)
	if err != nil || cfg == nil {
		t.Fatalf("summarize-only yaml: %+v, %v; want a present config", cfg, err)
	}
	if len(cfg.Summarize) != 2 || cfg.Summarize[0] != "pi:a" || cfg.Summarize[1] != "claude:b" || cfg.Models != nil {
		t.Fatalf("summarize-only yaml decoded wrong: %+v", cfg)
	}
	cfg, err = Load(writeYaml(t, "summarize: { models: [] }\n"), stepsDir(t), nil)
	if err != nil || cfg == nil || cfg.Summarize == nil || len(cfg.Summarize) != 0 {
		t.Fatalf("declared-empty summarize: %+v, %v; want present and empty", cfg, err)
	}
	cfg, err = Load(writeYaml(t, "steps:\n  e2e:\n    fix: { models: [pi:a] }\n"), stepsDir(t), nil)
	if err != nil || cfg == nil || cfg.Summarize != nil {
		t.Fatalf("keyless yaml: %+v, %v; want a nil summarize list", cfg, err)
	}
}

// TestLoadRootSummarizeLanguage pins summarize.language (polyforge#243):
// a declared value is accepted verbatim after the trim (free text, no
// allowlist), and an absent key decodes "" (the claim resolves the
// default).
func TestLoadRootSummarizeLanguage(t *testing.T) {
	cfg, err := Load(writeYaml(t, "summarize: { models: [pi:a], language: \"  English (US)  \" }\n"), stepsDir(t), nil)
	if err != nil || cfg == nil || cfg.SummarizeLanguage != "English (US)" || len(cfg.Summarize) != 1 {
		t.Fatalf("declared language: %+v, %v; want \"English (US)\"", cfg, err)
	}
	cfg, err = Load(writeYaml(t, "summarize:\n  models: [pi:a]\n  language: 日本語\n"), stepsDir(t), nil)
	if err != nil || cfg == nil || cfg.SummarizeLanguage != "日本語" {
		t.Fatalf("block-style language: %+v, %v; want 日本語", cfg, err)
	}
	cfg, err = Load(writeYaml(t, "summarize: { models: [pi:a] }\n"), stepsDir(t), nil)
	if err != nil || cfg == nil || cfg.SummarizeLanguage != "" {
		t.Fatalf("absent language: %+v, %v; want \"\"", cfg, err)
	}
}

// TestLoadRootSummarizeInterval pins summarize.interval_sec
// (polyforge#250): both inclusive bounds and a middle value are accepted
// as declared, and an absent key decodes 0 (the claim resolves the
// default 60).
func TestLoadRootSummarizeInterval(t *testing.T) {
	for _, sec := range []int{10, 120, 3600} {
		cfg, err := Load(writeYaml(t, fmt.Sprintf("summarize: { models: [pi:a], interval_sec: %d }\n", sec)), stepsDir(t), nil)
		if err != nil || cfg == nil || cfg.SummarizeIntervalSec != sec || len(cfg.Summarize) != 1 {
			t.Fatalf("declared interval_sec %d: %+v, %v", sec, cfg, err)
		}
	}
	cfg, err := Load(writeYaml(t, "summarize:\n  models: [pi:a]\n  language: English\n  interval_sec: 300\n"), stepsDir(t), nil)
	if err != nil || cfg == nil || cfg.SummarizeIntervalSec != 300 || cfg.SummarizeLanguage != "English" {
		t.Fatalf("block-style interval_sec beside language: %+v, %v", cfg, err)
	}
	cfg, err = Load(writeYaml(t, "summarize: { models: [pi:a] }\n"), stepsDir(t), nil)
	if err != nil || cfg == nil || cfg.SummarizeIntervalSec != 0 {
		t.Fatalf("absent interval_sec: %+v, %v; want 0", cfg, err)
	}
}

// TestUnreadableYamlIsTyped pins the unreadable-file branch (a
// directory at the yaml path is the sharpest unreadable shape).
func TestUnreadableYamlIsTyped(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ws, stepsDir(t), nil); err == nil {
		t.Fatal("a directory at the yaml path must refuse")
	}
}

// TestLoadStepKeysNeedTheInstalledSet: no workflows dir skips only the
// step-id check, an unreadable one refuses; yaml anchors let one list
// serve several step ids.
func TestLoadStepKeysNeedTheInstalledSet(t *testing.T) {
	ws := writeYaml(t, "steps:\n  e2e:\n    code: { models: &c [pi:a, pi:b] }\n    fix: { models: *c }\n    bogus: { models: *c }\n")
	if _, err := Load(ws, stepsDir(t), nil); err == nil || !strings.Contains(err.Error(), "steps.e2e.bogus") {
		t.Fatalf("an unknown step id must refuse, got %v", err)
	}
	cfg, err := Load(ws, "", nil)
	if err != nil || len(cfg.Models["e2e"]["fix"]) != 2 {
		t.Fatalf("no workflows dir: %+v, %v; want the anchored lists loaded", cfg, err)
	}
	if _, err := Load(ws, filepath.Join(t.TempDir(), "missing"), nil); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("an unreadable workflows dir must refuse, got %v", err)
	}
}

// TestLoadEmptyWorkflowsDirSaysSo: an existing workflows dir with no
// readable bundle refuses a per-step key with that fact, not by calling
// every key unknown; a default-only yaml still loads.
func TestLoadEmptyWorkflowsDirSaysSo(t *testing.T) {
	empty := t.TempDir()
	_, err := Load(writeYaml(t, "steps:\n  e2e:\n    fix: { models: [pi:a] }\n    code: { models: [pi:b] }\n"), empty, nil)
	if err == nil || !strings.Contains(err.Error(), "no workflows found in "+empty) || strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("empty workflows dir: %v", err)
	}
	if cfg, err := Load(writeYaml(t, "steps:\n  e2e:\n    default: { models: [pi:a] }\n"), empty, nil); err != nil || cfg == nil {
		t.Fatalf("default-only yaml over an empty workflows dir: %+v, %v", cfg, err)
	}
}

// TestLoadProjectsMustBeMapped: a project key under steps that cli.toml
// does not map to this workspace refuses, naming it and the mapped set;
// nil skips the check. Model ids are trimmed on load.
func TestLoadProjectsMustBeMapped(t *testing.T) {
	ws := writeYaml(t, "steps:\n  e2e:\n    fix: { models: [\" pi:a \"] }\n  ghost:\n    default: { models: [pi:b] }\n")
	_, err := Load(ws, stepsDir(t), []string{"e2e", "other"})
	if err == nil || !strings.Contains(err.Error(), "project(s) ghost are not mapped to this workspace in cli.toml (it maps: e2e, other)") {
		t.Fatalf("unmapped project: %v", err)
	}
	cfg, err := Load(ws, stepsDir(t), nil)
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
		_, err := Load(writeYaml(t, yaml), stepsDir(t), nil)
		if err == nil || !strings.Contains(err.Error(), "steps.b.fix: must be `{ models: [...] }`") {
			t.Fatalf("run %d: %v, want the first defect in file order (steps.b.fix)", i, err)
		}
	}
	yaml = "steps:\n  e2e:\n    zz: { models: [pi:x] }\n    fix: { models: [~] }\n    aa: { models: [pi:y, pi:y] }\n"
	if _, err := Load(writeYaml(t, yaml), stepsDir(t), nil); err == nil || !strings.Contains(err.Error(), "steps.e2e.fix.models[0]") {
		t.Fatalf("within one project: %v, want steps.e2e.fix.models[0] first", err)
	}
}

// TestLoadZeroProjectWorkspaceRefusesProjectKeys: a mapped workspace with
// no projects answers a non-nil empty set, so every project key refuses.
func TestLoadZeroProjectWorkspaceRefusesProjectKeys(t *testing.T) {
	_, err := Load(writeYaml(t, "steps:\n  e2e:\n    fix: { models: [pi:a] }\n"), stepsDir(t), []string{})
	if err == nil || !strings.Contains(err.Error(), "project(s) e2e are not mapped") {
		t.Fatalf("zero-project workspace: %v", err)
	}
}

// TestLoadMixedHarnessChain pins the polyforge#175 format: one list may
// mix harnesses, the first `:` splits (the model keeps its own), and the
// list comes back canonical.
func TestLoadMixedHarnessChain(t *testing.T) {
	cfg, err := Load(writeYaml(t, "steps:\n  e2e:\n    review: { models: [claude:claude-opus-5-5, \" pi : example-gateway/model-a\", \"pi:ollama/llama3:8b\"] }\n"), stepsDir(t), nil)
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
	fm := "---\nsteps:\n  - { id: smoke, harness: claude, model_candidates: [claude-opus-5-5] }\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "smoke", "workflow.md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(writeYaml(t, "steps:\n  e2e:\n    smoke: { models: [claude:claude-opus-5-5] }\n"), dir, nil)
	if err != nil || cfg == nil {
		t.Fatalf("a yaml key of a refused bundle's step id: %+v, %v", cfg, err)
	}
}
