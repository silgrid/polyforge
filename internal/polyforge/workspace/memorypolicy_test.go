package workspace

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestMemoryPolicyBuiltinDefault pins the built-in policy (parsed from
// the very section `pf init` appends) and that an absent file, a file
// without the section and a nil config all answer it.
func TestMemoryPolicyBuiltinDefault(t *testing.T) {
	d := DefaultMemoryPolicy()
	if len(d.Record) != 3 || !strings.Contains(d.Record[0], "踩坑和根因") || len(d.Skip) != 4 || d.Skip[3] != "密钥和凭据的值" {
		t.Fatalf("built-in policy = %+v", d)
	}
	var nilCfg *Config
	if got := nilCfg.MemoryPolicyFor("e2e"); !reflect.DeepEqual(got, d) {
		t.Fatalf("nil config policy = %+v", got)
	}
	cfg, err := Load(t.TempDir(), "", nil)
	if err != nil || !reflect.DeepEqual(cfg.MemoryPolicyFor("e2e"), d) {
		t.Fatalf("absent file: %+v, %v", cfg, err)
	}
	cfg, err = Load(writeYaml(t, "steps:\n  e2e:\n    code: { models: [pi:a] }\n"), stepsDir(t), nil)
	if err != nil || cfg.MemoryPolicy != nil || !reflect.DeepEqual(cfg.MemoryPolicyFor("e2e"), d) {
		t.Fatalf("steps-only file: %+v, %v", cfg, err)
	}
	// the caller's copy is its own
	d.Record[0] = "x"
	if DefaultMemoryPolicy().Record[0] == "x" {
		t.Fatal("DefaultMemoryPolicy shares its backing array")
	}
}

// TestMemoryPolicyMerge pins the merge: default's declared lists replace
// the built-in ones list by list, a project's lists are appended
// (duplicates dropped), a declared empty list governs.
func TestMemoryPolicyMerge(t *testing.T) {
	yaml := "memory_policy:\n" +
		"  default:\n    record: [r1, r2]\n" +
		"  e2e:\n    record: [r2, r3]\n    skip: [s1]\n" +
		"  other:\n    skip: []\n"
	cfg, err := Load(writeYaml(t, yaml), stepsDir(t), []string{"e2e", "other", "third"})
	if err != nil || cfg == nil {
		t.Fatalf("valid memory_policy refused: %v", err)
	}
	builtinSkip := DefaultMemoryPolicy().Skip
	if got := cfg.MemoryPolicyFor("e2e"); !reflect.DeepEqual(got.Record, []string{"r1", "r2", "r3"}) ||
		!reflect.DeepEqual(got.Skip, append(append([]string(nil), builtinSkip...), "s1")) {
		t.Fatalf("e2e policy = %+v", got)
	}
	if got := cfg.MemoryPolicyFor("other"); !reflect.DeepEqual(got.Record, []string{"r1", "r2"}) || !reflect.DeepEqual(got.Skip, builtinSkip) {
		t.Fatalf("other policy = %+v", got)
	}
	if got := cfg.MemoryPolicyFor("third"); !reflect.DeepEqual(got.Record, []string{"r1", "r2"}) {
		t.Fatalf("third policy = %+v", got)
	}
	cfg, err = Load(writeYaml(t, "memory_policy:\n  default:\n    skip: []\n"), "", nil)
	if err != nil || len(cfg.MemoryPolicyFor("e2e").Skip) != 0 || cfg.MemoryPolicyFor("e2e").Skip == nil {
		t.Fatalf("declared empty default skip: %+v, %v", cfg, err)
	}
	// a project-only section keeps the built-in base
	cfg, err = Load(writeYaml(t, "memory_policy:\n  e2e:\n    record: [extra]\n"), "", nil)
	if got := cfg.MemoryPolicyFor("e2e"); err != nil || len(got.Record) != 4 || got.Record[3] != "extra" {
		t.Fatalf("project-only section: %+v, %v", got, err)
	}
}

// TestMemoryPolicyRefusals pins the strict grammar: each defect names
// its yaml path.
func TestMemoryPolicyRefusals(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"null section", "memory_policy:\n", "memory_policy: section is empty"},
		{"empty section", "memory_policy: {}\n", "memory_policy: section is empty"},
		{"section a list", "memory_policy: [a]\n", "memory_policy: must be a mapping"},
		{"empty entry", "memory_policy:\n  default: {}\n", "memory_policy.default: entry is empty"},
		{"entry a list", "memory_policy:\n  default: [a]\n", "memory_policy.default: must be `{ record: [...], skip: [...] }`"},
		{"unknown entry key", "memory_policy:\n  default:\n    remember: [a]\n", "memory_policy.default.remember: not allowed"},
		{"list a scalar", "memory_policy:\n  default:\n    record: a\n", "memory_policy.default.record: must be a list of strings"},
		{"non-string item", "memory_policy:\n  default:\n    record: [a, 42]\n", "memory_policy.default.record[1]: must be a plain string"},
		{"null item", "memory_policy:\n  default:\n    skip: [~]\n", "memory_policy.default.skip[0]: must be a plain string"},
		{"blank item", "memory_policy:\n  default:\n    skip: [\"  \"]\n", "memory_policy.default.skip[0]: is empty"},
		{"repeated item", "memory_policy:\n  default:\n    skip: [a, \" a \"]\n", `memory_policy.default.skip[1]: repeats "a"`},
		{"duplicate project", "memory_policy:\n  e2e:\n    skip: [a]\n  e2e:\n    skip: [b]\n", "memory_policy.e2e: duplicate key"},
		{"unmapped project", "memory_policy:\n  nope:\n    skip: [a]\n", "project(s) nope are not mapped"},
		{"unknown top-level key", "memory:\n  default:\n    skip: [a]\n", `unknown top-level key "memory"`},
	} {
		_, err := Load(writeYaml(t, tc.yaml), stepsDir(t), []string{"e2e"})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

// TestEnsureMemoryPolicyAppends pins `pf init`'s yaml half: the section
// is appended as text after the existing bytes (kept exactly), the result
// loads, a second run is a no-op, and an absent file is created with it.
func TestEnsureMemoryPolicyAppends(t *testing.T) {
	orig := "# hand-written: keep me\nsteps:   # odd spacing stays\n  e2e:\n    code: { models: [pi:a] }  # trailing\n# last comment, no newline"
	ws := writeYaml(t, orig)
	path := PathFor(ws)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	res, err := Init(ws)
	if err != nil || res.Yaml != "appended" {
		t.Fatalf("init = %+v, %v", res, err)
	}
	raw := readFile(t, path)
	if !strings.HasPrefix(raw, orig+"\n") || !strings.HasSuffix(raw, DefaultMemoryPolicySection) {
		t.Fatalf("appended file:\n%s", raw)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640 kept", fi.Mode().Perm())
	}
	cfg, err := Load(ws, stepsDir(t), nil)
	if err != nil || cfg.Models["e2e"]["code"][0] != "pi:a" || !reflect.DeepEqual(cfg.MemoryPolicyFor("e2e"), DefaultMemoryPolicy()) {
		t.Fatalf("appended file loads to %+v, %v", cfg, err)
	}
	res, err = Init(ws)
	if err != nil || res.Yaml != "unchanged" || readFile(t, path) != raw {
		t.Fatalf("second init = %+v, %v", res, err)
	}

	fresh := t.TempDir()
	if res, err := Init(fresh); err != nil || res.Yaml != "created" || readFile(t, PathFor(fresh)) != DefaultMemoryPolicySection {
		t.Fatalf("absent file: %+v, %v", res, err)
	}
	comments := writeYaml(t, "# only a comment\n")
	if res, err := Init(comments); err != nil || res.Yaml != "appended" || readFile(t, PathFor(comments)) != "# only a comment\n\n"+DefaultMemoryPolicySection {
		t.Fatalf("comments-only file: %+v, %v", res, err)
	}
}

// TestEnsureMemoryPolicyLeavesDeclaredAndBrokenFiles pins that a file
// already declaring the key (even invalidly) and a file that does not
// load are never written; a broken file is reported as a warning while
// scratch/ and CLAUDE.md are still laid out.
func TestEnsureMemoryPolicyLeavesDeclaredAndBrokenFiles(t *testing.T) {
	declared := "memory_policy:\n  default:\n    record: [mine]\n"
	ws := writeYaml(t, declared)
	if res, err := Init(ws); err != nil || res.Yaml != "unchanged" || readFile(t, PathFor(ws)) != declared {
		t.Fatalf("declared: %+v, %v", res, err)
	}
	for broken, want := range map[string]string{
		"steps: [\n":    "fix it first",
		"x\n":           "fix it first",
		"{steps: {}}\n": "add it by hand",
		"steps:\n...\n": "add it by hand",
	} {
		ws := writeYaml(t, broken)
		res, err := Init(ws)
		if err != nil || res.Yaml != "skipped" || !strings.Contains(res.YamlWarning, want) {
			t.Fatalf("%q: %+v, %v; want a skipped yaml warning containing %q", broken, res, err, want)
		}
		if readFile(t, PathFor(ws)) != broken {
			t.Fatalf("%q was rewritten", broken)
		}
		if res.Scratch != "created" || res.NotesFile != "created" {
			t.Fatalf("%q: the layout half did not run: %+v", broken, res)
		}
	}
}

// TestParseTrailingContentReason pins the refusal of content after the
// top-level document: it names the parse failure, not a second document.
func TestParseTrailingContentReason(t *testing.T) {
	for _, raw := range []string{"{steps: {}}\nmemory_policy:\n  default:\n    skip: [a]\n", "steps:\n...\nmemory_policy: {}\n"} {
		_, reason := parse([]byte(raw), nil)
		if !strings.Contains(reason, "content after the top-level document does not parse") || strings.Contains(reason, "second `---`") {
			t.Fatalf("%q: reason %q", raw, reason)
		}
	}
	if _, reason := parse([]byte("a: 1\n---\nb: 2\n"), nil); !strings.Contains(reason, "found a second `---` document") {
		t.Fatalf("two documents: %q", reason)
	}
}
