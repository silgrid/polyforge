package daemon

// paramsview.go: the params read face (polyforge#79) —
//
//	GET /jobs/{wi}/params -> protocol.ParamsView
//
// One row per declared step, with every param the lane declares on it
// and each one's FINAL value and source: the per-ticket layer ("step
// override" — the capsule's overlay record of a claimed work item,
// merged with the daemon-local pending record of a parked one; the
// reserved step_params attrs key of a queued one), else the project's
// steps.<project>.<step>.params.<name>, else the lane default
// (bundle <lane> step <step> params.<name>). The overlay view
// (overrides.go) stays the overlay layer alone, per #78's ruling; this
// face is the composed one Pi Web's panel and work-item detail read
// through `pf params`.
//
// Reach: a read any local peer may make, exactly like the overlay view.
// A queued work item holds no capsule: its lane comes from this
// machine's lane set (the project's mapped workspace), so the view is
// this machine's answer — the machine that claims it may compose the
// project layer differently (its own .polyforge.yaml).

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// GET /jobs/{wi}/params: the per-step params face (the final values and
// their sources).
func (d *Daemon) paramsViewRoute(w http.ResponseWriter, r *http.Request) {
	wi, item := d.resolveWI(r.PathValue("wi"))
	if wi == "" {
		wi = r.PathValue("wi")
	}
	view, err := d.paramsView(wi, item)
	if err != nil {
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (d *Daemon) paramsView(wi string, item *api.WorkItem) (*protocol.ParamsView, error) {
	scene, err := d.resolveOverrideScene(wi, item)
	if err != nil {
		return nil, err
	}
	if scene.posture == runtime.OverridePostureQueued {
		return d.queuedParamsView(wi, item, scene)
	}
	wf, err := scene.laneGraph()
	if err != nil {
		return nil, err
	}
	// the layers below the per-ticket one, then the overlay record
	// merged with the pending record of a parked work item — the values
	// the next claim here runs with
	project := laneProject(item, scene)
	var below map[string]map[string]workspace.StepParamSource
	if lane := laneSnapshotOf(scene); lane != nil {
		cfg, _ := d.rt.LoadWorkspace(project)
		below = workspace.StepParamSources(scene.bundle, lane, cfg, project)
	}
	ov, oerr := capsule.ReadStepOverrides(scene.capsuleDir)
	if oerr != nil {
		ov = capsule.StepOverrides{}
	}
	if !scene.live {
		ov, _ = runtime.MergedStepOverrides(ov, d.rt.PendingStepOverrides(wi))
	}
	out := &protocol.ParamsView{WorkItem: wi, Posture: scene.posture, Steps: []protocol.ParamsStepView{}}
	for _, spec := range wf.Steps {
		out.Steps = append(out.Steps, d.paramsRow(spec.ID, spec.Params, scene.statusOf(spec.ID), below, ov))
	}
	return out, nil
}

// queuedOverrideView answers a queued work item's OVERLAY view (the
// GET /jobs/{wi}/overrides face, polyforge#79): the lane from this
// machine's set, the reserved step_params attrs key as the overlay layer
// (By=step_params), and the model sources below it. The models and skip
// aspects never ride the key — they are the claimed ticket's — so the
// rows carry no overlay of them (review r1#7: the unbranched path read
// the daemon's own current directory).
func (d *Daemon) queuedOverrideView(wi string, item *api.WorkItem, scene *overrideScene) (*protocol.OverrideView, error) {
	if scene.bundle == "" {
		return nil, &api.Error{Code: api.ErrDaemonConflict,
			Detail: fmt.Sprintf("%s names no bundle: its overlay is composed from its lane, and the lane is the bundle it will run", wi)}
	}
	lane := d.laneOf(item.ProjectName, scene.bundle)
	if lane == nil {
		return nil, &api.Error{Code: api.ErrDaemonConflict,
			Detail: fmt.Sprintf("the lane %s of %s does not load on this daemon — a queued work item's overlay is composed from its lane (the step declarations); it loads on a machine whose lane set has it", scene.bundle, item.ProjectName)}
	}
	cfg, _ := d.rt.LoadWorkspace(item.ProjectName)
	sources := workspace.StepModelSources(lane, cfg, item.ProjectName, nil)
	ov := capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{}}
	for step, params := range runtime.StepParamsOfAttrs(item.Attrs) {
		e := &capsule.StepOverride{Params: map[string]*capsule.ParamOverride{}}
		for name, entry := range params {
			e.Params[name] = &capsule.ParamOverride{Value: entry.Value, By: runtime.StepParamsClaimedBy}
		}
		ov.Steps[step] = e
	}
	out := &protocol.OverrideView{WorkItem: wi, Posture: scene.posture, Steps: []protocol.OverrideStepView{}}
	for _, s := range lane.Steps {
		row := protocol.OverrideStepView{ID: s.ID, Skippable: s.Skippable}
		if e := ov.Steps[s.ID]; e != nil {
			fillRow(&row, e)
		}
		if src, ok := sources[s.ID]; ok {
			row.Effective, row.Source = src.Models, src.Source
		}
		out.Steps = append(out.Steps, row)
	}
	return out, nil
}

// queuedParamsView composes a queued work item's face: this machine's
// lane set, the project's yaml and the reserved step_params attrs key.
func (d *Daemon) queuedParamsView(wi string, item *api.WorkItem, scene *overrideScene) (*protocol.ParamsView, error) {
	if scene.bundle == "" {
		return nil, &api.Error{Code: api.ErrDaemonConflict,
			Detail: fmt.Sprintf("%s names no bundle: a queued work item's params are composed from its lane, and the lane is the bundle it will run", wi)}
	}
	project := item.ProjectName
	lane := d.laneOf(project, scene.bundle)
	if lane == nil {
		return nil, &api.Error{Code: api.ErrDaemonConflict,
			Detail: fmt.Sprintf("the lane %s of %s does not load on this daemon — a queued work item's params need its lane (the step declarations); it loads on a machine whose lane set has it", scene.bundle, project)}
	}
	cfg, _ := d.rt.LoadWorkspace(project)
	below := workspace.StepParamSources(scene.bundle, lane, cfg, project)
	ticket := runtime.StepParamsOfAttrs(item.Attrs)
	ov := capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{}}
	for step, params := range ticket {
		e := &capsule.StepOverride{Params: map[string]*capsule.ParamOverride{}}
		for name, entry := range params {
			e.Params[name] = &capsule.ParamOverride{Value: entry.Value}
		}
		ov.Steps[step] = e
	}
	out := &protocol.ParamsView{WorkItem: wi, Posture: scene.posture, Steps: []protocol.ParamsStepView{}}
	for _, s := range lane.Steps {
		out.Steps = append(out.Steps, d.paramsRow(s.ID, s.Params, "", below, ov))
	}
	return out, nil
}

// paramsRow composes one step's row: every param the lane declares
// (names sorted, the lane default the floor), the layer below it, and
// the per-ticket record on top.
func (d *Daemon) paramsRow(step string, declared map[string]string, status string,
	below map[string]map[string]workspace.StepParamSource, ov capsule.StepOverrides) protocol.ParamsStepView {
	row := protocol.ParamsStepView{ID: step, Status: status, Params: []protocol.ParamFinalView{}}
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := protocol.ParamFinalView{Name: name, Value: declared[name]}
		if src, ok := below[step][name]; ok {
			p.Value, p.Source = src.Value, src.Source
		}
		if e := ov.Steps[step]; e != nil {
			if pv := e.Params[name]; pv != nil && !pv.Cleared {
				p.Value, p.Source = pv.Value, overrideSource
			}
		}
		row.Params = append(row.Params, p)
	}
	return row
}
