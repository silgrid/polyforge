package daemon

// overrides.go: the step-overlay face (polyforge#25) — the per-work-item
// routes
//
//	GET  /jobs/{wi}/overrides
//	POST /jobs/{wi}/overrides
//
// The view answers one row per declared step (its status, the lane's
// skippable declaration, the overlay aspects, the effective model chain
// and its source); the write applies one change through the runtime's
// per-work-item actor (stepoverrides.go), one action per request:
// models (set the chain), clear_models, skip (true/false). The write
// validates here — the step's declaration and status in the scene the
// posture resolves to, the candidate shape, the step-agent models.json
// membership of every pi model — and the actor turn re-checks the step
// status (the one thing that can move before its turn).
//
// Reach: the write sits behind refuseStepOrigin like every POST (a step
// or task process tree is refused DAEMON-403); the view is a read any
// local peer may make. Postures: a live running job, a retained paused
// record, or a work item this daemon no longer holds (parked blocked) —
// the change then rides the daemon-local pending record into the next
// claim's fresh capsule.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// The overlay source labels (protocol.OverrideStepView.Source). The
// manual list's own label lives in workspace.StepModelSources
// ("pf submit --models"); the chain commit's message prefix is the
// runtime's ("step override: ").
const (
	overrideSource     = "step override"
	overrideSourcePend = "step override (pending)"
)

// overrideScene is the posture the routes resolve a work item to, with
// the scene (capsule dir) and its bundle name.
type overrideScene struct {
	posture    string
	capsuleDir string
	bundle     string
	exec       *capsule.Execution
	live       bool
}

// resolveOverrideScene resolves the work item's posture on this daemon:
// a live running job, a retained paused record, or the sealed scene of
// a work item this daemon no longer holds (parked blocked, or paused
// with its record gone); a terminal work item answers 409. A work item
// this daemon holds in a transient state (claiming, closing) answers
// the try-again 409; one with no scene here answers 404.
func (d *Daemon) resolveOverrideScene(wi string, item *api.WorkItem) (*overrideScene, error) {
	if ji, ok := d.rt.Info(wi); ok {
		switch ji.State {
		case runtime.StateRunning:
			return &overrideScene{posture: runtime.OverridePostureRunning, capsuleDir: ji.CapsuleDir,
				bundle: ji.BundleName, live: true}, nil
		case runtime.StatePaused:
			return &overrideScene{posture: runtime.OverridePosturePaused, capsuleDir: ji.CapsuleDir,
				bundle: ji.BundleName, live: true}, nil
		default:
			return nil, &api.Error{Code: api.ErrDaemonConflict,
				Detail: fmt.Sprintf("%s is %s on this daemon right now; try again in a moment", wi, ji.State)}
		}
	}
	// no job here: the sealed scene of the last close (a blocked or
	// long-paused work item), located the answer path's way
	ex, dir := d.loadExecution(wi, item)
	if dir == "" {
		return nil, &api.Error{Code: api.ErrDaemonNotFound,
			Detail: fmt.Sprintf("%s is not running on this daemon and holds no capsule of it here; set the overlay on the machine that runs it", wi)}
	}
	// only a work item that will be claimed again takes the change: a
	// blocked one (or a paused one whose record went); a terminal one
	// (wrapped, canceled, failed, …) never runs again, so its change
	// would neither land nor ever be consumed
	if item == nil || (item.Status != api.StatusBlocked && item.Status != api.StatusPaused) {
		status := "unknown to aihub"
		if item != nil {
			status = item.Status
		}
		return nil, &api.Error{Code: api.ErrDaemonConflict,
			Detail: fmt.Sprintf("%s is %s: the overlay is for a work item that runs again (running, paused or blocked)", wi, status)}
	}
	idn := capsuleIdentityOf(dir)
	return &overrideScene{posture: runtime.OverridePostureBlocked, capsuleDir: dir,
		bundle: idn.BundleName, exec: ex}, nil
}

// capsuleIdentityOf reads a capsule's identity (capsule.json); a torn one
// answers the zero identity.
func capsuleIdentityOf(dir string) capsule.Identity {
	raw, err := os.ReadFile(filepath.Join(dir, capsule.CapsuleJSON))
	if err != nil {
		return capsule.Identity{}
	}
	var idn capsule.Identity
	_ = json.Unmarshal(raw, &idn)
	return idn
}

// execOf answers the scene's execution record (nil when absent/torn — a
// fresh capsule, or a claim that never wrote one).
func (s *overrideScene) execOf() *capsule.Execution {
	if s.exec != nil {
		return s.exec
	}
	s.exec = readExecutionFile(filepath.Join(s.capsuleDir, capsule.ExecutionJSON))
	return s.exec
}

// laneGraph loads the scene's lane graph (the workflow.yaml the driver
// would run, definitions not needed): the step order, the skippable and
// wrap-attrs declarations.
func (s *overrideScene) laneGraph() (*driver.Workflow, error) {
	dir := filepath.Join(s.capsuleDir, capsule.DirWorkflows, s.bundle)
	if _, err := os.Stat(filepath.Join(dir, workflow.WorkflowFile)); err != nil {
		return nil, &api.Error{Code: api.ErrDaemonConflict,
			Detail: fmt.Sprintf("the capsule's lane snapshot of %s does not read here; the overlay needs it (the step declarations)", s.bundle)}
	}
	wf, err := driver.LoadWorkflowGraph(dir)
	if err != nil {
		return nil, &api.Error{Code: api.ErrDaemonConflict, Detail: err.Error()}
	}
	return wf, nil
}

// statusOf answers the step's recorded status ("" when absent).
func (s *overrideScene) statusOf(step string) string {
	if ex := s.execOf(); ex != nil {
		if st := ex.Steps[step]; st != nil {
			return st.Status
		}
	}
	return ""
}

// GET /jobs/{wi}/overrides: the per-step overlay view.
func (d *Daemon) overrideViewRoute(w http.ResponseWriter, r *http.Request) {
	wi, item := d.resolveWI(r.PathValue("wi"))
	if wi == "" {
		wi = r.PathValue("wi")
	}
	view, err := d.overrideView(wi, item)
	if err != nil {
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (d *Daemon) overrideView(wi string, item *api.WorkItem) (*protocol.OverrideView, error) {
	scene, err := d.resolveOverrideScene(wi, item)
	if err != nil {
		return nil, err
	}
	wf, err := scene.laneGraph()
	if err != nil {
		return nil, err
	}
	// the capsule's overlay record, and (parked) the pending entries merged
	// over it by the claim's own rule — a pending aspect older than the
	// capsule's same aspect does not apply, so the row shows what the
	// re-claim will run (AC8)
	ov, oerr := capsule.ReadStepOverrides(scene.capsuleDir)
	if oerr != nil {
		ov = capsule.StepOverrides{}
	}
	var pending map[string]*capsule.StepOverride
	if !scene.live {
		pending = d.rt.PendingStepOverrides(wi)
	}
	ov, applied := runtime.MergedStepOverrides(ov, pending)
	// the sources below the overlay, for the effective labels
	var sources map[string]workspace.StepModelSource
	if lane := laneSnapshotOf(scene); lane != nil {
		cfg, _ := d.rt.LoadWorkspace(laneProject(item, scene))
		sources = workspace.StepModelSources(lane, cfg, laneProject(item, scene), d.rt.ManualModels(wi))
	}
	out := &protocol.OverrideView{WorkItem: wi, Posture: scene.posture, Steps: []protocol.OverrideStepView{}}
	for _, spec := range wf.Steps {
		row := protocol.OverrideStepView{ID: spec.ID, Skippable: spec.Skippable, Status: scene.statusOf(spec.ID)}
		if e := ov.Steps[spec.ID]; e != nil {
			fillRow(&row, e)
		}
		a := applied[spec.ID]
		row.Pending = a.Models || a.Skip
		switch {
		case row.Models != nil && a.Models:
			row.Effective, row.Source = row.Models, overrideSourcePend
		case row.Models != nil:
			row.Effective, row.Source = row.Models, overrideSource
		case sources != nil:
			if src, ok := sources[spec.ID]; ok {
				row.Effective, row.Source = src.Models, src.Source
			}
		}
		out.Steps = append(out.Steps, row)
	}
	return out, nil
}

// fillRow copies one entry's aspects onto the row.
func fillRow(row *protocol.OverrideStepView, e *capsule.StepOverride) {
	if e == nil {
		return
	}
	if e.Models != nil {
		row.Models, row.ModelsBy, row.ModelsAt = e.Models, e.ModelsBy, e.ModelsAt
	}
	if e.ModelsCleared {
		// the pending clear tombstone: the sealed chain no longer governs
		row.Models, row.ModelsBy, row.ModelsAt = nil, "", ""
	}
	if e.Skip != nil {
		row.Skip, row.SkipBy, row.SkipAt = e.Skip, e.SkipBy, e.SkipAt
	}
}

// laneSnapshotOf parses the scene's lane (workflow.yaml) for the source
// labelling; nil when it does not read (the rows then carry no
// below-overlay source).
func laneSnapshotOf(scene *overrideScene) *workflow.Lane {
	dir := filepath.Join(scene.capsuleDir, capsule.DirWorkflows, scene.bundle)
	if _, err := os.Stat(filepath.Join(dir, workflow.WorkflowFile)); err != nil {
		return nil
	}
	lane, errs := workflow.Load(dir)
	if lane == nil && len(errs) > 0 {
		return nil
	}
	return lane
}

// laneProject answers the project the source labelling composes for.
func laneProject(item *api.WorkItem, scene *overrideScene) string {
	if item != nil && item.ProjectName != "" {
		return item.ProjectName
	}
	return capsuleIdentityOf(scene.capsuleDir).Project
}

// POST /jobs/{wi}/overrides: one change, one action.
func (d *Daemon) overrideWriteRoute(w http.ResponseWriter, r *http.Request) {
	wi, item := d.resolveWI(r.PathValue("wi"))
	if wi == "" {
		wi = r.PathValue("wi")
	}
	var req protocol.OverrideWriteRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, api.ErrDaemonBadInput, "decode: "+err.Error())
		return
	}
	if req.Step == "" {
		writeErr(w, api.ErrDaemonBadInput, "step: the step id is required")
		return
	}
	actions := 0
	if req.Models != nil {
		actions++
	}
	if req.ClearModels {
		actions++
	}
	if req.Skip != nil {
		actions++
	}
	if actions != 1 {
		writeErr(w, api.ErrDaemonBadInput, "exactly one action per request: models (set), clear_models, or skip (true/false)")
		return
	}
	operator := req.Operator
	if operator == "" {
		operator = r.Header.Get("X-PF-User")
	}
	scene, err := d.resolveOverrideScene(wi, item)
	if err != nil {
		writeErrOf(w, err)
		return
	}
	wf, err := scene.laneGraph()
	if err != nil {
		writeErrOf(w, err)
		return
	}
	spec := wfStep(wf, req.Step)
	if spec == nil {
		writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("the lane declares no step %q", req.Step))
		return
	}
	status := scene.statusOf(req.Step)
	// the write's step-status rule, by posture: a live dispatch is
	// refused; a parked work item's frozen current step accepts a model
	// chain (the change-before-wake window); done/skipped steps never
	// accept anything; the skip switch is for steps that never started.
	if req.Skip == nil && (status == capsule.StepDone || status == capsule.StepSkipped) {
		writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("step %s already ran (its status is %s): the overlay is for steps that have not started", req.Step, status))
		return
	}
	if req.Skip != nil {
		// both directions of the switch are the lane's skippable
		// declaration's: a non-skippable step neither skips nor un-skips
		if !spec.Skippable {
			writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("the lane does not declare step %s skippable, so its skip switch cannot be set or cleared mid-run; to let a failed gate of it pass waive it instead (pf answer — the step hands off waive citing your words; waivable gates only)", req.Step))
			return
		}
		setting := *req.Skip
		if setting {
			if status != "" && status != capsule.StepPending {
				writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("step %s has started (its status is %s): the skip switch is for steps that never started", req.Step, status))
				return
			}
			if err := d.guardWrapAttrs(scene, wf, spec); err != nil {
				writeErrOf(w, err)
				return
			}
		} else {
			// an un-skip the effective set would keep anyway is refused
			// naming what it is (polyforge#47 AC2): the step is skipped at
			// the project level — the workspace yaml's switch the daemon
			// fixed in the capsule at its first claim — and the per-ticket
			// overlay cannot restore it (the driver honors the union of the
			// two). Accepting it reported a change that never took effect.
			if slices.Contains(driver.ReadSkippedSteps(scene.capsuleDir), req.Step) {
				writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("step %s is skipped at the project level (the workspace yaml's skip switch, fixed in this capsule at its first claim), so the per-ticket overlay cannot un-skip it: the project's switch governs the claims made after it changes — skip it on another step, or change the project's switch and file a new ticket", req.Step))
				return
			}
			if status == capsule.StepInProgress || status == capsule.StepDone {
				// the un-skip is the cleanup of the switch itself: a step the
				// switch already turned off (skipped) or one that never
				// started (pending) may un-skip; a step that RAN cannot
				writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("step %s has started (its status is %s): the skip switch is for steps that never started", req.Step, status))
				return
			}
		}
	} else if status == capsule.StepInProgress && scene.live && scene.posture == runtime.OverridePostureRunning {
		writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("step %s has a live dispatch (its status is %s): the overlay is for steps without one — set it while the work item is parked, or on another step", req.Step, status))
		return
	}
	// the chain's shape, and the pi models' membership in the Cell's
	// step-agent models.json
	models := req.Models
	if req.Models != nil {
		norm, nerr := workspace.NormalizeModels(req.Models)
		if nerr != nil {
			writeErr(w, api.ErrDaemonBadInput, "models: "+nerr.Error())
			return
		}
		acceptable := d.piModelsAcceptable()
		for _, c := range norm {
			parsed, _ := harness.ParseCandidate(c)
			if parsed.Harness == harness.Pi && !acceptable[parsed.Model] {
				writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("models: the pi model %q is not in this Cell's step-agent models.json, so a pi step could not run it — add it there first, then retry", parsed.Model))
				return
			}
		}
		models = norm
	}
	ch := runtime.OverrideChange{
		Step: req.Step, Operator: operator, Models: models, ClearModels: req.ClearModels, Skip: req.Skip,
	}
	if !scene.live {
		ch.SealedDir = scene.capsuleDir // a clear reaches the chain the sealed scene carries
	}
	out, err := d.rt.ApplyStepOverride(r.Context(), wi, ch)
	if err != nil {
		writeErrOf(w, err)
		return
	}
	// the answer's step row (the scene may have moved — the posture the
	// actor answered is the authority)
	row := protocol.OverrideStepView{ID: req.Step, Skippable: spec.Skippable, Status: status}
	if out.Entry != nil {
		fillRow(&row, out.Entry)
	}
	if out.Changed && out.Posture == runtime.OverridePostureBlocked {
		row.Pending = true
	}
	if out.Changed && row.Models != nil {
		row.Effective, row.Source = row.Models, overrideSource
		if row.Pending {
			row.Source = overrideSourcePend
		}
	}
	writeJSON(w, http.StatusOK, protocol.OverrideWriteResult{
		WorkItem: wi, Changed: out.Changed, Posture: out.Posture, Published: out.Published,
		Note: out.Note, Step: row,
	})
}

// guardWrapAttrs refuses a skip whose wrap attrs would strand: the step
// declares wrap_attrs and every step before it already finished, so
// nothing that could register the moved attrs will run again.
func (d *Daemon) guardWrapAttrs(scene *overrideScene, wf *driver.Workflow, spec *driver.StepSpec) error {
	if len(spec.WrapAttrs) == 0 {
		return nil
	}
	for _, s := range wf.Steps {
		if s.ID == spec.ID {
			break
		}
		if st := scene.statusOf(s.ID); st != capsule.StepDone && st != capsule.StepSkipped {
			return nil // something before it still runs: the attrs move to it
		}
	}
	return &api.Error{Code: api.ErrDaemonConflict,
		Detail: fmt.Sprintf("skipping step %s now would strand its wrap attrs (%s): everything before it already finished, so no later step can register them — skip it earlier, or let it run", spec.ID, spec.WrapAttrs[0])}
}

// wfStep answers the workflow's step spec by id (nil when absent).
func wfStep(wf *driver.Workflow, id string) *driver.StepSpec {
	if wf == nil {
		return nil
	}
	for i := range wf.Steps {
		if wf.Steps[i].ID == id {
			return &wf.Steps[i]
		}
	}
	return nil
}

// piModelsAcceptable answers the set of pi model ids this Cell's step
// agent may run, parsed from <harness-root>/step-agent/models.json: each
// provider's models[].id, in the `<provider>/<id>` form pi resolves and
// (when exactly one provider declares it) the bare id too. A missing or
// unparsable file answers the empty set (every pi candidate is refused —
// the file is the Cell's declaration of what its pi steps may run).
func (d *Daemon) piModelsAcceptable() map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(d.cfg.HarnessRoot, "step-agent", "models.json"))
	if err != nil {
		return out
	}
	var doc struct {
		Providers map[string]struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return out
	}
	bare := map[string]int{}
	for prov, p := range doc.Providers {
		for _, m := range p.Models {
			if m.ID == "" {
				continue
			}
			out[prov+"/"+m.ID] = true
			bare[m.ID]++
		}
	}
	for id, n := range bare {
		if n == 1 {
			out[id] = true
		}
	}
	return out
}
