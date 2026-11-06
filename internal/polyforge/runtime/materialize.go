package runtime

// materialize.go: the F7 lane — the bundle copy into a freshly cloned
// capsule:
//
//   - the full bundle tree lands under workflows/<bundle>/ (the driver
//     reads step definitions from workflows/<bundle>/steps/ directly) —
//     the lane the work item's lane set resolved, a built-in lane or its
//     workspace's user lane alike; the built-in shared tree
//     (workflows/_shared/, when the release has one) lands beside it
//     under workflows/_shared/, less its scripts' own unit tests
//     (sharedTestsDir), so a user lane's snapshot falls back on
//     exactly the shared files it was judged with;
//   - every step's definition, rendered by the one definition render the
//     driver reads it through (workflow.RenderDefinition, the step's
//     vars), lands under workflows/_rendered/<step>.md: the text a step's
//     sub-agent is pointed at, the same the step's own prompt carries;
//   - the completion marker sync/materialized.json records the pinned
//     bundle identity (name), the workflow contract version the snapshot
//     targets, its source and content hash, and the full targets list,
//     written LAST; recovery relaunches only over a snapshot the marker
//     vouches for (materializationComplete). The snapshot is loaded
//     through the one lane loader before the marker is written: a lane
//     the contract refuses is never vouched for.
//
// Materialize runs once per claim, on a fresh clone, before any agent
// starts (prepareCapsule never reuses a previous scene), so the copy
// needs no staging: whatever an earlier claim committed under the three
// trees is removed and the checkout's copy replaces it.
//
// sync/ is daemon-private state: it never enters the capsule's revision
// chain — the .gitignore carries /sync/ for exactly that reason.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

const (
	// RelMaterializeMarker is the completion marker: sync/materialized.json.
	RelMaterializeMarker = "sync/materialized.json"
	// gitignoreSyncLine is appended to the capsule .gitignore: sync/ is
	// daemon-private marker state and never enters the chain.
	gitignoreSyncLine = "/sync/\n"
	// renderedDir holds each step's rendered definition beside the
	// snapshot (workflows/_rendered/<step>.md); "_" keeps it out of the
	// lane directories.
	renderedDir = "_rendered"
)

// materializeMarkerRecord is the content of the completion marker: the
// pinned bundle identity, the snapshot's workflow contract version,
// source and content hash, every materialized file and the completion
// timestamp.
type materializeMarkerRecord struct {
	BundleName string `json:"bundle_name"`
	// Contract is the workflow contract version the snapshot targets (its
	// workflow.yaml's `contract`); the driver blocks a snapshot whose
	// version is not the daemon's (kind `contract`).
	Contract int `json:"contract"`
	// Source is where the lane came from: workflow.SourceBuiltin (the
	// daemon's release) or workflow.SourceWorkspace (the user lane of the
	// work item's workspace).
	Source string `json:"source"`
	// ContentHash is workflow.ContentHash over Targets.
	ContentHash string `json:"content_hash"`
	// Targets lists every materialized file's capsule-relative path
	// (slash-separated, sorted).
	Targets     []string `json:"targets"`
	CompletedAt string   `json:"completed_at"`
}

// materializationComplete is the recovery trust gate: the capsule's
// snapshot is complete for THIS job's pinned bundle — the marker parses,
// names the job's bundle, records a target set, and every recorded
// target is a regular file on disk. Anything else answers false (fail
// closed).
func (r *Runtime) materializationComplete(j *job) bool {
	if j.bundleName == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(j.capsuleDir, filepath.FromSlash(RelMaterializeMarker)))
	if err != nil {
		return false
	}
	var m materializeMarkerRecord
	if json.Unmarshal(raw, &m) != nil || m.BundleName != j.bundleName || len(m.Targets) == 0 {
		return false
	}
	for _, rel := range m.Targets {
		info, serr := os.Stat(filepath.Join(j.capsuleDir, filepath.FromSlash(rel)))
		if serr != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// materializeBundle copies the pinned bundle (and the shared step
// definitions beside it) into the capsule and writes the completion
// marker last. Claim-time entry. The bytes are the lane the pre-claim
// check judged (j.lane); a journal replay, which has none, copies the
// lane the work item's lane set resolves now. A re-claim whose capsule
// chain already carries a lane snapshot of another workflow contract
// version keeps that snapshot (keptContractSnapshot): the driver then
// parks the work item blocked with kind contract instead of running an
// old record on a new lane.
func (r *Runtime) materializeBundle(j *job) error {
	if r.cfg.WorkflowsDir == "" || j.bundleName == "" {
		return nil
	}
	if j.bundleName == workflow.SharedDir {
		return fmt.Errorf("runtime: %s: %q holds the shared step definitions and is not a workflow bundle", j.wi, workflow.SharedDir)
	}
	stage := j.lane
	if stage == nil || stage.bundle != j.bundleName {
		var err error
		if stage, err = stageLane(r.laneSet(j.project), j.bundleName); err != nil {
			return fmt.Errorf("runtime: %s: %w", j.wi, err)
		}
	}
	workflowsDir := filepath.Join(j.capsuleDir, capsule.DirWorkflows)
	laneDir := filepath.Join(workflowsDir, j.bundleName)
	if lane, kept := keptContractSnapshot(laneDir); kept {
		fmt.Fprintf(os.Stderr, "runtime: %s: the capsule's lane snapshot targets workflow contract %d, not %d: kept, so the work item parks blocked (kind contract)\n", j.wi, lane, workflow.ContractVersion)
		return r.writeMaterializeMarker(j, lane, stage.source)
	}
	for _, tree := range []string{j.bundleName, workflow.SharedDir, renderedDir} {
		if err := os.RemoveAll(filepath.Join(workflowsDir, tree)); err != nil {
			return err
		}
	}
	if err := stage.writeTo(workflowsDir); err != nil {
		return err
	}
	lane, errs := workflow.LoadLane(laneDir, stage.source, filepath.Join(workflowsDir, workflow.SharedDir))
	if err := workflow.AsLoadError(laneDir, errs); err != nil {
		return fmt.Errorf("runtime: %s: the lane snapshot does not load: %w", j.wi, err)
	}
	if err := writeRenderedDefinitions(laneDir, filepath.Join(workflowsDir, renderedDir), lane.Steps); err != nil {
		return fmt.Errorf("runtime: %s: %w", j.wi, err)
	}
	return r.writeMaterializeMarker(j, lane.Contract, stage.source)
}

// writeRenderedDefinitions writes each step's definition, rendered as the
// driver reads it (workflow.RenderDefinition over the capsule's lane
// snapshot with the step's vars), read-only to dir/<step>.md.
func writeRenderedDefinitions(laneDir, dir string, steps []workflow.Step) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, s := range steps {
		raw, err := workflow.RenderDefinition(laneDir, s.ID, s.Vars)
		if err != nil {
			return fmt.Errorf("rendering step %s: %w", s.ID, err)
		}
		if err := atomicfile.Write(filepath.Join(dir, s.ID+".md"), raw, 0o444); err != nil {
			return err
		}
	}
	return nil
}

// keptContractSnapshot reports whether the capsule's existing lane
// snapshot (a re-claim's chain carries the one its first claim
// materialized) targets another workflow contract version than this
// daemon's — WFC-100 or WFC-101, a contract 4 workflow.md snapshot among
// them — and answers the version it targets (0 when it declares none).
// Such a snapshot is never replaced.
func keptContractSnapshot(laneDir string) (int, bool) {
	if !workflow.LanePresent(filepath.Dir(laneDir), filepath.Base(laneDir)) {
		return 0, false
	}
	lane, errs := workflow.Load(laneDir)
	for _, e := range errs {
		if e.Code == workflow.CodeContractMissing || e.Code == workflow.CodeContractVersion {
			v := 0
			if lane != nil {
				v = lane.Contract
			}
			return v, true
		}
	}
	return 0, false
}

// writeMaterializeMarker writes the completion marker over the files the
// capsule's workflows dir holds for the lane, the shared tree and the
// rendered definitions, naming the lane's source.
func (r *Runtime) writeMaterializeMarker(j *job, contract int, source string) error {
	var targets []string
	for _, tree := range []string{j.bundleName, workflow.SharedDir, renderedDir} {
		live := filepath.Join(j.capsuleDir, capsule.DirWorkflows, tree)
		if !dirExists(live) {
			continue
		}
		files, err := regularFiles(j.capsuleDir, live)
		if err != nil {
			return err
		}
		targets = append(targets, files...)
	}
	sort.Strings(targets)
	hash, err := workflow.ContentHash(j.capsuleDir, targets)
	if err != nil {
		return err
	}
	blob, err := json.MarshalIndent(materializeMarkerRecord{
		BundleName:  j.bundleName,
		Contract:    contract,
		Source:      source,
		ContentHash: hash,
		Targets:     targets,
		CompletedAt: nowStamp(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(j.capsuleDir, filepath.FromSlash(RelMaterializeMarker)), blob, 0o444)
}

// regularFiles answers every regular file under dir as a path relative
// to base, slash-separated.
func regularFiles(base, dir string) ([]string, error) {
	var out []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, werr error) error {
		if werr != nil || !info.Mode().IsRegular() {
			return werr
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

// laneStage is one lane read into memory at the pre-claim check: its
// files and the built-in shared tree, keyed by their slash path under a
// workflows dir (the lane's under <bundle>/ wherever it came from), and
// the lane's source. The claim materializes exactly these bytes.
type laneStage struct {
	bundle string
	source string
	files  map[string][]byte
}

// sharedTestsDir is the one subtree of the built-in shared tree a lane
// snapshot leaves out: the shared scripts' own unit tests and their
// fixtures, which no script, skill or check reads at runtime.
const sharedTestsDir = "scripts/tests"

// stageLane reads the lane bundle the lane set resolves and the set's
// built-in shared tree (a workspace has none of its own), less the shared
// tree's sharedTestsDir; a name the set does not carry is the WFC-123
// refusal.
func stageLane(set workflow.LaneSet, bundle string) (*laneStage, error) {
	dir, source, ok := set.Resolve(bundle)
	if !ok {
		return nil, fmt.Errorf("%s", LaneMissingDetail(bundle, set))
	}
	st := &laneStage{bundle: bundle, source: source, files: map[string][]byte{}}
	for tree, base := range map[string]string{bundle: dir, workflow.SharedDir: filepath.Join(set.Builtin, workflow.SharedDir)} {
		if !dirExists(base) {
			continue
		}
		err := filepath.Walk(base, func(p string, info os.FileInfo, werr error) error {
			if werr != nil {
				return werr
			}
			rel, err := filepath.Rel(base, p)
			if err != nil {
				return err
			}
			if info.IsDir() && tree == workflow.SharedDir && filepath.ToSlash(rel) == sharedTestsDir {
				return filepath.SkipDir
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			st.files[tree+"/"+filepath.ToSlash(rel)] = raw
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return st, nil
}

// writeTo lays the staged files out under a workflows dir, read-only (the
// snapshot is never edited in place).
func (st *laneStage) writeTo(workflowsDir string) error {
	for rel, raw := range st.files {
		p := filepath.Join(workflowsDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := atomicfile.Write(p, raw, 0o444); err != nil {
			return err
		}
	}
	return nil
}

// stageCheckedLane is the pre-claim lane check of Start: the lane the
// work item's lane set resolves (laneSet: the built-in lanes and its
// project's workspace's user lanes) is read into memory and the one lane
// loader judges exactly those bytes (laid out in a scratch directory,
// beside the built-in shared tree they fall back on), with CheckBundle's
// refusals — a lane the lane set does not carry is WFC-123, a refused one
// names every defect; either way nothing is claimed. The staged lane is
// what the claim materializes. No workflows dir: nothing is judged or
// staged.
func (r *Runtime) stageCheckedLane(wi, project, bundle string) (*laneStage, error) {
	if err := r.CheckBundle(wi, project, bundle); err != nil {
		return nil, err
	}
	if bundle == "" || r.cfg.WorkflowsDir == "" {
		return nil, nil
	}
	refuse := func(err error) error {
		return &api.Error{Code: api.ErrDaemonWorkspaceYaml,
			Detail: fmt.Sprintf("the workflow bundle of %s refused the claim before it was made (the work item stays queued): %v", wi, err)}
	}
	st, err := stageLane(r.laneSet(project), bundle)
	if err != nil {
		return nil, refuse(err)
	}
	scratch, err := os.MkdirTemp("", "pf-lane-")
	if err != nil {
		return nil, refuse(err)
	}
	defer os.RemoveAll(scratch)
	if err := st.writeTo(scratch); err != nil {
		return nil, refuse(err)
	}
	if !workflow.LanePresent(scratch, bundle) {
		return nil, refuse(fmt.Errorf("%s", LaneMissingDetail(bundle, r.laneSet(project))))
	}
	_, errs := workflow.LoadLane(filepath.Join(scratch, bundle), st.source, filepath.Join(scratch, workflow.SharedDir))
	if err := workflow.AsLoadError(filepath.Join(scratch, bundle), errs); err != nil {
		return nil, refuse(fmt.Errorf("workflow bundle %s refused: %w", bundle, err))
	}
	return st, nil
}
