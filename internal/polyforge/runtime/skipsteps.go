package runtime

// skipsteps.go: the work item's skipped-step set, fixed
// in the capsule when the work item is first claimed
// (capsule.SkippedStepsJSON): the steps the capsule's lane snapshot
// declares skippable that the project's workspace yaml switches off
// (workspace.SkippedSteps). The file is written only when the capsule
// does not carry one yet — a re-claim or a resume clones the capsule
// chain, which carries the first claim's file, and keeps it as it is, so
// a switch flipped while the work item runs never reaches it. The driver
// reads the file, never the yaml.

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

// writeSkippedSteps writes the capsule's skipped-step set unless the
// capsule already carries one. A capsule without a lane snapshot gets
// none (there is nothing to run). The workspace yaml is read through the
// pre-claim's strict load (LoadWorkspace): a yaml that stopped loading
// since the pre-claim check refuses the materialize.
func (r *Runtime) writeSkippedSteps(j *job) error {
	path := filepath.Join(j.capsuleDir, filepath.FromSlash(capsule.SkippedStepsJSON))
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
	skipped := workspace.SkippedSteps(cfg, lane, j.project)
	if skipped == nil {
		skipped = []string{}
	}
	blob, err := json.Marshal(skipped)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(path, append(blob, '\n'), 0o644); err != nil {
		return fmt.Errorf("runtime: %s: write %s: %w", j.wi, capsule.SkippedStepsJSON, err)
	}
	return nil
}
