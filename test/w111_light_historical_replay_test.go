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
// land, the corpus reds. The inputs and expectations live in
// test/testdata/light-replay/cases.json, the selection algorithm nowhere.
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
	AllowedNew map[string][]w111lrAllow `json:"allowed_new"` // what this change may add, item by item with the declaration that brings it
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
	if len(d.Cases) != 5 || d.Counterexample == "" || len(d.Ac3Paths) == 0 {
		t.Fatalf("the corpus must carry five cases, a counterexample and the AC3 paths: %+v", d)
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
			// The selection stays inside old ∪ allowed_new, item by item:
			// a swap of equal count (one old name dropped, another slipped
			// in) reds here, and so does any addition no declaration names.
			measured := map[string][]string{
				"go_pkg_cmds":   nil,
				"go_run_cmds":   nil,
				"e2e_run_names": nil,
				"conf_cmds":     nil,
				"npm_suites":    nil,
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
					measured["go_run_cmds"] = append(measured["go_run_cmds"], c2.Cmd)
				case strings.Contains(c2.Cmd, "go test"):
					measured["go_pkg_cmds"] = append(measured["go_pkg_cmds"], c2.Cmd)
				}
			}
			allowed := map[string]map[string]bool{}
			for kind, items := range c.Old {
				m := allowed[kind]
				if m == nil {
					m = map[string]bool{}
					allowed[kind] = m
				}
				for _, v := range items {
					m[v] = true
				}
			}
			for kind, items := range c.AllowedNew {
				m := allowed[kind]
				if m == nil {
					m = map[string]bool{}
					allowed[kind] = m
				}
				for _, it := range items {
					m[it.Name] = true
				}
			}
			for kind, items := range measured {
				for _, v := range items {
					if !allowed[kind][v] {
						t.Fatalf("%s: the selection gained %s %q, outside the old set and the allowed additions:\nselected:\n%s\nallowed:\n%s",
							c.Id, kind, v, strings.Join(items, "\n"), w111lrAllowedList(allowed[kind]))
					}
				}
			}
			for kind, cap := range c.Cap {
				got := map[string]int{"pkg": k.pkg, "run": k.run, "conf": k.conf, "e2e_names": k.e2eNames, "suites": k.suites}[kind]
				if got > cap {
					t.Fatalf("%s: %s selected %d, over the cap %d:\n%s",
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

// RootLabel answers the case's engine root for messages.
func (c w111lrCase) RootLabel() string {
	if c.Root == "" {
		return "repository root"
	}
	return c.Root
}
