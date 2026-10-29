package test

// w114_verify_scope_conf_first_acceptance_test.go: the
// acceptance suite for the conf-first verification-scope engine,
// `tools/verify-scope`, driven only through its documented surface — the
// target clone as working directory, `--base`, `--full`, `--json`, `--run` —
// over real git fixtures:
//
//   - conf first: a repository with no supported manifest (or an unpinned
//     multi-manifest one) reports its conf's commands on the full tier;
//   - `stack=none` pins "no analyzer": always full, the conf's commands only;
//   - `--json` emits exactly one object in the schema-1 contract, agreeing
//     with the line report;
//   - `conf_changed_in_diff` both ways;
//   - the new conf keys (check=, check_requires_env=, check_requires_cmd=,
//     check_timeout=, test_glob=, base_setup=, runner=, ci_evidence=) and
//     their diagnose-and-ignore rejections;
//   - `--run` executes with `bash -c` (stdin closed), skips a command whose
//     declared prerequisites are missing, and still stops at the first
//     failure.
//
// It reuses the w25e2e fixture helpers (same package).
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW114VerifyScope' -count=1 -v

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// w114vsCommand is one entry of the JSON report's commands array.
type w114vsCommand struct {
	Name        string   `json:"name"`
	Phase       string   `json:"phase"`
	Cmd         string   `json:"cmd"`
	RequiresEnv []string `json:"requires_env"`
	RequiresCmd []string `json:"requires_cmd"`
	Timeout     *string  `json:"timeout"`
	Source      string   `json:"source"`
}

// w114vsTO renders a command's timeout: its declared duration, or "<null>"
// when none is declared (the JSON carries null, never an engine default).
func w114vsTO(c w114vsCommand) string {
	if c.Timeout == nil {
		return "<null>"
	}
	return *c.Timeout
}

// w114vsReport is the JSON report (schema 1).
type w114vsReport struct {
	Schema            int             `json:"schema"`
	Root              string          `json:"root"`
	Stack             string          `json:"stack"`
	Analyzer          *string         `json:"analyzer"`
	Conf              string          `json:"conf"`
	ConfChangedInDiff bool            `json:"conf_changed_in_diff"`
	Base              *string         `json:"base"`
	Head              *string         `json:"head"`
	DefaultBranch     string          `json:"default_branch"`
	Mode              string          `json:"mode"`
	Paths             []string        `json:"paths"`
	Commands          []w114vsCommand `json:"commands"`
	TestGlob          []string        `json:"test_glob"`
	BaseSetup         []string        `json:"base_setup"`
	Runner            *string         `json:"runner"`
	CIEvidence        []string        `json:"ci_evidence"`
	Diagnostics       []string        `json:"diagnostics"`
}

// w114vsTopKeys is the contract's key set (plus ci_evidence, the echo the
// deliver step's conf key gets).
var w114vsTopKeys = []string{
	"analyzer", "base", "base_setup", "ci_evidence", "commands", "conf",
	"conf_changed_in_diff", "default_branch", "diagnostics", "head", "mode",
	"paths", "root", "runner", "schema", "stack", "test_glob",
}

var w114vsCommandKeys = []string{"cmd", "name", "phase", "requires_cmd", "requires_env", "source", "timeout"}

// w114vsKeys answers the sorted keys of a JSON object, failing on a non-object.
func w114vsKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, raw)
	}
	keys := make([]string, 0, len(m))
	for k, v := range m {
		keys = append(keys, k)
		// every array field must be an array (never null)
		switch k {
		case "paths", "commands", "test_glob", "base_setup", "ci_evidence", "diagnostics", "requires_env", "requires_cmd":
			if strings.TrimSpace(string(v)) == "null" || !strings.HasPrefix(strings.TrimSpace(string(v)), "[") {
				t.Fatalf("%q must be an array, got %s", k, v)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// w114vsJSON runs the engine with --json and validates the contract shape:
// stdout is exactly one JSON object with exactly the contract's keys and
// types, and the diagnostics are exactly the stderr lines.
func w114vsJSON(t *testing.T, dir string, env []string, args ...string) w114vsReport {
	t.Helper()
	out, errb, code := w25e2eRun(t, dir, env, append([]string{"--json"}, args...)...)
	if code != 0 {
		t.Fatalf("tools/verify-scope --json %v: exit %d (stderr %q)", args, code, errb)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		t.Fatalf("--json stdout is not JSON: %v\n%s", err, out)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("--json stdout must be exactly one JSON object; trailing data: %v %s", err, extra)
	}
	if got := w114vsKeys(t, raw); !reflect.DeepEqual(got, w114vsTopKeys) {
		t.Fatalf("--json keys = %v, want %v", got, w114vsTopKeys)
	}
	var shape struct {
		Commands []json.RawMessage `json:"commands"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	for _, c := range shape.Commands {
		if got := w114vsKeys(t, c); !reflect.DeepEqual(got, w114vsCommandKeys) {
			t.Fatalf("--json command keys = %v, want %v", got, w114vsCommandKeys)
		}
	}
	dec = json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var r w114vsReport
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("--json report does not match the contract's types: %v\n%s", err, raw)
	}
	if r.Schema != 1 {
		t.Fatalf("schema = %d, want 1", r.Schema)
	}
	if !filepath.IsAbs(r.Root) {
		t.Fatalf("root %q is not absolute", r.Root)
	}
	want, _ := filepath.EvalSymlinks(dir)
	if got, _ := filepath.EvalSymlinks(r.Root); got != want {
		t.Fatalf("root = %q, want the target repository %q", r.Root, dir)
	}
	switch r.Stack {
	case "go", "npm", "none", "unsupported", "ambiguous":
	default:
		t.Fatalf("stack %q is outside go|npm|none|unsupported|ambiguous", r.Stack)
	}
	if r.Analyzer != nil && *r.Analyzer != "go" && *r.Analyzer != "npm" {
		t.Fatalf("analyzer %q is outside go|npm|null", *r.Analyzer)
	}
	if r.Conf != "present" && r.Conf != "absent" {
		t.Fatalf("conf %q is neither present nor absent", r.Conf)
	}
	if r.Mode != "full" && r.Mode != "scoped" {
		t.Fatalf("mode %q is neither full nor scoped", r.Mode)
	}
	if r.Runner != nil && *r.Runner != "complete" {
		t.Fatalf("runner %q is neither complete nor null", *r.Runner)
	}
	seen := map[string]bool{}
	for _, c := range r.Commands {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("command names must be non-empty and unique: %+v", r.Commands)
		}
		seen[c.Name] = true
		if c.Phase != "fast" && c.Phase != "full" && c.Phase != "scoped" {
			t.Fatalf("command %q phase %q is outside fast|full|scoped", c.Name, c.Phase)
		}
		if c.Source != "conf" && c.Source != "default" {
			t.Fatalf("command %q source %q is neither conf nor default", c.Name, c.Source)
		}
		if c.Timeout != nil {
			if d, err := time.ParseDuration(*c.Timeout); err != nil || d <= 0 {
				t.Fatalf("command %q timeout %q is neither null nor a positive Go duration", c.Name, *c.Timeout)
			}
		}
		if c.Cmd == "" {
			t.Fatalf("command %q has an empty cmd", c.Name)
		}
	}
	var stderrLines []string
	for _, ln := range strings.Split(strings.TrimRight(errb, "\n"), "\n") {
		if ln != "" {
			stderrLines = append(stderrLines, ln)
		}
	}
	if len(stderrLines) == 0 {
		stderrLines = nil
	}
	diags := r.Diagnostics
	if len(diags) == 0 {
		diags = nil
	}
	if !reflect.DeepEqual(diags, stderrLines) {
		t.Fatalf("diagnostics must be exactly the stderr lines:\njson   %q\nstderr %q", r.Diagnostics, stderrLines)
	}

	// The JSON agrees with the line report of the same invocation.
	lineOut, _, lineCode := w25e2eRun(t, dir, env, args...)
	if lineCode != 0 {
		t.Fatalf("line report %v: exit %d", args, lineCode)
	}
	line := w25e2eParse(t, lineOut)
	var cmds []string
	for _, c := range r.Commands {
		cmds = append(cmds, c.Cmd)
	}
	if line.mode != r.Mode || strings.Join(line.paths, "\n") != strings.Join(r.Paths, "\n") || strings.Join(line.commands, "\n") != strings.Join(cmds, "\n") {
		t.Fatalf("--json disagrees with the line report:\njson mode %q paths %q commands %q\nline mode %q paths %q commands %q",
			r.Mode, r.Paths, cmds, line.mode, line.paths, line.commands)
	}
	return r
}

// w114vsByName indexes a report's commands.
func w114vsByName(r w114vsReport) map[string]w114vsCommand {
	m := map[string]w114vsCommand{}
	for _, c := range r.Commands {
		m[c.Name] = c
	}
	return m
}

// w114vsPlan renders name:phase:source:cmd per command, in order.
func w114vsPlan(r w114vsReport) []string {
	var out []string
	for _, c := range r.Commands {
		out = append(out, c.Name+":"+c.Phase+":"+c.Source+":"+c.Cmd)
	}
	return out
}

// TestW114VerifyScopeStacklessConfEmitsCommands: a repository with no
// supported manifest declares its bar in the conf, and the engine reports
// those commands (full tier, file order by phase) for any diff — the empty
// diff included; an unpinned multi-manifest repository likewise gets its
// conf's commands and no analyzer default; with no conf there are none.
func TestW114VerifyScopeStacklessConfEmitsCommands(t *testing.T) {
	conf := "fast_gate=cargo fmt --check\n" +
		"full_command=cargo test --workspace\n" +
		"check=clippy|fast|cargo clippy -- -D warnings\n" +
		"check=py|full|python3 -m pytest -q\n"
	dir, base := w25e2eFixture(t, map[string]string{
		"Cargo.toml":                   "[package]\nname = \"w114\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":                   "pub fn one() -> u32 { 1 }\n",
		".polyforge/verify-scope.conf": conf,
	})
	w25e2eWrite(t, dir, "src/lib.rs", "pub fn one() -> u32 { 2 }\n")
	w25e2eCommit(t, dir, "change src/lib.rs")
	want := []string{"cargo fmt --check", "cargo clippy -- -D warnings", "cargo test --workspace", "python3 -m pytest -q"}

	out, errb, code := w25e2eRun(t, dir, nil)
	if code != 0 {
		t.Fatalf("a stackless repo with a conf must exit 0, got %d (%s)", code, errb)
	}
	r := w25e2eParse(t, out)
	if r.mode != "full" || strings.Join(r.paths, ",") != "src/lib.rs" || !reflect.DeepEqual(r.commands, want) {
		t.Fatalf("stackless conf report = mode %q paths %v commands %q, want full [src/lib.rs] %q", r.mode, r.paths, r.commands, want)
	}
	if !strings.Contains(errb, "no supported stack manifest") || !strings.Contains(errb, "conf's commands only") {
		t.Fatalf("the stackless fallback must say it reports the conf's commands only: %q", errb)
	}
	if empty := w25e2eScope(t, dir, "--base", "HEAD"); empty.mode != "full" || len(empty.paths) != 0 || !reflect.DeepEqual(empty.commands, want) {
		t.Fatalf("no analyzer is always full with the conf's commands, even on an empty diff: mode %q paths %v commands %q", empty.mode, empty.paths, empty.commands)
	}
	if over := w25e2eScope(t, dir, "--base", base); !reflect.DeepEqual(over.commands, want) {
		t.Fatalf("--base must not change a conf-only command list: %q", over.commands)
	}
	j := w114vsJSON(t, dir, nil)
	if j.Stack != "unsupported" || j.Analyzer != nil || j.Conf != "present" {
		t.Fatalf("stackless JSON = stack %q analyzer %v conf %q, want unsupported/null/present", j.Stack, j.Analyzer, j.Conf)
	}
	wantPlan := []string{
		"fast-1:fast:conf:cargo fmt --check",
		"clippy:fast:conf:cargo clippy -- -D warnings",
		"full-1:full:conf:cargo test --workspace",
		"py:full:conf:python3 -m pytest -q",
	}
	if got := w114vsPlan(j); !reflect.DeepEqual(got, wantPlan) {
		t.Fatalf("stackless JSON plan = %q, want %q", got, wantPlan)
	}

	// An unpinned multi-manifest repo: the conf's commands, no analyzer default.
	ambDir, _ := w25e2eFixture(t, map[string]string{
		"go.mod":                       "module fixture.test/w114\n\ngo 1.21\n",
		"package.json":                 "{\n  \"name\": \"w114\",\n  \"scripts\": {\"lint\": \"eslint .\"}\n}\n",
		".polyforge/verify-scope.conf": "fast_gate=echo amb-fast\nfull_command=echo amb-full\n",
	})
	amb := w25e2eScope(t, ambDir, "--base", "HEAD")
	if amb.mode != "full" || !reflect.DeepEqual(amb.commands, []string{"echo amb-fast", "echo amb-full"}) {
		t.Fatalf("an ambiguous repo must report its conf's commands on full, got mode %q commands %q", amb.mode, amb.commands)
	}
	if ja := w114vsJSON(t, ambDir, nil, "--base", "HEAD"); ja.Stack != "ambiguous" || ja.Analyzer != nil {
		t.Fatalf("ambiguous JSON = stack %q analyzer %v", ja.Stack, ja.Analyzer)
	}

	// No conf: still no commands, and the diagnostic says so.
	bare, _ := w25e2eFixture(t, map[string]string{"Cargo.toml": "[package]\nname = \"w114\"\n"})
	out, errb, code = w25e2eRun(t, bare, nil)
	if code != 0 {
		t.Fatalf("a stackless repo without a conf must exit 0, got %d", code)
	}
	if nr := w25e2eParse(t, out); nr.mode != "full" || len(nr.commands) != 0 {
		t.Fatalf("a stackless repo without a conf reports no command, got mode %q %q", nr.mode, nr.commands)
	}
	if !strings.Contains(errb, "no commands") {
		t.Fatalf("the no-conf fallback must say it reports no commands: %q", errb)
	}
}

// TestW114VerifyScopeStackNonePin: `stack=none` turns the analyzer off even
// where go.mod exists — always full, the conf's commands only, never a Go
// default — and a stack=none conf without commands reports none.
func TestW114VerifyScopeStackNonePin(t *testing.T) {
	files := w25e2eGoModule("fixture.test/w114")
	files[".polyforge/verify-scope.conf"] = "stack=none\nfast_gate=echo conf-fast\nfull_command=echo conf-full\n"
	dir, base := w25e2eFixture(t, files)
	want := []string{"echo conf-fast", "echo conf-full"}
	empty := w25e2eScope(t, dir, "--base", "HEAD")
	if empty.mode != "full" || !reflect.DeepEqual(empty.commands, want) {
		t.Fatalf("stack=none on an empty diff = mode %q commands %q, want full %q", empty.mode, empty.commands, want)
	}
	w25e2eWrite(t, dir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 2 }\n")
	w25e2eCommit(t, dir, "change leaf")
	r := w25e2eScope(t, dir, "--base", base)
	if r.mode != "full" || !reflect.DeepEqual(r.commands, want) || strings.Contains(r.joined(), "go build") || strings.Contains(r.joined(), "go test") {
		t.Fatalf("stack=none must never scope or add Go defaults, got mode %q commands %q", r.mode, r.commands)
	}
	j := w114vsJSON(t, dir, nil, "--base", base)
	if j.Stack != "none" || j.Analyzer != nil || j.Mode != "full" {
		t.Fatalf("stack=none JSON = stack %q analyzer %v mode %q", j.Stack, j.Analyzer, j.Mode)
	}

	nocmd := w25e2eGoModule("fixture.test/w114")
	nocmd[".polyforge/verify-scope.conf"] = "stack=none\n"
	ncDir, _ := w25e2eFixture(t, nocmd)
	out, errb, code := w25e2eRun(t, ncDir, nil, "--base", "HEAD")
	if code != 0 {
		t.Fatalf("stack=none without commands must exit 0, got %d", code)
	}
	if nr := w25e2eParse(t, out); nr.mode != "full" || len(nr.commands) != 0 {
		t.Fatalf("stack=none without commands = mode %q commands %q, want full and none", nr.mode, nr.commands)
	}
	if !strings.Contains(errb, "stack=none") || !strings.Contains(errb, "declares none") {
		t.Fatalf("stack=none without commands must say so on stderr: %q", errb)
	}
}

// TestW114VerifyScopeJSONContract: --json emits the schema-1 object for a
// conf-heavy Go repo (scoped and --full), a zero-config Go repo, a
// non-default default branch and a non-git tree.
func TestW114VerifyScopeJSONContract(t *testing.T) {
	conf := strings.Join([]string{
		"stack=go",
		"check_requires_env=lint|W114VS_TOKEN,HOME",
		"fast_gate=echo fast-anon",
		"check=lint|fast|echo lint",
		"check_requires_cmd=lint|sh",
		"check_timeout=lint|90s",
		"full_command=echo full-anon",
		"check=e2e|full|echo e2e",
		"check_timeout=e2e|1h30m",
		"check_requires_env=fast-1|HOME",
		"test_glob=e2e/*.mjs",
		"test_glob=tests/**/test_*.py",
		"base_setup=cd webui && npm ci",
		"base_setup=make deps",
		"runner=complete",
		"ci_evidence=ci.yml/test",
		"ci_evidence=ci.yml/e2e",
		"mystery=1",
	}, "\n") + "\n"
	files := w25e2eGoModule("fixture.test/w114")
	files[".polyforge/verify-scope.conf"] = conf
	dir, base := w25e2eFixture(t, files)
	w25e2eWrite(t, dir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 2 }\n")
	head := w25e2eCommit(t, dir, "change leaf")

	s := w114vsJSON(t, dir, nil)
	if s.Stack != "go" || s.Analyzer == nil || *s.Analyzer != "go" || s.Conf != "present" || s.Mode != "scoped" {
		t.Fatalf("scoped JSON = stack %q analyzer %v conf %q mode %q", s.Stack, s.Analyzer, s.Conf, s.Mode)
	}
	if s.Base == nil || *s.Base != base || s.Head == nil || *s.Head != head || s.DefaultBranch != "main" {
		t.Fatalf("JSON base/head/default = %v/%v/%q, want %s/%s/main", s.Base, s.Head, s.DefaultBranch, base, head)
	}
	if s.ConfChangedInDiff || !reflect.DeepEqual(s.Paths, []string{"leaf/leaf.go"}) {
		t.Fatalf("JSON paths %q conf_changed_in_diff %t, want [leaf/leaf.go] false", s.Paths, s.ConfChangedInDiff)
	}
	plan := w114vsPlan(s)
	if len(plan) != 3 || plan[0] != "fast-1:fast:conf:echo fast-anon" || plan[1] != "lint:fast:conf:echo lint" ||
		!strings.HasPrefix(plan[2], "scoped-1:scoped:default:GOWORK=off go test -count=1 ") {
		t.Fatalf("scoped JSON plan = %q, want the conf's fast lines then the analyzer's scoped selection", plan)
	}
	by := w114vsByName(s)
	if c := by["lint"]; !reflect.DeepEqual(c.RequiresEnv, []string{"W114VS_TOKEN", "HOME"}) || !reflect.DeepEqual(c.RequiresCmd, []string{"sh"}) || w114vsTO(c) != "90s" {
		t.Fatalf("lint check prerequisites = %+v (timeout %s), want its declared 90s", c, w114vsTO(c))
	}
	if c := by["fast-1"]; !reflect.DeepEqual(c.RequiresEnv, []string{"HOME"}) || len(c.RequiresCmd) != 0 || w114vsTO(c) != "<null>" {
		t.Fatalf("an anonymous line binds by its fast-<i> name, and with no declared timeout carries null (timeout %s): %+v", w114vsTO(c), c)
	}
	if c := by["scoped-1"]; len(c.RequiresEnv) != 0 || len(c.RequiresCmd) != 0 || w114vsTO(c) != "<null>" {
		t.Fatalf("an analyzer command carries no prerequisites and a null timeout (timeout %s): %+v", w114vsTO(c), c)
	}
	if !reflect.DeepEqual(s.TestGlob, []string{"e2e/*.mjs", "tests/**/test_*.py"}) ||
		!reflect.DeepEqual(s.BaseSetup, []string{"cd webui && npm ci", "make deps"}) ||
		s.Runner == nil || *s.Runner != "complete" ||
		!reflect.DeepEqual(s.CIEvidence, []string{"ci.yml/test", "ci.yml/e2e"}) {
		t.Fatalf("conf echoes = test_glob %q base_setup %q runner %v ci_evidence %q", s.TestGlob, s.BaseSetup, s.Runner, s.CIEvidence)
	}
	if len(s.Diagnostics) == 0 || !strings.Contains(strings.Join(s.Diagnostics, "\n"), "unknown key 'mystery'") {
		t.Fatalf("the unknown-key diagnostic must be collected: %q", s.Diagnostics)
	}

	// --full forces the full tier and its commands; it never narrows.
	f := w114vsJSON(t, dir, nil, "--full")
	wantFull := []string{
		"fast-1:fast:conf:echo fast-anon",
		"lint:fast:conf:echo lint",
		"full-1:full:conf:echo full-anon",
		"e2e:full:conf:echo e2e",
	}
	fby := w114vsByName(f)
	if f.Mode != "full" || !reflect.DeepEqual(w114vsPlan(f), wantFull) || w114vsTO(fby["e2e"]) != "1h30m" || w114vsTO(fby["full-1"]) != "<null>" {
		t.Fatalf("--full JSON = mode %q plan %q (e2e timeout %s, full-1 timeout %s), want e2e's declared 1h30m and null for the undeclared full-1",
			f.Mode, w114vsPlan(f), w114vsTO(fby["e2e"]), w114vsTO(fby["full-1"]))
	}

	// Zero-config Go: defaults only, conf absent, empty echoes as arrays.
	zDir, _ := w25e2eFixture(t, w25e2eGoModule("fixture.test/w114"))
	w25e2eWrite(t, zDir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 3 }\n")
	w25e2eCommit(t, zDir, "change leaf")
	z := w114vsJSON(t, zDir, nil)
	if z.Conf != "absent" || z.Runner != nil || len(z.TestGlob) != 0 || len(z.BaseSetup) != 0 || len(z.CIEvidence) != 0 {
		t.Fatalf("zero-config JSON = conf %q runner %v test_glob %q base_setup %q ci_evidence %q", z.Conf, z.Runner, z.TestGlob, z.BaseSetup, z.CIEvidence)
	}
	for _, c := range z.Commands {
		if c.Timeout != nil {
			t.Fatalf("a zero-config default command declares no timeout, so it carries null: %s = %q", c.Name, *c.Timeout)
		}
	}
	zplan := w114vsPlan(z)
	if len(zplan) != 4 || zplan[0] != "fast-1:fast:default:GOWORK=off go build ./..." ||
		zplan[2] != `fast-3:fast:default:test -z "$(gofmt -l .)"` || !strings.HasPrefix(zplan[3], "scoped-1:scoped:default:") {
		t.Fatalf("zero-config JSON plan = %q", zplan)
	}
	zf := w114vsJSON(t, zDir, nil, "--full")
	if zfp := w114vsPlan(zf); len(zfp) != 4 || zfp[3] != "full-1:full:default:GOWORK=off go test -count=1 ./..." {
		t.Fatalf("zero-config --full JSON plan = %q", zfp)
	}

	// The default branch is whatever origin/HEAD names.
	w25e2eGit(t, zDir, "update-ref", "refs/remotes/origin/trunk", w25e2eGit(t, zDir, "rev-parse", "refs/remotes/origin/main"))
	w25e2eGit(t, zDir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	if tr := w114vsJSON(t, zDir, nil); tr.DefaultBranch != "trunk" {
		t.Fatalf("default_branch = %q, want trunk (origin/HEAD)", tr.DefaultBranch)
	}

	// A non-git tree: no base, no head, full.
	nonGit := t.TempDir()
	w25e2eWrite(t, nonGit, "go.mod", "module fixture.test/w114\n\ngo 1.21\n")
	ng := w114vsJSON(t, nonGit, nil)
	if ng.Base != nil || ng.Head != nil || ng.Mode != "full" || ng.ConfChangedInDiff {
		t.Fatalf("non-git JSON = base %v head %v mode %q conf_changed %t", ng.Base, ng.Head, ng.Mode, ng.ConfChangedInDiff)
	}
}

// TestW114VerifyScopeConfChangedInDiff: true exactly when the conf is among
// the changed paths or differs between base and HEAD — false for an
// unrelated change and for a change reverted within the range.
func TestW114VerifyScopeConfChangedInDiff(t *testing.T) {
	files := w25e2eGoModule("fixture.test/w114")
	files[".polyforge/verify-scope.conf"] = "stack=go\nfast_gate=echo one\n"
	dir, base := w25e2eFixture(t, files)

	w25e2eWrite(t, dir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 2 }\n")
	unrelated := w25e2eCommit(t, dir, "unrelated change")
	if r := w114vsJSON(t, dir, nil); r.ConfChangedInDiff {
		t.Fatalf("an unrelated change must not report conf_changed_in_diff")
	}

	w25e2eWrite(t, dir, ".polyforge/verify-scope.conf", "stack=go\nfast_gate=echo two\n")
	w25e2eCommit(t, dir, "change the conf")
	if r := w114vsJSON(t, dir, nil); !r.ConfChangedInDiff || !strings.Contains(strings.Join(r.Paths, ","), ".polyforge/verify-scope.conf") {
		t.Fatalf("a conf change must report conf_changed_in_diff (default base), paths %q", r.Paths)
	}
	if r := w114vsJSON(t, dir, nil, "--base", unrelated); !r.ConfChangedInDiff {
		t.Fatalf("a conf change must report conf_changed_in_diff (--base)")
	}
	if r := w114vsJSON(t, dir, nil, "--base", "HEAD"); r.ConfChangedInDiff {
		t.Fatalf("an empty diff must not report conf_changed_in_diff")
	}

	// Reverted within the range: base and HEAD carry the same conf.
	w25e2eWrite(t, dir, ".polyforge/verify-scope.conf", "stack=go\nfast_gate=echo one\n")
	w25e2eCommit(t, dir, "revert the conf")
	if r := w114vsJSON(t, dir, nil, "--base", base); r.ConfChangedInDiff {
		t.Fatalf("a conf change reverted within the range must not report conf_changed_in_diff, paths %q", r.Paths)
	}

	// Deleting the conf is a change too.
	if err := os.Remove(filepath.Join(dir, ".polyforge", "verify-scope.conf")); err != nil {
		t.Fatal(err)
	}
	w25e2eCommit(t, dir, "delete the conf")
	if r := w114vsJSON(t, dir, nil, "--base", base); !r.ConfChangedInDiff || r.Conf != "absent" {
		t.Fatalf("a deleted conf must report conf_changed_in_diff with conf absent: %t %q", r.ConfChangedInDiff, r.Conf)
	}
}

// TestW114VerifyScopeNewKeysParsed: check= lines interleave with the
// anonymous lines by phase in file order, check_* lines may precede their
// check, and every malformed new-key line is diagnosed and ignored.
func TestW114VerifyScopeNewKeysParsed(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pwned")
	conf := strings.Join([]string{
		"stack=none",
		"check_timeout=lint|5m",
		"fast_gate=echo A",
		"full_command=echo C",
		"check=lint|fast|echo B",
		"check=e2e|full|echo D",
		"fast_gate=echo E",
		"full_command=echo F",
		"check=fast-9|fast|echo reserved",
		"check=bad name|fast|echo malformed",
		"check=slowpoke|slow|echo bad-phase",
		"check=lint|full|echo duplicate",
		"check=noshape",
		"check=hook|fast|$(touch " + marker + ")",
		"check_requires_env=lint|1BAD",
		"check_requires_cmd=lint|has space",
		"check_requires_env=ghost|HOME",
		"check_timeout=e2e|20 minutes",
		"check_timeout=e2e|0s",
		"check_timeout=lint|9m",
		"runner=partial",
		"ci_evidence=no-slash",
		"base_setup=$(touch " + marker + ")",
		"test_glob=",
		"mystery=value",
	}, "\n") + "\n"
	dir, _ := w25e2eFixture(t, map[string]string{".polyforge/verify-scope.conf": conf})

	out, errb, code := w25e2eRun(t, dir, nil, "--base", "HEAD")
	if code != 0 {
		t.Fatalf("a conf with rejected lines must still report, got exit %d (%s)", code, errb)
	}
	want := []string{"echo A", "echo B", "echo E", "echo C", "echo D", "echo F"}
	if r := w25e2eParse(t, out); !reflect.DeepEqual(r.commands, want) {
		t.Fatalf("command order = %q, want fast lines then full lines, each in file order: %q", r.commands, want)
	}
	for _, diag := range []string{
		"check name 'fast-9' (reserved",
		"check name 'bad name'",
		"check phase 'slow'",
		"a second check named 'lint'",
		"check without the <name>|<fast|full>|<command> shape",
		"conf lint: check value begins with a command substitution",
		"check_requires_env value",
		"check_requires_cmd value",
		"check_requires_env for 'ghost', which names no declared command",
		"check_timeout value (expected <name>|<positive Go duration",
		"a second check_timeout for 'lint'",
		"runner 'partial'",
		"ci_evidence value",
		"conf lint: base_setup value begins with a command substitution",
		"an empty test_glob",
		"unknown key 'mystery'",
	} {
		if !strings.Contains(errb, diag) {
			t.Fatalf("stderr lacks the diagnostic %q:\n%s", diag, errb)
		}
	}
	j := w114vsJSON(t, dir, nil, "--base", "HEAD")
	wantPlan := []string{
		"fast-1:fast:conf:echo A",
		"lint:fast:conf:echo B",
		"fast-2:fast:conf:echo E",
		"full-1:full:conf:echo C",
		"e2e:full:conf:echo D",
		"full-2:full:conf:echo F",
	}
	if got := w114vsPlan(j); !reflect.DeepEqual(got, wantPlan) {
		t.Fatalf("JSON plan = %q, want %q", got, wantPlan)
	}
	by := w114vsByName(j)
	if w114vsTO(by["lint"]) != "5m" || len(by["lint"].RequiresEnv) != 0 || len(by["lint"].RequiresCmd) != 0 {
		t.Fatalf("lint keeps its first valid timeout (got %s) and no rejected prerequisite: %+v", w114vsTO(by["lint"]), by["lint"])
	}
	for _, name := range []string{"e2e", "fast-1", "full-2"} {
		if got := w114vsTO(by[name]); got != "<null>" {
			t.Fatalf("%s declares no valid timeout, so it carries null (no engine default), got %s", name, got)
		}
	}
	if j.Runner != nil || len(j.CIEvidence) != 0 || len(j.BaseSetup) != 0 || len(j.TestGlob) != 0 {
		t.Fatalf("rejected echoes must contribute nothing: runner %v ci %q base_setup %q test_glob %q", j.Runner, j.CIEvidence, j.BaseSetup, j.TestGlob)
	}
	if _, _, code := w25e2eRun(t, dir, nil, "--base", "HEAD", "--run"); code != 0 {
		t.Fatalf("--run over the accepted commands must pass, got %d", code)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("a lint-diagnosed command substitution was executed")
	}
}

// TestW114VerifyScopeRunUsesBash: --run executes each command with
// `bash -c` (a bash-only construct runs; /bin/sh as the negative control),
// with stdin closed, skips a command whose declared prerequisites are
// missing, and still stops at the first failure.
func TestW114VerifyScopeRunUsesBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash is required by --run: %v", err)
	}
	bashOnly := `[[ -n "${HOME:-x}" ]] && arr=(a b c) && echo "bash ${#arr[@]}" > bash.txt`
	conf := strings.Join([]string{
		"stack=none",
		"fast_gate=" + bashOnly,
		`fast_gate=read -r line; printf '%s' "${line:-}" > stdin.txt`,
		"check=needs|full|touch needs.txt",
		"check_requires_env=needs|W114VS_RUN_TOKEN",
		"check_requires_cmd=needs|w114vs-no-such-binary",
		"check=needs-env|full|touch needs-env.txt",
		"check_requires_env=needs-env|W114VS_RUN_TOKEN",
		"full_command=touch last.txt",
	}, "\n") + "\n"
	dir, _ := w25e2eFixture(t, map[string]string{".polyforge/verify-scope.conf": conf})

	// Negative control: the same string under /bin/sh.
	if err := exec.Command("sh", "-c", `[[ -n "x" ]] && arr=(a b c) && true`).Run(); err == nil {
		t.Logf("/bin/sh accepts bash-only syntax here; the bash.txt assertion below still pins execution")
	}

	_, errb, code := w25e2eRun(t, dir, []string{"W114VS_RUN_TOKEN="}, "--base", "HEAD", "--run")
	if code != 0 {
		t.Fatalf("--run with bash-only syntax must pass, got %d (%s)", code, errb)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "bash.txt")); err != nil || strings.TrimSpace(string(b)) != "bash 3" {
		t.Fatalf("the bash-only command did not run under bash: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "stdin.txt")); err != nil || len(b) != 0 {
		t.Fatalf("a command must read a closed stdin, never the engine's command list: %q %v", b, err)
	}
	for _, f := range []string{"needs.txt", "needs-env.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Fatalf("%s ran although its declared prerequisites are missing (an empty variable counts as missing)", f)
		}
	}
	if !strings.Contains(errb, "skipped needs (missing env W114VS_RUN_TOKEN, command w114vs-no-such-binary)") ||
		!strings.Contains(errb, "skipped needs-env (missing env W114VS_RUN_TOKEN)") {
		t.Fatalf("a skipped command must name what is missing: %q", errb)
	}
	if _, err := os.Stat(filepath.Join(dir, "last.txt")); err != nil {
		t.Fatalf("a skipped command must not stop the run: %v", err)
	}

	// With the variable set, the command whose prerequisites hold runs.
	if _, errb, code := w25e2eRun(t, dir, []string{"W114VS_RUN_TOKEN=set"}, "--base", "HEAD", "--run"); code != 0 {
		t.Fatalf("--run with the variable set: exit %d (%s)", code, errb)
	}
	if _, err := os.Stat(filepath.Join(dir, "needs-env.txt")); err != nil {
		t.Fatalf("the command whose prerequisites hold did not run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "needs.txt")); err == nil {
		t.Fatalf("the command missing an executable still ran")
	}

	// Stop at the first failure (a bash-only failure included).
	failDir, _ := w25e2eFixture(t, map[string]string{
		".polyforge/verify-scope.conf": "stack=none\nfast_gate=[[ 1 -eq 2 ]]\nfull_command=touch after.txt\n",
	})
	if _, errb, code := w25e2eRun(t, failDir, nil, "--base", "HEAD", "--run"); code == 0 || !strings.Contains(errb, "reported command failed: [[ 1 -eq 2 ]]") {
		t.Fatalf("--run must stop non-zero at the first failure, got %d (%s)", code, errb)
	}
	if _, err := os.Stat(filepath.Join(failDir, "after.txt")); err == nil {
		t.Fatalf("--run continued past the first failure")
	}
}
