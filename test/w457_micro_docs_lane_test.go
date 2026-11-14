// carries: .polyforge/verify-scope.conf, workflows/_shared/scripts/verify-scope, docs/concepts.md, docs/contributing.md
package test

// w457_micro_docs_lane_test.go: this repository opts its docs into the micro
// lane. The suite pins three facts about the root conf:
//   - its micro_path= set covers exactly the docs a text-only change may
//     touch, and leaves out the append-only, hash-pinned or executable ones;
//   - `workflows/_shared/scripts/verify-scope --micro --json` reports that set and the
//     micro_check= commands, run after the fast phase;
//   - breaking a sentence a contract test pins, in an in-scope doc, makes
//     `workflows/_shared/scripts/verify-scope --micro --run` exit non-zero through a micro_check.
//
// `--micro` reads the conf at the diff base, so each test commits this
// tree's conf as the base of a fixture instead of reading the real repo,
// which carries the declaration only once it is merged.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW457' -count=1 -v

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// w457MicroPaths is the declared micro_path= set, in file order.
var w457MicroPaths = []string{
	"docs/architecture.md",
	"docs/bundles.md",
	"docs/concepts.md",
	"docs/contributing.md",
	"docs/extending.md",
	"docs/install.md",
	"docs/quickstart-developers.md",
	"docs/quickstart-macos.md",
	"docs/user-guide.md",
	"docs/zh.md",
	"docs/reference/*.md",
	"docs/design/polyforge-vnext/*.md",
	"docs/design/polyforge-vnext/archive/*.md",
}

// w457Excluded are the docs paths left out of micro on purpose (a path
// equal to an entry, or under an entry ending in `/`).
var w457Excluded = []string{
	"docs/upgrade.md",
	"docs/sync/",
	"docs/migration/",
	"docs/design/polyforge-vnext/tools/",
}

// w457Match is the micro_path glob semantics: segment by segment, `*`
// within one segment, `**` zero or more whole segments.
func w457Match(pattern, p string) bool {
	return w457MatchSegs(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func w457MatchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if w457MatchSegs(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
		return false
	}
	return w457MatchSegs(pat[1:], segs[1:])
}

func w457IsExcluded(p string) bool {
	for _, e := range w457Excluded {
		if p == e || (strings.HasSuffix(e, "/") && strings.HasPrefix(p, e)) {
			return true
		}
	}
	return false
}

type w457Report struct {
	MicroPaths  []string `json:"micro_paths"`
	MicroChecks []string `json:"micro_checks"`
	Commands    []struct {
		Name  string `json:"name"`
		Phase string `json:"phase"`
		Cmd   string `json:"cmd"`
	} `json:"commands"`
}

func w457Parse(t *testing.T, out string) w457Report {
	t.Helper()
	var r w457Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("--micro --json is not JSON: %v\n%s", err, out)
	}
	return r
}

// TestW457MicroJSONReportsTheDocsDeclaration: the conf's micro_path= set is
// exactly the safe docs, and --micro --json reports it with the
// micro_check= commands after the fast phase.
func TestW457MicroJSONReportsTheDocsDeclaration(t *testing.T) {
	dir, base := w25e2eFixture(t, map[string]string{
		"go.mod":                       "module fixture.test/w457\n\ngo 1.22\n",
		".polyforge/verify-scope.conf": w25engShippedConf(t),
	})
	out, errb, code := w25e2eRun(t, dir, nil, "--micro", "--json", "--base", base)
	if code != 0 {
		t.Fatalf("--micro --json exit %d, want 0\nstderr: %s", code, errb)
	}
	r := w457Parse(t, out)
	if strings.Join(r.MicroPaths, "\n") != strings.Join(w457MicroPaths, "\n") {
		t.Fatalf("micro_paths = %q\nwant %q", r.MicroPaths, w457MicroPaths)
	}
	if len(r.MicroChecks) == 0 {
		t.Fatalf("micro_checks is empty: the micro evidence must run the doc-reading contract tests")
	}
	lastFast, firstMicro := -1, -1
	micro := 0
	for i, c := range r.Commands {
		switch c.Phase {
		case "fast":
			lastFast = i
		case "micro":
			if firstMicro < 0 {
				firstMicro = i
			}
			if micro >= len(r.MicroChecks) || c.Cmd != r.MicroChecks[micro] {
				t.Fatalf("micro command %d = %q, want micro_checks[%d] of %q", i, c.Cmd, micro, r.MicroChecks)
			}
			micro++
			if want := "micro-" + strconv.Itoa(micro); c.Name != want {
				t.Fatalf("micro command name = %q, want %q", c.Name, want)
			}
		default:
			t.Fatalf("--micro command %q has phase %q, want fast or micro only", c.Name, c.Phase)
		}
	}
	if micro != len(r.MicroChecks) || lastFast < 0 || firstMicro < lastFast {
		t.Fatalf("commands must be the fast phase then every micro_check: %+v", r.Commands)
	}
	for _, c := range r.MicroChecks {
		if strings.Contains(c, "TestW457") {
			t.Fatalf("a micro_check must not run this suite (it runs the engine itself): %q", c)
		}
	}

	// Every tracked docs file is covered or excluded, never both.
	ls, err := exec.Command("git", "-C", v1Root(t), "ls-files", "docs").Output()
	if err != nil {
		t.Fatalf("git ls-files docs: %v", err)
	}
	files := strings.Fields(string(ls))
	if len(files) == 0 {
		t.Fatal("git ls-files docs listed nothing")
	}
	for _, f := range files {
		covered := false
		for _, p := range r.MicroPaths {
			if w457Match(p, f) {
				covered = true
			}
		}
		switch excl := w457IsExcluded(f); {
		case covered && excl:
			t.Errorf("%s is excluded from micro but a micro_path matches it", f)
		case !covered && !excl:
			t.Errorf("%s is neither in a micro_path nor a named exclusion: list it or exclude it with a reason", f)
		}
	}
}

// TestW457MicroRunFailsOnABrokenPinnedDoc: a micro change that breaks a
// sentence TestLaneRuleTextsContract pins in docs/concepts.md fails the
// micro evidence itself.
func TestW457MicroRunFailsOnABrokenPinnedDoc(t *testing.T) {
	root := v1Root(t)
	dir := filepath.Join(t.TempDir(), "clone")
	w25e2eGit(t, root, "clone", "-q", "--shared", root, dir)
	w25e2eGit(t, dir, "config", "user.email", "w457@e")
	w25e2eGit(t, dir, "config", "user.name", "w457")
	// The gitignored go:embed tree the Go packages need to compile.
	if err := os.CopyFS(filepath.Join(dir, "webui", "dist"), os.DirFS(filepath.Join(root, "webui", "dist"))); err != nil {
		t.Fatalf("copy webui/dist (build it first: cd webui && npm ci && npm run build): %v", err)
	}

	// The fast gates read no doc and every other bar proves them; dropping
	// them keeps the fixture off the network and the npm cache.
	var kept []string
	for _, ln := range strings.SplitAfter(w25engShippedConf(t), "\n") {
		if !strings.HasPrefix(ln, "fast_gate=") {
			kept = append(kept, ln)
		}
	}
	w25e2eWrite(t, dir, ".polyforge/verify-scope.conf", strings.Join(kept, ""))
	w25e2eGit(t, dir, "add", ".polyforge/verify-scope.conf")
	w25e2eGit(t, dir, "commit", "-q", "--allow-empty", "-m", "w457 base")
	base := w25e2eGit(t, dir, "rev-parse", "HEAD")
	w25e2eGit(t, dir, "update-ref", "refs/remotes/origin/main", base)
	w25e2eGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	const pinned = "weakening guarantees the work item does not name"
	concepts := filepath.Join(dir, "docs", "concepts.md")
	raw, err := os.ReadFile(concepts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), pinned) {
		t.Fatalf("docs/concepts.md no longer carries %q: pick another sentence a micro_check pins", pinned)
	}
	w25e2eWrite(t, dir, "docs/concepts.md", strings.Replace(string(raw), pinned, "changing things the ticket leaves alone", 1))
	w25e2eGit(t, dir, "commit", "-q", "-am", "w457 break a pinned sentence")

	jout, jerr, jcode := w25e2eRun(t, dir, nil, "--micro", "--json")
	if jcode != 0 {
		t.Fatalf("--micro --json exit %d\nstderr: %s", jcode, jerr)
	}
	checks := w457Parse(t, jout).MicroChecks

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(root, "workflows", "_shared", "scripts", "verify-scope"), "--micro", "--run")
	cmd.Dir = dir
	// The nested go test gets its own TMPDIR, removed with this test's
	// temp dirs, so what its harness leaves never lands in ours.
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	both := out.String() + errb.String()
	if runErr == nil {
		t.Fatalf("--micro --run exited 0 on a broken pinned sentence\n%s", both)
	}
	if ctx.Err() != nil {
		t.Fatalf("--micro --run did not finish within the deadline\n%s", both)
	}
	if !strings.Contains(both, "--- FAIL: TestLaneRuleTextsContract") {
		t.Fatalf("--micro --run failed, but not on the pinning contract test\n%s", both)
	}
	named := false
	for _, c := range checks {
		if strings.Contains(both, "verify-scope: reported command failed: "+c) {
			named = true
		}
	}
	if !named {
		t.Fatalf("the failure must be a micro_check (%q)\n%s", checks, both)
	}
}
