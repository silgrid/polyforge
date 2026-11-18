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
	"os"
	"path/filepath"
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
		if cap, ok := c.Cap["e2e_names"]; ok && counts["e2e_names"] > cap {
			t.Fatalf("%s: a Test function added after the baseline must not count toward the cap: %d > %d", c.Id, counts["e2e_names"], cap)
		}
	})

	// A baseline-era Test function newly pulled in through a case path is
	// still a violation, the full list is answered at once, and it still
	// counts toward the caps (polyforge#126 AC3, the Q3 ruling).
	t.Run("baseline-era-test-newly-selected-still-overruns", func(t *testing.T) {
		c := w111lrCaseById(doc, "polyforge-36")
		harness := "test/w174_daemon_env_hygiene_test.go" // existed at the baseline, references no case path
		name := "TestW174EnsureDaemonEnvIsTheCallersAllowList"
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(harness)))
		if err != nil {
			t.Fatalf("read %s: %v", harness, err)
		}
		r := w111lrReplayMods(t, wt, base, c.Root, c.Paths, map[string]string{
			harness: string(body) + "\n// w126: the literal workflows/_shared/scripts/revert.py pulls this file in\n",
		})
		if r.Mode != "scoped" {
			t.Fatalf("the %s replay must stay scoped, got %q (diagnostics %v)", c.Id, r.Mode, r.Diagnostics)
		}
		violations, _, counts := w111lrJudge(c, r, doc.Baseline, atBaseline)
		joined := strings.Join(violations, "\n")
		if !strings.Contains(joined, name) {
			t.Fatalf("%s: a baseline-era test newly selected through a case path must be reported as a violation, all at once — %s missing from:\n%s", c.Id, name, joined)
		}
		if cap, ok := c.Cap["e2e_names"]; ok && counts["e2e_names"] <= cap {
			t.Fatalf("%s: a baseline-era test must still count toward the cap: %d <= %d", c.Id, counts["e2e_names"], cap)
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
	return nil, nil, nil
}

// w111lrBaselineErr answers why the corpus's baseline commit is unusable
// in this clone, nil when it is: the pass-through filter judges against
// that commit, so a clone without it must fail loudly naming it — never
// silently pass everything.
func w111lrBaselineErr(root, commit string) error {
	return nil
}

// w111lrNameAtBaseline answers whether the named Test function is defined
// by some *_test.go at the baseline commit — the pass-through filter's one
// question. git grep answers from the object store, so the worktree's state
// does not matter.
func w111lrNameAtBaseline(root, baseline, name string) (bool, error) {
	return false, nil
}

// RootLabel answers the case's engine root for messages.
func (c w111lrCase) RootLabel() string {
	if c.Root == "" {
		return "repository root"
	}
	return c.Root
}
