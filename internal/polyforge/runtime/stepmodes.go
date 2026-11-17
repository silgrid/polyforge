package runtime

// stepmodes.go: the work item's step modes, fixed in the capsule when the
// work item is first claimed (capsule.StepModesJSON): each step the
// capsule's lane snapshot declares human_mergeable that the project's
// workspace yaml declares `by: human` (workspace.HumanMergeSteps — the
// develop lane's merge done by a person, polyforge#36). The file is
// written only when the capsule does not carry one yet — a re-claim or a
// resume clones the capsule chain, which carries the first claim's file,
// and keeps it as it is, so a declaration changed while the work item
// runs never reaches it (exactly the skipped-step set's rule,
// skipsteps.go). The steps' skills and merge.py read the file, never the
// yaml. A lane the yaml's by cannot honor — a step it does not carry, or
// one it does not declare human_mergeable (micro accepts none) — refuses
// the claim with the judgment's reason: the mode is the lane's to accept,
// never silently ignored. A by the yaml declares for a step the lane does
// not carry rides along unchanged — a lane's step may read another lane's
// step mode (the batch repair's gate reads the merge mode to switch the
// route off for a project whose PRs await a person), and the claim never
// refuses over one.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// writeStepModes writes the capsule's step modes unless the capsule
// already carries one. A capsule without a lane snapshot gets none (there
// is nothing to run). The workspace yaml is read through the pre-claim's
// strict load (LoadWorkspace): a yaml that stopped loading since the
// pre-claim check refuses the materialize; a by it declares that this
// lane cannot honor refuses it naming the step.
func (r *Runtime) writeStepModes(j *job) error {
	path := filepath.Join(j.capsuleDir, filepath.FromSlash(capsule.StepModesJSON))
	if _, err := os.Lstat(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	workflowsDir := filepath.Join(j.capsuleDir, capsule.DirWorkflows)
	if !workflow.LanePresent(workflowsDir, j.bundleName) {
		return nil
	}
	lane, _ := workflow.Load(filepath.Join(workflowsDir, j.bundleName))
	cfg, err := r.LoadWorkspace(j.project)
	if err != nil {
		return err
	}
	modes, refused := workspace.HumanMergeSteps(cfg, lane, j.project)
	if refused != "" {
		return fmt.Errorf("runtime: %s: the claim refuses over the workspace yaml: %s", j.wi, refused)
	}
	if modes == nil {
		modes = map[string]string{}
	}
	// a by the yaml declares for a step this lane does not carry rides
	// along unchanged: a lane's step may read another lane's step mode
	// (the batch repair's gate reads the merge mode), and the claim
	// never refuses over one — the refusal above is only for a step the
	// lane carries
	if lane != nil && cfg != nil && cfg.By != nil {
		carried := make(map[string]bool, len(lane.Steps))
		for _, st := range lane.Steps {
			carried[st.ID] = true
		}
		for id, mode := range cfg.By[j.project] {
			if !carried[id] {
				modes[id] = mode
			}
		}
	}
	blob, err := json.Marshal(modes)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(path, append(blob, '\n'), 0o644); err != nil {
		return fmt.Errorf("runtime: %s: write %s: %w", j.wi, capsule.StepModesJSON, err)
	}
	return nil
}
