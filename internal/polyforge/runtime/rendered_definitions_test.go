// carries: internal/polyforge/runtime/materialize.go, workflows/_shared/build_subagents.md, workflows/develop/steps/build.md

package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// placeholderGrammar is the definition placeholder syntax ({{name}}):
// scanned by grammar, not by name, so a placeholder a definition adds
// later is caught without touching this test.
var placeholderGrammar = regexp.MustCompile(`\{\{[a-z][a-z0-9_]*\}\}`)

// A claim writes every step's rendered definition beside the snapshot
// (workflows/_rendered/<id>.md), the text a build sub-agent is told to
// read: no placeholder is left, its body is exactly the parent step's
// definition body (the same render), develop/build's carries the
// principles in full, and both pointers name that copy. Run over every
// built-in lane of this checkout.
func TestMaterializeWritesRenderedStepDefinitions(t *testing.T) {
	builtin, err := filepath.Abs(filepath.Join("..", "..", "..", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	principles, err := os.ReadFile(filepath.Join(builtin, workflow.SharedDir, "principles.md"))
	if err != nil {
		t.Fatal(err)
	}
	lanes, err := workflow.LaneDirs(builtin)
	if err != nil || len(lanes) == 0 {
		t.Fatalf("built-in lanes: %v %v", lanes, err)
	}
	sawDevelopBuild := false
	for _, src := range lanes {
		name := filepath.Base(src)
		t.Run(name, func(t *testing.T) {
			r, _, _ := newTestRuntime(t, newFakeHub(), &scriptedSpawner{})
			r.cfg.WorkflowsDir = builtin
			capDir := t.TempDir()
			if err := r.materializeBundle(&job{wi: "wi_rendered", bundleName: name, capsuleDir: capDir}); err != nil {
				t.Fatalf("materializeBundle: %v", err)
			}
			laneDir := filepath.Join(capDir, "workflows", name)
			lane, errs := workflow.Load(laneDir)
			if len(errs) > 0 || lane == nil {
				t.Fatalf("capsule lane does not load: %v", errs)
			}
			for _, s := range lane.Steps {
				rel := "workflows/_rendered/" + s.ID + ".md"
				raw, err := os.ReadFile(filepath.Join(capDir, filepath.FromSlash(rel)))
				if err != nil {
					t.Errorf("%s: rendered definition missing: %v", rel, err)
					continue
				}
				if left := placeholderGrammar.FindAllString(string(raw), -1); len(left) > 0 {
					t.Errorf("%s: unexpanded placeholders %v", rel, left)
				}
				_, body, ok := workflow.SplitFrontmatter(raw)
				if !ok {
					t.Errorf("%s: no frontmatter", rel)
					continue
				}
				def, err := workflow.ReadDefinition(laneDir, s.ID, s.Vars)
				if err != nil {
					t.Fatalf("ReadDefinition(%s): %v", s.ID, err)
				}
				if strings.TrimSpace(string(body)) != def.Body {
					t.Errorf("%s: body differs from the parent step's rendered definition", rel)
				}
				if name == "develop" && s.ID == "build" {
					sawDevelopBuild = true
					if !strings.Contains(string(raw), strings.TrimSpace(string(principles))) {
						t.Errorf("%s: principles.md not carried in full", rel)
					}
				}
			}
		})
	}
	if !sawDevelopBuild {
		t.Error("develop/build was never checked")
	}
	for _, rel := range []string{"_shared/build_subagents.md", "develop/steps/build.md"} {
		raw, err := os.ReadFile(filepath.Join(builtin, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "workflows/_rendered/") {
			t.Errorf("workflows/%s does not point the sub-agent at workflows/_rendered/", rel)
		}
	}
}
