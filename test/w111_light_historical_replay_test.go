// carries: workflows/_shared/scripts/*, .polyforge/verify-scope.conf, pi-web/.polyforge/verify-scope.conf, pi-web/e2e/w367-default-cell-pinned.mjs, pi-web/e2e/claude-composer-272.mjs, test/w348_cell_e2e_acceptance_test.go, test/w113_workspace_mapping_acceptance_test.go, test/testdata/light-replay/*

package test

// w111_light_historical_replay_test.go (polyforge#111 u3): the historical
// miss-replay corpus. Five real changes reached the develop lane's light
// verification green and broke only at the integration full gate
// (polyforge#87's five, each with its own fix ticket); each is replayed as
// its actual changed-path list — a synthetic commit on a clean worktree of
// HEAD, the current tree's state and the current selection rules — through
// the release-shipped engine's real --light report, at the root the case's
// target lives in (the repository root or the pi-web nested root). The
// corpus asserts the target that was red at the full gate is selected, and
// that the selection stays inside the per-case caps the corpus records
// (the old set's measured counts plus the additions this change allows):
// a selection rule that quietly regresses — or quietly balloons — cannot
// land, the corpus reds. The judgment runs through the baseline commit
// cases.json records (polyforge#126): a Test function the baseline commit
// does not define is named in the log as passed — the corpus judges the
// tests the baseline already defines — and a baseline commit missing from
// the clone fails loudly, never silently passes everything. The inputs and
// expectations live in test/testdata/light-replay/cases.json, the selection
// algorithm nowhere.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestLightHistoricalReplay$' -count=1 -v

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// w111lrTarget is what a case asserts is selected.
type w111lrTarget struct {
	Kind  string `json:"kind"`  // command | harness_name | suite
	Value string `json:"value"` // the scoped command verbatim, a Test name in E2E_RUN, or a suite path
}

// w111lrCase is one historical miss (or already-covered counterexample)
// with its input paths and the caps of its selection.
type w111lrCase struct {
	Id         string                   `json:"id"`
	Status     string                   `json:"status"` // miss | covered
	Root       string                   `json:"root"`   // "" = the repository root, "pi-web" = the nested root
	Paths      []string                 `json:"paths"`
	Target     w111lrTarget             `json:"target"`
	Note       string                   `json:"note"`
	Old        map[string][]string      `json:"old"`         // the old rules' measured selection, item by item (the baseline replay)
	AllowedNew map[string][]w111lrAllow `json:"allowed_new"` // what this change may add, item by item with the declaration that brings it — judged, for Test functions, against the cases.json baseline commit
	Cap        map[string]int           `json:"cap"`         // the numeric upper bounds the selection must stay inside
}

// w111lrAllow is one allowed addition: the item and the declaration that
// brings it (which carries header or conf line matched which changed path).
type w111lrAllow struct {
	Name string `json:"name"`
	Via  string `json:"via"`
}

// w111lrDoc is the corpus's static file.
type w111lrDoc struct {
	Counterexample string       `json:"counterexample"`
	Baseline       string       `json:"baseline"` // the commit the pass-through filter judges against
	Ac3Paths       []string     `json:"ac3_paths"`
	Cases          []w111lrCase `json:"cases"`
}

func w111lrLoad(t *testing.T) w111lrDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "light-replay", "cases.json"))
	if err != nil {
		t.Fatalf("the corpus must ship: %v", err)
	}
	var d w111lrDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("the corpus must parse: %v", err)
	}
	if len(d.Cases) != 5 || d.Counterexample == "" || len(d.Ac3Paths) == 0 || d.Baseline == "" {
		t.Fatalf("the corpus must carry five cases, a counterexample, the AC3 paths and its baseline commit: %d cases, counterexample %q, %d ac3 paths, baseline %q", len(d.Cases), d.Counterexample, len(d.Ac3Paths), d.Baseline)
	}
	return d
}

// w111lrCounts is one report's selection by kind.
type w111lrCounts struct {
	pkg, run, conf, e2eNames, suites int
	cmds                             []string // the scoped command list, in order
	e2eCmd, suitesCmd                string
}

func (k w111lrCounts) has(kind, value string) bool {
	switch kind {
	case "command":
		for _, c := range k.cmds {
			if c == value {
				return true
			}
		}
	case "harness_name":
		return k.e2eCmd != "" && strings.Contains(k.e2eCmd, value)
	case "suite":
		return k.suitesCmd != "" && strings.Contains(" "+k.suitesCmd+" ", " "+value+" ")
	}
	return false
}

func w111lrMeasure(t *testing.T, r w111scReport) w111lrCounts {
	t.Helper()
	var k w111lrCounts
	for _, c := range r.scoped() {
		k.cmds = append(k.cmds, c.Cmd)
		if c.Source == "conf" {
			k.conf++
		}
		switch {
		case strings.HasPrefix(c.Cmd, "E2E_RUN"):
			k.e2eCmd = c.Cmd
			k.e2eNames = strings.Count(c.Cmd, "|") + 1
		case strings.HasPrefix(c.Cmd, "npm run test:e2e:all"):
			k.suitesCmd = c.Cmd
			k.suites = len(strings.Split(strings.SplitN(c.Cmd, "-- ", 2)[1], " "))
		case strings.Contains(c.Cmd, "go test"):
			if strings.Contains(c.Cmd, " -run ") {
				k.run++
			} else {
				k.pkg++
			}
		}
	}
	return k
}

// w111lrReplay commits the paths as one synthetic change on a fresh reset
// of the worktree and answers the engine's --light report at root.
func w111lrReplay(t *testing.T, wt, base, root string, paths []string) w111scReport {
	t.Helper()
	return w111lrReplayMods(t, wt, base, root, paths, nil)
}

// w111lrReplayMods commits the paths as one synthetic change beside the
// given full file bodies (the baseline subtests' harness-file edits) and
// answers the engine's --light report at root.
func w111lrReplayMods(t *testing.T, wt, base, root string, paths []string, mods map[string]string) w111scReport {
	t.Helper()
	w25e2eGit(t, wt, "reset", "--hard", base)
	w25e2eGit(t, wt, "clean", "-qfd")
	for _, p := range paths {
		full := filepath.Join(wt, filepath.FromSlash(p))
		if _, err := os.Stat(full); err == nil {
			f, err := os.OpenFile(full, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatalf("append %s: %v", p, err)
			}
			if _, err := f.WriteString("\n"); err != nil {
				t.Fatalf("append %s: %v", p, err)
			}
			f.Close()
		} else {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("w111 replay\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for rel, body := range mods {
		w25e2eWrite(t, wt, rel, body)
	}
	w25e2eCommit(t, wt, "w111 replay")
	dir := wt
	if root != "" {
		dir = filepath.Join(wt, filepath.FromSlash(root))
	}
	return w111scRun(t, dir, "--light", "--base", base)
}

// TestLightHistoricalReplay is the corpus's entry: every case selects its
// full-gate-red target inside its caps, the unrelated counterexample adds
// nothing (alone or appended to any case), and a change to the selector,
// the conf, a carries declaration or the corpus itself selects this very
// test — the corpus cannot miss its own selection.
func TestLightHistoricalReplay(t *testing.T) {
	doc := w111lrLoad(t)
	root := w25e2eRoot(t)
	base := w25e2eGit(t, root, "rev-parse", "HEAD")
	wt, cleanup := w25ioWorktree(t, root, base)
	defer cleanup()
	if err := w111lrBaselineErr(root, doc.Baseline); err != nil {
		t.Fatal(err)
	}
	// The pass-through filter's one question, cached per name: is this Test
	// function defined by some *_test.go at the baseline commit?
	baselineCache := map[string]bool{}
	atBaseline := func(name string) bool {
		if ok, seen := baselineCache[name]; seen {
			return ok
		}
		ok, err := w111lrNameAtBaseline(root, doc.Baseline, name)
		if err != nil {
			t.Fatalf("the baseline-commit lookup for %s failed: %v", name, err)
		}
		baselineCache[name] = ok
		return ok
	}

	caseCounts := map[string]map[string]int{}
	for _, c := range doc.Cases {
		c := c
		t.Run(c.Id, func(t *testing.T) {
			r := w111lrReplay(t, wt, base, c.Root, c.Paths)
			if r.Mode != "scoped" {
				t.Fatalf("%s: the %s report must stay scoped, got %q (diagnostics %v)", c.Id, c.RootLabel(), r.Mode, r.Diagnostics)
			}
			k := w111lrMeasure(t, r)
			if !k.has(c.Target.Kind, c.Target.Value) {
				t.Fatalf("%s (%s): the full-gate-red target is not selected:\n%s", c.Id, c.Status, strings.Join(k.cmds, "\n"))
			}
			// The selection stays inside old ∪ allowed_new, item by item,
			// through the baseline filter (polyforge#126): a Test function
			// the baseline commit does not define is recorded as passed and
			// named in the log, a baseline-era test newly selected is a
			// violation, and every violation is reported — none is swallowed
			// by a first-stop. The caps judge the baseline-carried items only.
			violations, passed, counts := w111lrJudge(c, r, doc.Baseline, atBaseline)
			for _, p := range passed {
				t.Logf("%s: %s", c.Id, p)
			}
			for _, v := range violations {
				t.Errorf("%s", v)
			}
			for kind, cap := range c.Cap {
				if got := counts[kind]; got > cap {
					t.Errorf("%s: %s selected %d baseline-carried items, over the cap %d:\n%s",
						c.Id, kind, got, cap, strings.Join(k.cmds, "\n"))
				}
			}
			caseCounts[c.Id] = counts
			// The unrelated counterexample adds no selection: appended to
			// this case's input it leaves the scoped commands byte-identical.
			with := w111lrReplay(t, wt, base, c.Root, append(append([]string{}, c.Paths...), doc.Counterexample))
			k2 := w111lrMeasure(t, with)
			if strings.Join(k2.cmds, "\n") != strings.Join(k.cmds, "\n") {
				t.Fatalf("%s: the unrelated path %s changes the selection:\n%s\n--- with it ---\n%s",
					c.Id, doc.Counterexample, strings.Join(k.cmds, "\n"), strings.Join(k2.cmds, "\n"))
			}
		})
	}

	t.Run("counterexample-alone", func(t *testing.T) {
		r := w111lrReplay(t, wt, base, "", []string{doc.Counterexample})
		if r.Mode != "scoped" {
			t.Fatalf("the counterexample must stay scoped, got %q (diagnostics %v)", r.Mode, r.Diagnostics)
		}
		if k := w111lrMeasure(t, r); len(k.cmds) != 0 {
			t.Fatalf("the unrelated path %s must select nothing:\n%s", doc.Counterexample, strings.Join(k.cmds, "\n"))
		}
	})

	t.Run("the-corpus-selects-itself", func(t *testing.T) {
		for _, p := range doc.Ac3Paths {
			r := w111lrReplay(t, wt, base, "", []string{p})
			k := w111lrMeasure(t, r)
			if !k.has("harness_name", "TestLightHistoricalReplay") {
				t.Fatalf("a change to %s must select this corpus through the harness carries declaration:\n%s", p, strings.Join(k.cmds, "\n"))
			}
		}
	})

	// A Test function added after the baseline commit and selected through
	// a case path cannot red the case: the judge records it as passed, the
	// log names it, and the caps do not count it (polyforge#126 AC2).
	t.Run("baseline-passes-a-test-added-after-it", func(t *testing.T) {
		c := w111lrCaseById(doc, "polyforge-36")
		harness := "test/w404_integrate_e2e_test.go" // literally references every case-36 path
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(harness)))
		if err != nil {
			t.Fatalf("read %s: %v", harness, err)
		}
		added := "TestW126AddedAfterTheBaselinePasses"
		r := w111lrReplayMods(t, wt, base, c.Root, c.Paths, map[string]string{
			harness: string(body) + "\nfunc " + added + "(t *testing.T) {}\n",
		})
		if r.Mode != "scoped" {
			t.Fatalf("the %s replay must stay scoped, got %q (diagnostics %v)", c.Id, r.Mode, r.Diagnostics)
		}
		violations, passed, counts := w111lrJudge(c, r, doc.Baseline, atBaseline)
		for _, p := range passed {
			t.Logf("%s: %s", c.Id, p)
		}
		for _, v := range violations {
			t.Errorf("%s", v)
		}
		if !strings.Contains(strings.Join(passed, "\n"), added) {
			t.Fatalf("%s: the pass-through record must name %s:\npassed:\n%s", c.Id, added, strings.Join(passed, "\n"))
		}
		for kind, cap := range c.Cap {
			if got := counts[kind]; got > cap {
				t.Fatalf("%s: a Test function added after the baseline must not red any cap: %s %d > %d", c.Id, kind, got, cap)
			}
		}
	})

	// Baseline-era Test functions newly pulled in through a case path are
	// still violations — two injections, so a judge that stops at the
	// first violation cannot pass — the full list names them all at once,
	// and both still count toward the caps (polyforge#126 AC3, the Q3/Q4
	// rulings; review r1 #2).
	t.Run("baseline-era-test-newly-selected-still-overruns", func(t *testing.T) {
		c := w111lrCaseById(doc, "polyforge-36")
		pull := "// w126: the literal workflows/_shared/scripts/revert.py pulls this file in\n"
		mods := map[string]string{}
		for _, harness := range []string{
			"test/w174_daemon_env_hygiene_test.go",   // existed at the baseline, references no case path
			"test/w218_doctor_prerequisites_test.go", // the same, a second injection: a first-stop judge fails here
		} {
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(harness)))
			if err != nil {
				t.Fatalf("read %s: %v", harness, err)
			}
			mods[harness] = string(body) + "\n" + pull
		}
		r := w111lrReplayMods(t, wt, base, c.Root, c.Paths, mods)
		if r.Mode != "scoped" {
			t.Fatalf("the %s replay must stay scoped, got %q (diagnostics %v)", c.Id, r.Mode, r.Diagnostics)
		}
		violations, _, counts := w111lrJudge(c, r, doc.Baseline, atBaseline)
		joined := strings.Join(violations, "\n")
		for _, name := range []string{"TestW174EnsureDaemonEnvIsTheCallersAllowList", "TestW218DoctorPrerequisitesRowWarns"} {
			if !strings.Contains(joined, name) {
				t.Fatalf("%s: every baseline-era test newly selected through a case path must be reported as a violation, all at once — %s missing from:\n%s", c.Id, name, joined)
			}
		}
		untouched := caseCounts[c.Id]["e2e_names"]
		if got := counts["e2e_names"]; got != untouched+2 {
			t.Fatalf("%s: both baseline-era tests must still count toward the cap: %d, want the untouched replay's %d + 2", c.Id, got, untouched)
		}
	})

	// A baseline commit the clone does not carry fails loudly, naming it —
	// a shallow checkout can never silently pass everything (AC4).
	t.Run("baseline-missing-fails-loudly", func(t *testing.T) {
		if err := w111lrBaselineErr(root, ""); err == nil || !strings.Contains(err.Error(), "baseline") {
			t.Fatalf("an empty baseline commit must fail loudly, got %v", err)
		}
		bogus := strings.Repeat("deadbeef", 5)
		if err := w111lrBaselineErr(root, bogus); err == nil || !strings.Contains(err.Error(), bogus) {
			t.Fatalf("a baseline commit missing from the clone must fail loudly naming %s, got %v", bogus, err)
		}
	})

	// The judge's own accounting on a constructed report, no replay: the
	// conf and suites caps' inputs are counted whole (they are never
	// pass-throughs), and only a well-shaped Test name passes — a package
	// path, a command fragment or an unparseable shape is a violation,
	// never a pass-through (review r1 #1/#3).
	t.Run("judge-counts-what-the-caps-judge", func(t *testing.T) {
		c := w111lrCaseById(doc, "polyforge-36")
		runTrailing := "GOWORK=off go test -count=1 -run '^(TestW126NeverAddedAnywhere)$' ./internal/polyforge/stepscripts E2E_EXTRA=1"
		runUnparsed := "GOWORK=off go test -count=1 -run '^(TestW126NeverAddedAnywhere)$' internal/polyforge/stepscripts"
		e2eTrailing := "E2E_RUN='^(TestW126NeverAddedAnywhere)$' sh test/e2e-parallel.sh --extra"
		rep := w111scReport{Commands: []w111scCommand{
			{Name: "conf-one", Phase: "scoped", Source: "conf", Cmd: "echo CONF-ONE"},
			{Name: "conf-two", Phase: "scoped", Source: "conf", Cmd: "echo CONF-TWO"},
			{Name: "suites", Phase: "scoped", Source: "default", Cmd: "npm run test:e2e:all -- e2e/a.mjs e2e/b.mjs e2e/c.mjs"},
			{Name: "run-new", Phase: "scoped", Source: "default", Cmd: "GOWORK=off go test -count=1 -run '^(TestW126NeverAddedAnywhere)$' ./internal/polyforge/stepscripts"},
			{Name: "run-trailing", Phase: "scoped", Source: "default", Cmd: runTrailing},
			{Name: "run-unparsed", Phase: "scoped", Source: "default", Cmd: runUnparsed},
			{Name: "e2e-new", Phase: "scoped", Source: "default", Cmd: "E2E_RUN='^(TestW126NeverAddedAnywhere|not-an-identifier)$' sh test/e2e-parallel.sh"},
			{Name: "e2e-trailing", Phase: "scoped", Source: "default", Cmd: e2eTrailing},
		}}
		// Every pin reports, so one run names each defect the tree carries
		// (review r1 #1 and #3 alike), none hidden behind the first.
		violations, passed, counts := w111lrJudge(c, rep, doc.Baseline, atBaseline)
		if counts["conf"] != 2 || counts["suites"] != 3 {
			t.Errorf("the conf and suites caps must count every conf command and suite item: conf %d, suites %d", counts["conf"], counts["suites"])
		}
		passedJoined := strings.Join(passed, "\n")
		if !strings.Contains(passedJoined, "TestW126NeverAddedAnywhere") {
			t.Errorf("a Test function added after the baseline must pass, named:\n%s", passedJoined)
		}
		violationsJoined := strings.Join(violations, "\n")
		for _, bad := range []string{runTrailing, runUnparsed, e2eTrailing, "not-an-identifier"} {
			if strings.Contains(passedJoined, bad) {
				t.Errorf("a malformed or non-identifier item must never be a pass-through: %q in:\n%s", bad, passedJoined)
			}
			if !strings.Contains(violationsJoined, bad) {
				t.Errorf("a malformed or non-identifier item must be a violation: %q missing from:\n%s", bad, violationsJoined)
			}
		}
	})
}

// w111lrCaseById answers the corpus's case with the id — the corpus carries
// exactly the five cases the loader validated, so a miss is impossible.
func w111lrCaseById(doc w111lrDoc, id string) w111lrCase {
	for _, c := range doc.Cases {
		if c.Id == id {
			return c
		}
	}
	panic("the corpus must carry the case " + id)
}

// w111lrAllowedList answers the allowed items, sorted, one per line.
func w111lrAllowedList(m map[string]bool) string {
	var items []string
	for v := range m {
		items = append(items, v)
	}
	sort.Strings(items)
	return strings.Join(items, "\n")
}

// w111lrJudge compares one replayed selection against the case's recorded
// old and allowed_new sets, through the corpus's baseline-commit filter
// (polyforge#126): an item outside the sets that is a Test function the
// baseline commit does not define is answered as a pass-through record —
// the corpus judges only the tests the baseline already defines — while
// every other item outside the sets is a violation, and the answer carries
// them all at once, none swallowed by a first-stop. The counts answer, per
// cap kind, how many baseline-carried items the selection carries: the
// numeric caps judge only those.
func w111lrJudge(c w111lrCase, r w111scReport, baseline string, atBaseline func(name string) bool) (violations, passed []string, counts map[string]int) {
	measured := map[string][]string{
		"go_run_pairs":  nil,
		"go_pkg_dirs":   nil,
		"e2e_run_names": nil,
		"conf_cmds":     nil,
		"npm_suites":    nil,
	}
	runNames := map[string][]string{} // one entry per run command: its package's selected names
	counts = map[string]int{
		"conf":   0,
		"suites": 0,
	}
	for _, c2 := range r.scoped() {
		switch {
		case strings.HasPrefix(c2.Cmd, "E2E_RUN"):
			measured["e2e_run_names"] = append(measured["e2e_run_names"], strings.Split(strings.TrimSuffix(strings.TrimPrefix(c2.Cmd, "E2E_RUN='^("), ")$' sh test/e2e-parallel.sh"), "|")...)
		case strings.HasPrefix(c2.Cmd, "npm run test:e2e:all"):
			measured["npm_suites"] = append(measured["npm_suites"], strings.Split(strings.SplitN(c2.Cmd, "-- ", 2)[1], " ")...)
		case c2.Source == "conf":
			measured["conf_cmds"] = append(measured["conf_cmds"], c2.Cmd)
		case strings.Contains(c2.Cmd, "go test") && strings.Contains(c2.Cmd, " -run "):
			for _, pair := range w111lrRunPairs(c2.Cmd) {
				measured["go_run_pairs"] = append(measured["go_run_pairs"], pair)
				i := strings.LastIndex(pair, " ")
				runNames[pair[:i]] = append(runNames[pair[:i]], pair[i+1:])
			}
		case strings.Contains(c2.Cmd, "go test"):
			measured["go_pkg_dirs"] = append(measured["go_pkg_dirs"], w111lrPkgDirs(c2.Cmd)...)
			counts["pkg"]++ // the cap counts package commands, as #111 measured them
		}
	}
	allowed := map[string]map[string]bool{
		"go_run_pairs": {}, "go_pkg_dirs": {}, "e2e_run_names": {}, "conf_cmds": {}, "npm_suites": {},
	}
	recorded := func(kind string, items []string, split func(string) []string) {
		for _, v := range items {
			for _, item := range split(v) {
				allowed[kind][item] = true
			}
		}
	}
	for kind, items := range c.Old {
		switch kind {
		case "go_run_cmds":
			recorded("go_run_pairs", items, w111lrRunPairs)
		case "go_pkg_cmds":
			recorded("go_pkg_dirs", items, w111lrPkgDirs)
		default:
			recorded(kind, items, func(s string) []string { return []string{s} })
		}
	}
	for kind, items := range c.AllowedNew {
		names := make([]string, len(items))
		for i, it := range items {
			names[i] = it.Name
		}
		switch kind {
		case "go_run_cmds":
			recorded("go_run_pairs", names, w111lrRunPairs)
		case "go_pkg_cmds":
			recorded("go_pkg_dirs", names, w111lrPkgDirs)
		default:
			recorded(kind, names, func(s string) []string { return []string{s} })
		}
	}
	for kind, items := range measured {
		for _, v := range items {
			if allowed[kind][v] {
				continue
			}
			// Only a Test function added after the baseline commit passes;
			// a package directory, a conf command and a suite are not Test
			// functions and stay judged (a growth there reds as before).
			var name string
			switch kind {
			case "go_run_pairs":
				name = v[strings.LastIndex(v, " ")+1:]
			case "e2e_run_names":
				name = v
			}
			if name != "" && !atBaseline(name) {
				passed = append(passed, fmt.Sprintf("%s %q — a Test function added after the corpus's baseline commit %s", kind, v, baseline))
				continue
			}
			violations = append(violations, fmt.Sprintf("%s: the selection gained %s %q, outside the old set and the allowed additions:\nselected:\n%s\nallowed:\n%s",
				c.Id, kind, v, strings.Join(measured[kind], "\n"), w111lrAllowedList(allowed[kind])))
		}
	}
	sort.Strings(violations)
	sort.Strings(passed)
	for _, n := range measured["e2e_run_names"] {
		if atBaseline(n) {
			counts["e2e_names"]++
		}
	}
	for _, names := range runNames {
		for _, n := range names {
			if atBaseline(n) {
				counts["run"]++
				break
			}
		}
	}
	return violations, passed, counts
}

// w111lrRunRe parses the engine's scoped run command: the -run name list
// and the package it selects.
var w111lrRunRe = regexp.MustCompile(`-run '\^\((.*)\)\$' (\.[^']*)$`)

// w111lrRunPairs splits one run command into its items, one "pkg name" per
// selected test. Both sides of the comparison parse through this one
// function, so a shape the engine changes stays symmetric; a command the
// expression does not match stays one whole item.
func w111lrRunPairs(cmd string) []string {
	m := w111lrRunRe.FindStringSubmatch(cmd)
	if m == nil {
		return []string{cmd}
	}
	out := make([]string, 0, len(strings.Split(m[1], "|")))
	for _, n := range strings.Split(m[1], "|") {
		out = append(out, m[2]+" "+n)
	}
	return out
}

// w111lrPkgDirs splits one package command into its directory items — the
// package closure is judged per directory, so a package that joins or leaves
// the closure is one item, not a whole new command.
func w111lrPkgDirs(cmd string) []string {
	var out []string
	for _, f := range strings.Fields(cmd) {
		if strings.HasPrefix(f, ".") {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return []string{cmd}
	}
	return out
}

// w111lrBaselineErr answers why the corpus's baseline commit is unusable
// in this clone, nil when it is: the pass-through filter judges against
// that commit, so a clone without it must fail loudly naming it — never
// silently pass everything.
func w111lrBaselineErr(root, commit string) error {
	if commit == "" {
		return fmt.Errorf("the corpus's cases.json must record its baseline commit")
	}
	c := exec.Command("git", "cat-file", "-e", commit+"^{commit}")
	c.Dir = root
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("the corpus's baseline commit %s is not in this clone — the replay corpus judges against it, fetch the full history (git cat-file says: %v)\n%s", commit, err, out)
	}
	return nil
}

// w111lrNameAtBaseline answers whether the named Test function is defined
// by some *_test.go at the baseline commit — the pass-through filter's one
// question. git grep answers from the object store, so the worktree's state
// does not matter.
func w111lrNameAtBaseline(root, baseline, name string) (bool, error) {
	c := exec.Command("git", "grep", "-F", "-e", "func "+name+"(", baseline, "--", "*_test.go")
	c.Dir = root
	_, err := c.Output()
	if err == nil {
		return true, nil
	}
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 1 { // 1 is git grep's plain "no match"
		return false, err
	}
	return false, nil
}

// RootLabel answers the case's engine root for messages.
func (c w111lrCase) RootLabel() string {
	if c.Root == "" {
		return "repository root"
	}
	return c.Root
}
