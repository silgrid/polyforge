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
// models (set the chain), clear_models, skip (true/false), param (set
// one step param) or clear_param (polyforge#78). The write validates
// here — the step's declaration and status in the scene the posture
// resolves to, the candidate shape, the step-agent models.json
// membership of every pi model, a param's declaration in the capsule's
// lane snapshot and its value (workflow.ParamValueProblem) — and the
// actor turn re-checks the step status (the one thing that can move
// before its turn).
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
	"sort"
	"time"

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
	// polyforge#79: a QUEUED work item takes the attrs face — its
	// per-ticket params ride the reserved step_params attrs key, so any
	// machine and any Cell that claims it sees them — never a local
	// scene, not even one an earlier claim left here. Only the params
	// actions answer here (the write route refuses the rest); the read
	// face composes from this machine's lane set.
	if item != nil && item.Status == api.StatusQueued {
		bundle := ""
		if item.BundleName != nil {
			bundle = *item.BundleName
		}
		return &overrideScene{posture: runtime.OverridePostureQueued, bundle: bundle}, nil
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
	// polyforge#79: a queued work item owns no capsule here — its overlay
	// view composes from this machine's lane set and the attrs key
	// (review r1#7: the capsule paths below would read the daemon's own
	// current directory).
	if scene.posture == runtime.OverridePostureQueued {
		return d.queuedOverrideView(wi, item, scene)
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
		row.Pending = a.Models || a.Skip || a.Params
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
	// the param overrides only (a cleared param overrides nothing), by
	// name — never the final value, which the lower layers compose
	for name, p := range e.Params {
		if p != nil && !p.Cleared {
			row.Params = append(row.Params, protocol.OverrideParamView{Name: name, Value: p.Value, By: p.By, At: p.At})
		}
	}
	sort.Slice(row.Params, func(i, j int) bool { return row.Params[i].Name < row.Params[j].Name })
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
	if req.Param != nil {
		actions++
	}
	if req.ClearParam != "" {
		actions++
	}
	if actions != 1 {
		writeErr(w, api.ErrDaemonBadInput, "exactly one action per request: models (set), clear_models, skip (true/false), param (set) or clear_param")
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
	// polyforge#79: a queued work item's write is the attrs-key face —
	// the params ride the reserved step_params key (the claim merges
	// them into its capsule); it owns no capsule here, so the lane
	// snapshot below is never its guard. The queue write applies the
	// request itself, validates it against this machine's lane set and
	// answers its own shape.
	if scene.posture == runtime.OverridePostureQueued {
		out, qerr := d.queuedParamWrite(wi, item, req, operator)
		if qerr != nil {
			writeErrOf(w, qerr)
			return
		}
		writeJSON(w, http.StatusOK, out)
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
	// a param is one the capsule's lane snapshot declares on the step,
	// its value one line within the bound every layer shares
	if name := req.ClearParam; req.Param != nil || name != "" {
		if req.Param != nil {
			name = req.Param.Name
		}
		if _, ok := spec.Params[name]; !ok {
			writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("the capsule's lane snapshot declares no step param %q on step %s: a param is set only where the lane declares it", name, req.Step))
			return
		}
		if req.Param != nil {
			if why := workflow.ParamValueProblem(req.Param.Value); why != "" {
				writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("param %s: %s", name, why))
				return
			}
		}
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
			if err := d.guardStartedLastStep(scene, wf, spec); err != nil {
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
		Param: req.Param, ClearParam: req.ClearParam,
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

// lastAgentStepOf answers the last step the workflow still dispatches
// (nil when the workflow is empty or everything is skipped).
func lastAgentStepOf(wf *driver.Workflow) *driver.StepSpec {
	if wf == nil {
		return nil
	}
	for i := len(wf.Steps) - 1; i >= 0; i-- {
		if !wf.Steps[i].Skipped {
			return &wf.Steps[i]
		}
	}
	return nil
}

// guardStartedLastStep refuses a skip that would move the lane's
// last-agent-step onto a step that already started (polyforge#47 AC1):
// the running step was dispatched under the step order it read, and the
// obligations a mid-run skip hands the new last step — the moved wrap
// attrs, the published-record registration — were settled at that
// dispatch. The same skip before that step starts is the owner's to
// make; a skip that moves no tail obligation (the last agent step
// stays the last agent step) is none of this guard's business.
func (d *Daemon) guardStartedLastStep(scene *overrideScene, wf *driver.Workflow, spec *driver.StepSpec) error {
	base := lastAgentStepOf(wf.WithSkipped(driver.EffectiveSkips(scene.capsuleDir)))
	if base == nil {
		return nil // an empty lane: nothing runs, guardWrapAttrs owns the attrs
	}
	set := driver.EffectiveSkips(scene.capsuleDir)
	if !slices.Contains(set, spec.ID) {
		set = append(append([]string(nil), set...), spec.ID)
	}
	after := lastAgentStepOf(wf.WithSkipped(set))
	if after == nil || after.ID == base.ID {
		return nil // the skip strands no last-step obligation
	}
	if st := scene.statusOf(after.ID); st == capsule.StepInProgress || st == capsule.StepDone {
		return &api.Error{Code: api.ErrDaemonConflict,
			Detail: fmt.Sprintf("skipping step %s now would make step %s the lane's last step, but %s already started (its status is %s): the last-step obligations were settled at its dispatch — skip %s before %s starts, or let %s run", spec.ID, after.ID, after.ID, st, spec.ID, after.ID, spec.ID)}
	}
	return nil
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

// queuedParamWrite applies one param change of a QUEUED work item to the
// reserved step_params attrs key (polyforge#79): the write is the
// daemon's machine-signed hub face (the key's one writer), whole-key
// read-merge-write so every step's params survive one another, judged
// against THIS machine's lane set before anything is written — the same
// guard the running posture's capsule snapshot is. Only the params
// answer here: a queued work item's chain and skip switch are set once
// it runs. The aihub-side attrs_written event is the change's record
// (no observe-lane event: no capsule here carries one). A claim that
// lands while the write does is detected by the re-read (the owner's
// 已定 3): the key gone says the claim merged the params into its
// capsule; the key still there says the change never reached the claim
// and the running ticket needs the set again.
func (d *Daemon) queuedParamWrite(wi string, item *api.WorkItem, req protocol.OverrideWriteRequest, operator string) (*protocol.OverrideWriteResult, error) {
	if req.Param == nil && req.ClearParam == "" {
		return nil, &api.Error{Code: api.ErrDaemonConflict,
			Detail: fmt.Sprintf("%s is queued: a queued work item takes its params only (they ride the work item's attrs until its claim) — set the model chain or the skip switch once it runs", wi)}
	}
	if item.BundleName == nil || *item.BundleName == "" {
		return nil, &api.Error{Code: api.ErrDaemonConflict,
			Detail: fmt.Sprintf("%s names no bundle: its params are written against the lane it will run, and the lane is the bundle it names", wi)}
	}
	lane := d.laneOf(item.ProjectName, *item.BundleName)
	if lane == nil {
		return nil, &api.Error{Code: api.ErrDaemonConflict,
			Detail: fmt.Sprintf("the lane %s of %s does not load on this daemon — a queued work item's params are written against its lane (the step declarations); write them on a machine whose lane set has it", *item.BundleName, item.ProjectName)}
	}
	var spec *workflow.Step
	for i := range lane.Steps {
		if lane.Steps[i].ID == req.Step {
			spec = &lane.Steps[i]
		}
	}
	if spec == nil {
		return nil, &api.Error{Code: api.ErrDaemonBadInput,
			Detail: fmt.Sprintf("the lane declares no step %q", req.Step)}
	}
	name := req.ClearParam
	if req.Param != nil {
		name = req.Param.Name
	}
	if _, declared := spec.Params[name]; !declared {
		return nil, &api.Error{Code: api.ErrDaemonBadInput,
			Detail: fmt.Sprintf("the lane declares no step param %q on step %s: a param is set only where the lane declares it", name, req.Step)}
	}
	if req.Param != nil {
		if why := workflow.ParamValueProblem(req.Param.Value); why != "" {
			return nil, &api.Error{Code: api.ErrDaemonBadInput,
				Detail: fmt.Sprintf("param %s: %s", name, why)}
		}
	}
	// the whole-key read-merge-write: the current key (this route's own
	// reads resolved the item fresh), the change applied to a copy
	current := runtime.StepParamsOfAttrs(item.Attrs)
	merged := map[string]map[string]runtime.StepParamAttr{}
	for step, params := range current {
		merged[step] = map[string]runtime.StepParamAttr{}
		for n, v := range params {
			merged[step][n] = v
		}
	}
	if req.Param != nil {
		if v, ok := merged[req.Step][name]; ok && v.Value == req.Param.Value {
			// the idempotent no-change success: nothing written
			return d.queuedParamAnswer(wi, req, spec, merged, false, "", operator), nil
		}
		if merged[req.Step] == nil {
			merged[req.Step] = map[string]runtime.StepParamAttr{}
		}
		// the entry's own write time — the claim merges by it, so a
		// residue never beats a newer running write and a re-queue's own
		// write wins (review r2#1)
		merged[req.Step][name] = runtime.StepParamAttr{Value: req.Param.Value,
			At: time.Now().UTC().Format(time.RFC3339Nano)}
	} else {
		if _, ok := merged[req.Step][name]; !ok {
			return d.queuedParamAnswer(wi, req, spec, merged, false, "", operator), nil
		}
		delete(merged[req.Step], name)
		if len(merged[req.Step]) == 0 {
			delete(merged, req.Step)
		}
	}
	raw, merr := json.Marshal(merged)
	if merr != nil {
		return nil, merr
	}
	if len(merged) == 0 {
		raw = []byte("null") // the last param's clear deletes the key
	}
	out, werr := d.hub.WriteAttrs(wi, api.AttrsWriteRequest{Key: api.StepParamsKey, Value: raw})
	if werr != nil {
		return nil, werr
	}
	note := "queued: the params ride the work item's step_params attrs key — the claim merges them into its capsule and clears the key once that publish succeeded"
	if out != nil && out.Status != api.StatusQueued {
		// the claim race the write side detects: one re-read says which
		// of the two races it was
		if cur, gerr := d.hub.GetWorkItem(wi); gerr == nil && cur != nil {
			if runtime.StepParamsOfAttrs(cur.Attrs) == nil {
				note = fmt.Sprintf("the work item was claimed while the write landed (its status is %s) and the claim merged the params into its capsule: read them with pf params", cur.Status)
			} else {
				note = fmt.Sprintf("the work item was claimed while the write landed (its status is %s) and this change did not reach its capsule: set the param on the running ticket again", cur.Status)
			}
		} else {
			note = fmt.Sprintf("the work item was claimed while the write landed (its status is %s): read the view before you send the change again", out.Status)
		}
	}
	return d.queuedParamAnswer(wi, req, spec, merged, true, note, operator), nil
}

// queuedParamAnswer shapes the attrs-key write's answer: the step's row
// carries the params the merged key holds for it (the overlay-view
// shape), changed false naming the idempotent no-change success.
func (d *Daemon) queuedParamAnswer(wi string, req protocol.OverrideWriteRequest, spec *workflow.Step,
	merged map[string]map[string]runtime.StepParamAttr, changed bool, note, operator string) *protocol.OverrideWriteResult {
	row := protocol.OverrideStepView{ID: req.Step, Skippable: spec.Skippable}
	names := make([]string, 0, len(merged[req.Step]))
	for name := range merged[req.Step] {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		row.Params = append(row.Params, protocol.OverrideParamView{Name: name, Value: merged[req.Step][name].Value, By: operator})
	}
	return &protocol.OverrideWriteResult{WorkItem: wi, Changed: changed,
		Posture: runtime.OverridePostureQueued, Published: false, Note: note, Step: row}
}
