package driver

// static_ids_test.go: the two static invariants of the workflow contract
// (§13.1 ⑥ and ⑦), in force without exception since.
//
// ⑥ No Go code of the execution plane or the CLI branches on a step id or
// a lane name: a string literal equal to a shipped lane's name or one of
// its step ids is a defect — a lane's own declarations (`exclusive`) and
// its skills' `--lane` say what a name would, a sub-definition's models
// the dispatching step's — except the literals that only coincide with an id
// (a git subcommand, a field, event, verb or directory name), each listed
// with what it is.
//
// ⑦ Renaming a lane (its directory, `name` and every reference) or a step
// defined in the lane's own directory (its id, its steps/<id>.md and every
// reference) changes no behaviour: the renamed lane loads to the same
// execution graph, step for step, its declarations included
// (the workflow package's, the daemon's and the CLI's own rename tests
// follow the renamed lanes through what they do).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// coincidences are literals equal to an id that name something else.
var coincidences = map[string]string{
	"driver/handoff.go:evidence":       "a blocker field (a blocked hand-off's)",
	"driver/supervision.go:evidence":   "a supervision fact field",
	"daemon/ensure.go:release":         "the release tree's directory",
	"runtime/recover.go:release":       "a park kind (a job's release)",
	"runtime/runtime.go:close":         "a lifecycle phase",
	"cli/doctor_layout.go:release":     "the release tree's directory",
	"cli/verbs_workflow.go:release":    "the release tree's directory",
	"cli/verbs_step.go:evidence":       "the `pf step handoff --evidence` flag and its wire key",
	"workspace/workspace.go:integrate": "the removed .polyforge.yaml top-level section integrate:, refused with its rewrite hint",
}

// shippedIDs answers every shipped lane's name and step ids.
func shippedIDs(t *testing.T) map[string]bool {
	t.Helper()
	dirs, err := workflow.LaneDirs(shippedWorkflowsDir)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, d := range dirs {
		ids[filepath.Base(d)] = true
		wf, err := LoadWorkflow(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range wf.Steps {
			ids[s.ID] = true
		}
	}
	return ids
}

func TestNoStepIDOrLaneNameLiterals(t *testing.T) {
	ids := shippedIDs(t)
	root := filepath.Join("..")
	var hits []string
	seen := map[string]bool{}
	for _, pkg := range []string{"driver", "daemon", "runtime", "workspace", "cli"} {
		files, _ := filepath.Glob(filepath.Join(root, pkg, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			af, err := parser.ParseFile(fset, f, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			imports := map[*ast.BasicLit]bool{}
			for _, im := range af.Imports {
				imports[im.Path] = true
			}
			ast.Inspect(af, func(n ast.Node) bool {
				bl, ok := n.(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING || imports[bl] {
					return true
				}
				v, err := strconv.Unquote(bl.Value)
				if err != nil || !ids[v] {
					return true
				}
				key := pkg + "/" + filepath.Base(f) + ":" + v
				seen[key] = true
				if coincidences[key] == "" {
					hits = append(hits, key+" (line "+strconv.Itoa(fset.Position(bl.Pos()).Line)+")")
				}
				return true
			})
		}
	}
	sort.Strings(hits)
	if len(hits) > 0 {
		t.Errorf("a step id or lane name as a Go literal (workflow contract ⑥): declare it in the lane instead:\n%s", strings.Join(hits, "\n"))
	}
	// the list names only what is there: a stale entry hides a new hit
	for key := range coincidences {
		if !seen[key] {
			t.Errorf("listed literal %s no longer occurs: drop it", key)
		}
	}
}

// laneShape is a loaded lane's execution graph with its own names
// abstracted: a step by position, its references by position.
func laneShape(t *testing.T, wf *Workflow) []string {
	t.Helper()
	pos := map[string]string{}
	for i, s := range wf.Steps {
		pos[s.ID] = "#" + strconv.Itoa(i)
	}
	ref := func(id string) string {
		if p, ok := pos[id]; ok {
			return p
		}
		return id
	}
	var out []string
	for _, s := range wf.Steps {
		line := strings.Join([]string{strconv.FormatBool(s.Gate), strconv.FormatBool(s.Waivable), strings.Join(s.Hub, "|"), strings.Join(s.WrapAttrs, "|")}, ";")
		if s.OnFail != nil {
			line += ";on_fail=" + ref(s.OnFail.Target)
		}
		out = append(out, line)
	}
	out = append(out, "max_rewinds="+strconv.Itoa(wf.MaxRewinds))
	return out
}

// TestRenameInvariant (⑦): every shipped lane, renamed together with one
// step its own directory defines, loads to the same execution graph.
func TestRenameInvariant(t *testing.T) {
	for lane, step := range map[string]string{"develop": "build", "micro": "fix", "release": "verify", "integrate": "close"} {
		t.Run(lane, func(t *testing.T) {
			orig, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, lane))
			if err != nil {
				t.Fatal(err)
			}
			newLane, newStep := lane+"-renamed", step+"-renamed"
			dst := useLaneTree(t, nil)
			if err := os.Rename(filepath.Join(dst, lane), filepath.Join(dst, newLane)); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(dst, newLane)
			if _, err := os.Stat(filepath.Join(dir, "steps", step+".md")); err == nil {
				if err := os.Rename(filepath.Join(dir, "steps", step+".md"), filepath.Join(dir, "steps", newStep+".md")); err != nil {
					t.Fatal(err)
				}
				def := filepath.Join(dir, "steps", newStep+".md")
				raw, _ := os.ReadFile(def)
				raw = regexp.MustCompile(`(?m)^name: `+regexp.QuoteMeta(step)+`$`).ReplaceAll(raw, []byte("name: "+newStep))
				if err := os.WriteFile(def, raw, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// a skill of the same name is the step's own file too (contract 2):
			// renamed with it, its frontmatter name following
			laneSkill := false
			if _, err := os.Stat(filepath.Join(dir, workflow.SkillsDir, step+".md")); err == nil {
				laneSkill = true
				sk := filepath.Join(dir, workflow.SkillsDir, newStep+".md")
				if err := os.Rename(filepath.Join(dir, workflow.SkillsDir, step+".md"), sk); err != nil {
					t.Fatal(err)
				}
				raw, _ := os.ReadFile(sk)
				raw = regexp.MustCompile(`(?m)^name: `+regexp.QuoteMeta(step)+`$`).ReplaceAll(raw, []byte("name: "+newStep))
				if err := os.WriteFile(sk, raw, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			p := filepath.Join(dir, workflow.WorkflowFile)
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			head := string(raw)
			head = strings.Replace(head, "\nname: "+lane+"\n", "\nname: "+newLane+"\n", 1)
			// every reference of the step: the id, on_fail/sweep/build_step,
			// step-refs and inputs
			// (a whole token: never inside build-facts or a description)
			idRe := regexp.MustCompile(`([\s{\[,:])` + regexp.QuoteMeta(step) + `([\s}\],.])`)
			for i := 0; i < 2; i++ { // adjacent occurrences share a delimiter
				head = idRe.ReplaceAllString(head, "${1}"+newStep+"${2}")
			}
			// a shared skill's name is no step reference (develop's build
			// follows _shared/skills/build.md, workflow contract v2 PR 12)
			if !laneSkill {
				head = strings.ReplaceAll(head, "skill: "+newStep+"\n", "skill: "+step+"\n")
			}
			if err := os.WriteFile(p, []byte(head), 0o644); err != nil {
				t.Fatal(err)
			}
			renamed, err := LoadWorkflow(dir)
			if err != nil {
				t.Fatalf("the renamed lane does not load: %v", err)
			}
			if renamed.step(newStep) == nil {
				t.Fatalf("the renamed lane has no step %q", newStep)
			}
			if a, b := laneShape(t, orig), laneShape(t, renamed); !reflect.DeepEqual(a, b) {
				t.Fatalf("renaming %s/%s changed the execution graph:\n%s\nvs\n%s", lane, step, strings.Join(a, "\n"), strings.Join(b, "\n"))
			}
		})
	}
}
