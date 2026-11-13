package test

// w429_one_lane_loader_test.go (AC1): every reader of a lane
// definition gets the same answer for the same lane, because each one is
// the one lane loader (workflow.Load) — the driver (LoadWorkflow), the
// spawn adapter (NewStepSpawnAdapter), the claim-side and `pf file
// create` read (workspace.Bundles), the model-candidate composition
// (workspace.ComposeCandidates over that read), the step id
// enumeration (workflow.LaneSet.StepIDs) and `pf workflows validate`.
// A lane the contract refuses is refused by each with the same WFC-*
// defects; a lane it accepts reads alike everywhere. And no second,
// lenient parser exists: outside the loader's package no non-test Go file
// splits a frontmatter block or decodes a lane's `steps`.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// noSpawner satisfies the spawn adapter's spawner (nothing is spawned).
type noSpawner struct{}

func (noSpawner) Spawn(context.Context, spawn.Params) (spawn.Handle, error) {
	return nil, errors.New("no spawn in this test")
}

// w429Lanes lays out a lanes root: the shipped `_shared` and lanes, a
// synthetic lane carrying candidates and wrap attrs (workflow contract 4
// refuses contract 3's `attrs` key), and a refused lane with defects in its
// workflow.yaml and in a definition.
func w429Lanes(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "workflows")
	if err := copyTree(filepath.Join(v1Root(t), "workflows"), root); err != nil {
		t.Fatal(err)
	}
	workflowtest.WriteLane(t, root, "synth", `defaults:
  model_candidates: [pi:prov/a, claude:prov/b]
steps:
  - id: draft
    model_candidates: [pi:prov/c]
    wrap_attrs: [integration]
  - { id: close }
`)
	workflowtest.WriteLaneUnchecked(t, root, "broken", `steps:
  - { id: draft, context_policy: fresh }
  - { id: check, gate: true, on_fail: { target: draft } }
  - { id: hole }
`)
	if err := os.WriteFile(filepath.Join(root, "broken", "steps", "hole.md"), []byte(workflowtest.Definition("hole", "missing_var")), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func copyTree(from, to string) error {
	return filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		out := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, raw, 0o644)
	})
}

// codesOf answers the sorted WFC codes of an error (nil error: none).
func codesOf(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	var le *workflow.LoadError
	if !errors.As(err, &le) {
		t.Fatalf("a refusal that is no *workflow.LoadError: %v", err)
	}
	return codeList(le.Errors)
}

func codeList(errs []workflow.ContractError) []string {
	var out []string
	for _, e := range errs {
		out = append(out, e.Code+"@"+filepath.Base(e.File)+":"+e.Path)
	}
	sort.Strings(out)
	return out
}

// validateCodes runs `pf --json workflows validate <dir>` in process and
// answers its exit code and its lane's defects.
func validateCodes(t *testing.T, dir string) (int, []string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	code := cli.Run([]string{"--json", "workflows", "validate", dir}, strings.NewReader(""), &stdout, &stderr)
	var rep struct {
		OK    bool `json:"ok"`
		Lanes []struct {
			Errors []workflow.ContractError `json:"errors"`
		} `json:"lanes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil || len(rep.Lanes) != 1 {
		t.Fatalf("validate %s: %v (stdout %q stderr %q)", dir, err, stdout.String(), stderr.String())
	}
	return code, codeList(rep.Lanes[0].Errors)
}

// spawnAdapterOver materializes the lane into a capsule the way a claim
// does (the lane and the shared tree beside it) and builds the spawn
// adapter over it.
func spawnAdapterOver(t *testing.T, root, name string) (*driver.StepSpawnAdapter, error) {
	t.Helper()
	capDir := t.TempDir()
	for _, tree := range []string{name, workflow.SharedDir} {
		if err := copyTree(filepath.Join(root, tree), filepath.Join(capDir, "workflows", tree)); err != nil {
			t.Fatal(err)
		}
	}
	return driver.NewStepSpawnAdapter(driver.StepSpawnAdapter{
		WI:      driver.WIInfo{ID: "wi_w429", CapsuleDir: capDir, BundleName: name},
		Spawner: noSpawner{},
	})
}

func stepIDs(wf *driver.Workflow) []string {
	var out []string
	for _, s := range wf.Steps {
		out = append(out, s.ID)
	}
	return out
}

func TestW429EveryReaderGetsTheSameLane(t *testing.T) {
	root := w429Lanes(t)
	installed, bad, err := workspace.Bundles(workflow.LaneSet{Builtin: root}, "")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := workflow.LaneSet{Builtin: root}.StepIDs()
	if err != nil {
		t.Fatal(err)
	}
	dirs, _ := workflow.LaneDirs(root)
	if len(dirs) < 6 {
		t.Fatalf("lanes %v", dirs)
	}
	for _, dir := range dirs {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			lane, errs := workflow.Load(dir)
			want := codeList(errs)
			// the driver, the spawn adapter, the claim-side read,
			// `pf workflows validate`: one verdict
			wf, derr := driver.LoadWorkflow(dir)
			ad, aerr := spawnAdapterOver(t, root, name)
			vcode, vcodes := validateCodes(t, dir)
			for reader, got := range map[string][]string{
				"driver.LoadWorkflow":   codesOf(t, derr),
				"NewStepSpawnAdapter":   codesOf(t, aerr),
				"workspace.Bundles":     codesOf(t, bad[name]),
				"pf workflows validate": vcodes,
			} {
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s answers %v, the loader %v", reader, got, want)
				}
			}
			if (vcode == 0) != (len(want) == 0) {
				t.Errorf("pf workflows validate exited %d with defects %v", vcode, want)
			}
			// the step id enumeration reads the same lane, refused or not
			// (its defect is the lane's, never a workspace yaml's)
			for _, s := range lane.Steps {
				if !ids[s.ID] {
					t.Errorf("StepIDs lacks %s", s.ID)
				}
			}
			if len(want) > 0 {
				if installed[name] != nil {
					t.Errorf("a refused lane is installed for the claim and pf file create")
				}
				return
			}
			// an accepted lane: one shape everywhere
			var laneIDs []string
			for _, s := range lane.Steps {
				laneIDs = append(laneIDs, s.ID)
			}
			if !reflect.DeepEqual(stepIDs(wf), laneIDs) || !reflect.DeepEqual(stepIDs(ad.Workflow), laneIDs) {
				t.Errorf("step ids: driver %v adapter %v lane %v", stepIDs(wf), stepIDs(ad.Workflow), laneIDs)
			}
			if wf.MaxRewinds != lane.MaxRewinds || ad.Workflow.MaxRewinds != lane.MaxRewinds {
				t.Errorf("budget: driver %d adapter %d lane %d", wf.MaxRewinds, ad.Workflow.MaxRewinds, lane.MaxRewinds)
			}
			if !reflect.DeepEqual(installed[name], lane) {
				t.Errorf("workspace.Bundles answers another lane than the loader")
			}
			if got, want := workspace.ComposeCandidates(installed[name], nil, "p", nil), workspace.ComposeCandidates(lane, nil, "p", nil); !reflect.DeepEqual(got, want) {
				t.Errorf("candidates %v, the loader's lane %v", got, want)
			}
		})
	}
	if len(bad) != 1 || bad["broken"] == nil {
		t.Fatalf("refused lanes %v, want broken only", bad)
	}
}

// No second, lenient parser: outside the loader's package, no non-test Go
// file splits a frontmatter block or decodes a lane's `steps` key.
func TestW429NoSecondLaneParser(t *testing.T) {
	root := v1Root(t)
	split := regexp.MustCompile(`\bSplitFrontmatter\(`)
	steps := regexp.MustCompile("`yaml:\"steps\"`")
	err := filepath.Walk(filepath.Join(root, "internal"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if strings.HasPrefix(filepath.ToSlash(rel), "internal/common/workflow/") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if split.Match(raw) {
			t.Errorf("%s splits a frontmatter block itself: read a lane through workflow.Load / workflow.ReadDefinition", rel)
		}
		if steps.Match(raw) {
			t.Errorf("%s decodes a `steps` key itself: read a lane through workflow.Load", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
