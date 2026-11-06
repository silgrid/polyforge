package cli

// w371_verify_scope_since_test.go: — `workflows/_shared/scripts/verify-scope
// --since <verified head>`: whether the upstream a delivery merged in after
// verification (<head>..HEAD) touches the affected set of the work item's
// own diff. Symmetric — each side's Go packages plus their reverse-
// dependency closure, the suites its carries select, the paths it changes —
// so an upstream change to a DEPENDENCY of the work item's packages meets
// it too, not only one to a reverse dependency. Any failure to compute
// answers intersects: true. Every case is a real merge in a git fixture,
// run through the engine's documented surface (`--base <default tip>
// --since <verified head> --json`).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w371SinceReport is the report with its --since answer.
type w371SinceReport struct {
	Since *struct {
		Head       string   `json:"head"`
		Intersects bool     `json:"intersects"`
		Paths      []string `json:"paths"`
		Reason     string   `json:"reason"`
	} `json:"since"`
}

// w371Since builds base on main, commits ticket on a feature branch (the
// verified head), commits upstream on main, merges main into the feature
// branch the way delivery does, and answers the engine's --since report
// against the verified head.
func w371Since(t *testing.T, base, ticket, upstream map[string]string) w371SinceReport {
	t.Helper()
	dir := w371Repo(t, base)
	w248Git(t, dir, "checkout", "-q", "-b", "feature")
	for rel, body := range ticket {
		w248Write(t, dir, rel, body)
	}
	w248Git(t, dir, "add", "-A")
	w248Git(t, dir, "commit", "-q", "-m", "ticket")
	verified := w248Git(t, dir, "rev-parse", "HEAD")
	w248Git(t, dir, "checkout", "-q", "main")
	for rel, body := range upstream {
		w248Write(t, dir, rel, body)
	}
	w248Git(t, dir, "add", "-A")
	w248Git(t, dir, "commit", "-q", "--allow-empty", "-m", "upstream")
	tip := w248Git(t, dir, "rev-parse", "HEAD")
	w248Git(t, dir, "checkout", "-q", "feature")
	w248Git(t, dir, "merge", "-q", "--no-edit", "main")
	return w371SinceRun(t, dir, "--base", tip, "--since", verified)
}

func w371SinceRun(t *testing.T, dir string, args ...string) w371SinceReport {
	t.Helper()
	cmd := exec.Command(w371Engine(t), append([]string{"--json"}, args...)...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("workflows/_shared/scripts/verify-scope %v: %v\nstderr: %s", args, err, stderr.String())
	}
	var r w371SinceReport
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if r.Since == nil {
		t.Fatalf("--since asked, but the report carries no since object:\n%s", out)
	}
	return r
}

// w371GoModule is a module of three packages: a imports b, c stands alone.
func w371GoModule() map[string]string {
	return map[string]string{
		"go.mod":      "module fixture.test/since\n\ngo 1.21\n",
		"a/a.go":      "package a\n\nimport \"fixture.test/since/b\"\n\nfunc A() int { return b.B() }\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { _ = A() }\n",
		"b/b.go":      "package b\n\nfunc B() int { return 1 }\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) { _ = B() }\n",
		"c/c.go":      "package c\n\nfunc C() int { return 3 }\n",
		"c/c_test.go": "package c\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) { _ = C() }\n",
		"docs/x.md":   "# x\n\na\nb\nc\nd\ne\n",
		"docs/y.md":   "# y\n",
	}
}

func w371WithConf(files map[string]string, conf string) map[string]string {
	out := map[string]string{".polyforge/verify-scope.conf": conf}
	for k, v := range files {
		out[k] = v
	}
	return out
}

func TestW371VerifyScopeSinceGo(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH: the Go analyzer needs go list")
	}
	editA := map[string]string{"a/a.go": "package a\n\nimport \"fixture.test/since/b\"\n\nfunc A() int { return b.B() + 1 }\n"}
	editB := map[string]string{"b/b.go": "package b\n\nfunc B() int { return 2 }\n"}
	editC := map[string]string{"c/c.go": "package c\n\nfunc C() int { return 4 }\n"}
	cases := []struct {
		name             string
		base             map[string]string
		ticket, upstream map[string]string
		want             bool
		paths            []string
		reason           string
	}{
		{"an upstream change in an unrelated package", w371GoModule(), editA, editC, false, nil, "touches nothing"},
		{"the upstream changes a dependency of the ticket's package", w371GoModule(), editA, editB, true, []string{"a"}, "packages both reach (a)"},
		{"the upstream changes a reverse dependency of the ticket's package", w371GoModule(), editB, editA, true, []string{"a"}, "packages both reach (a)"},
		{"the same non-Go file on both sides", w371GoModule(), map[string]string{"docs/x.md": "# x2\n\na\nb\nc\nd\ne\n"}, map[string]string{"docs/x.md": "# x\n\na\nb\nc\nd\ne2\n"}, true, []string{"docs/x.md"}, "paths both change"},
		{"different non-Go files", w371GoModule(), map[string]string{"docs/x.md": "# x2\n"}, map[string]string{"docs/y.md": "# y2\n"}, false, nil, "touches nothing"},
		{"the upstream changes a forced-full path", w371GoModule(), editA, map[string]string{"go.sum": "\n"}, true, []string{"go.sum"}, "go.sum is forced full"},
		{"the upstream changes the scope conf", w371WithConf(w371GoModule(), "stack=go\n"), editA, map[string]string{".polyforge/verify-scope.conf": "stack=go\nforced_full=never/*\n"}, true, nil, "verify-scope.conf"},
		{"both reach the declared harness", w371WithConf(w371GoModule(), "stack=go\ngo_harness=e2e/run.sh\n"),
			editA, editC, true, []string{"e2e/run.sh"}, "go_harness"},
		{"an upstream import the package graph cannot resolve", w371GoModule(), editA, map[string]string{"c/c.go": "package c\n\nimport \"fixture.test/since/missing\"\n\nfunc C() int { return missing.M() }\n"}, true, nil, "go list failed"},
	}
	// the harness case: one acceptance test names both changed files
	cases[7].base = w371WithConf(w371GoModule(), "stack=go\ngo_harness=e2e/run.sh\n")
	cases[7].base["e2e/run.sh"] = "go test ./e2e/\n"
	cases[7].base["e2e/flow_test.go"] = "package e2e\n\nimport \"testing\"\n\n// covers a/a.go and c/c.go\nfunc TestFlow(t *testing.T) {}\n"
	for _, c := range cases {
		r := w371Since(t, c.base, c.ticket, c.upstream)
		s := r.Since
		if s.Intersects != c.want || !strings.Contains(s.Reason, c.reason) {
			t.Errorf("%s: intersects=%t reason=%q, want %t with %q", c.name, s.Intersects, s.Reason, c.want, c.reason)
		}
		if c.paths != nil && strings.Join(s.Paths, ",") != strings.Join(c.paths, ",") {
			t.Errorf("%s: paths=%q, want %q", c.name, s.Paths, c.paths)
		}
		if len(s.Head) != 40 {
			t.Errorf("%s: head %q is not the resolved sha", c.name, s.Head)
		}
	}
}

func TestW371VerifyScopeSinceFailsClosed(t *testing.T) {
	dir := w371Repo(t, map[string]string{".polyforge/verify-scope.conf": "stack=none\nfast_gate=true\n", "a.txt": "a\n"})
	w248Write(t, dir, "a.txt", "b\n")
	w248Git(t, dir, "commit", "-q", "-am", "ticket")
	if s := w371SinceRun(t, dir, "--base", "HEAD~1", "--since", "no-such-rev").Since; !s.Intersects || s.Head != "no-such-rev" || !strings.Contains(s.Reason, "cannot resolve") {
		t.Errorf("an unresolvable --since: %+v", s)
	}
	// nothing changed since the verified head: nothing to re-verify
	if s := w371SinceRun(t, dir, "--base", "HEAD~1", "--since", "HEAD").Since; s.Intersects || !strings.Contains(s.Reason, "nothing changed") {
		t.Errorf("--since HEAD: %+v", s)
	}
	// no analyzer: the affected sets cannot be computed
	if s := w371SinceRun(t, dir, "--base", "HEAD~1", "--since", "HEAD~1").Since; !s.Intersects || !strings.Contains(s.Reason, "no analyzer") {
		t.Errorf("stack=none: %+v", s)
	}
}

func TestW371VerifyScopeSinceCarries(t *testing.T) {
	conf := "fast_gate=echo FAST\ne2e_runner=npm run test:e2e:all --\nfull_command=npm run test:e2e:all\n"
	cases := []struct {
		name             string
		ticket, upstream map[string]string
		want             bool
		reason           string
	}{
		{"suites the two sides select are disjoint", map[string]string{"src/panel.ts": "2\n"}, map[string]string{"src/other.ts": "2\n"}, false, "touches nothing"},
		{"both select the same suite", map[string]string{"src/panel.ts": "2\n"}, map[string]string{"e2e/panel.mjs": "// carries: src/panel*\nconsole.log(2)\n"}, true, "suites both select (e2e/panel.mjs)"},
		{"the upstream changes a helper the ticket's suite imports", map[string]string{"src/other.ts": "2\n"}, map[string]string{"e2e/helper.mjs": "export const h = 2;\n"}, true, "e2e/other.mjs"},
		{"an upstream path no carries declaration covers", map[string]string{"src/panel.ts": "2\n"}, map[string]string{"lib/unrelated.c": "int y;\n"}, true, "covered by no carries declaration"},
	}
	for _, c := range cases {
		s := w371Since(t, smokeFiles(conf), c.ticket, c.upstream).Since
		if s.Intersects != c.want || !strings.Contains(s.Reason, c.reason) {
			t.Errorf("%s: intersects=%t reason=%q, want %t with %q", c.name, s.Intersects, s.Reason, c.want, c.reason)
		}
	}
}

// w371HarnessModule is a module with a declared go_harness whose test
// binary imports two packages that do not import each other.
func w371HarnessModule() map[string]string {
	return map[string]string{
		".polyforge/verify-scope.conf": "stack=go\ngo_harness=test/e2e-parallel.sh\n",
		"go.mod":                       "module fixture.test/since\n\ngo 1.21\n",
		"internal/polyforge/cli/verbs_verifyrun.go":      "package cli\n\nfunc Verb() int { return 1 }\n",
		"internal/polyforge/cli/verbs_verifyrun_test.go": "package cli\n\nimport \"testing\"\n\nfunc TestVerb(t *testing.T) { _ = Verb() }\n",
		"internal/common/store/store.go":                 "package store\n\nfunc Get() int { return 2 }\n",
		"test/e2e-parallel.sh":                           "go test ./test/\n",
		"test/harness_test.go": "package test\n\nimport (\n\t\"testing\"\n\n\t\"fixture.test/since/internal/common/store\"\n" +
			"\t\"fixture.test/since/internal/polyforge/cli\"\n)\n\nfunc TestHarness(t *testing.T) { _ = cli.Verb() + store.Get() }\n",
	}
}

// TestW371VerifyScopeSinceHarness: the declared harness's directory is out
// of the scoped selection's package graph, but --since must not fail open
// through it. A ticket that only adds a harness test, against an upstream
// change to a package that harness's test binary imports, intersects (the
// review's reproduction: the pre-fix engine answered false); and two
// unrelated packages the harness imports meet in the harness package.
func TestW371VerifyScopeSinceHarness(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH: the Go analyzer needs go list")
	}
	probe := map[string]string{"test/zz_probe_test.go": "package test\n\nimport \"testing\"\n\nfunc TestProbe(t *testing.T) {}\n"}
	cliEdit := map[string]string{"internal/polyforge/cli/verbs_verifyrun.go": "package cli\n\nfunc Verb() int { return 3 }\n"}
	storeEdit := map[string]string{"internal/common/store/store.go": "package store\n\nfunc Get() int { return 4 }\n"}

	s := w371Since(t, w371HarnessModule(), probe, cliEdit).Since
	if !s.Intersects || !strings.Contains(s.Reason, "go_harness directory test") || strings.Join(s.Paths, ",") != "test/zz_probe_test.go" {
		t.Errorf("a ticket adding only a harness test vs an upstream change the harness imports: %+v, want intersects", s)
	}
	s = w371Since(t, w371HarnessModule(), cliEdit, probe).Since
	if !s.Intersects || !strings.Contains(s.Reason, "the upstream since") || !strings.Contains(s.Reason, "go_harness directory") {
		t.Errorf("the mirror (upstream adds the harness test): %+v, want intersects", s)
	}
	s = w371Since(t, w371HarnessModule(), cliEdit, storeEdit).Since
	if !s.Intersects || strings.Join(s.Paths, ",") != "test" || !strings.Contains(s.Reason, "packages both reach (test)") {
		t.Errorf("two packages only the harness imports both: %+v, want intersects in the harness package", s)
	}
}

// w248Git runs git in dir with a fixed identity (the engine fixtures'
// helper, kept from the deleted scoped-gates parity test: its parity moved
// to the workflow's scope_ready.py tests).
func w248Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=w248@example.invalid", "-c", "user.name=w248", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// w248Write writes one fixture file.
func w248Write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
