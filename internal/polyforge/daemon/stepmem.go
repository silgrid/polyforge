package daemon

// stepmem.go: the step agent's memory face behind `pf step recall`,
// `pf step memory show` and `pf step remember` (stepcall.go serves each as
// a /step/call verb through the forwarders below). A step agent holds no
// aihub credential path: the daemon forwards them with its own key. The
// work item — and with it the project — comes from the caller's process
// tree (peerStepOrigin), never from the request or the caller's env.
//
// Reach: a step agent cannot reach the
// read-only web console or the browse mode. The daemon proxies no /web/
// route at all (routes.go registers none), a step process holds no aihub
// key so it cannot authenticate there either, and the search forwarder
// below forwards only q/type/tags/limit/match plus the job's project —
// a cursor or a sort value a caller may send is dropped on the floor and
// the empty-q browse shape is refused before anything is forwarded.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/stepmem"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// stepJob is the work item and dispatch a step caller runs under.
type stepJob struct {
	wi, slug, project, step string
	attempt                 int
	capsuleDir              string
}

// stepCaller resolves the request to its step's live job, or writes the
// refusal and answers false.
func (d *Daemon) stepCaller(w http.ResponseWriter, r *http.Request) (stepJob, bool) {
	origin := d.stepOriginOf
	if origin == nil {
		origin = d.peerStepOrigin
	}
	j, reason := origin(r)
	if reason != "" {
		writeErr(w, api.ErrDaemonForbidden, r.URL.Path+": "+reason)
		return j, false
	}
	ji, live := d.rt.Info(d.canonWI(j.wi))
	if !live || ji.Project == "" {
		writeErr(w, api.ErrDaemonNotFound, "work item "+j.wi+" of the calling step is not running on this daemon")
		return j, false
	}
	j.wi, j.slug, j.project, j.capsuleDir = ji.WorkItem, ji.Slug, ji.Project, ji.CapsuleDir
	return j, true
}

// peerStepOrigin walks the UDS peer's ancestry (and process groups) to the
// step processes THIS daemon spawned — a live job's spawn-journal pid that
// is still the journaled process (live, start time = the journaled
// started_at); the newest match names the step and attempt. The step
// process's environment is not read: darwin omits an entitled agent's
// (polyforge#234). Exactly one job may match, and the caller's own
// PF_WI_ID must name it (any process can set the variable; it never
// selects the job).
func (d *Daemon) peerStepOrigin(r *http.Request) (stepJob, string) {
	const notStep = "serves step agents only (the caller is no step process of this daemon)"
	cred, ok := requestPeerCred(r.Context())
	if !ok {
		return stepJob{}, notStep
	}
	claimed, ok := runtime.StepProcessOrigin(cred.pid, d.cfg.MachineID)
	if !ok {
		return stepJob{}, notStep
	}
	chain := runtime.AncestryOf(cred.pid)
	var matches []stepJob
	for _, ji := range d.rt.List() {
		entries, _ := capsule.ReadSpawnJournal(ji.CapsuleDir)
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			if e.PID <= 1 || !chain[e.PID] {
				continue
			}
			if runtime.JournaledProcessLive(e.PID, e.StartedAt) {
				matches = append(matches, stepJob{wi: ji.WorkItem, step: e.StepID, attempt: e.SpawnN})
				break
			}
		}
	}
	switch {
	case len(matches) == 0:
		return stepJob{}, notStep + " (no live step process of this daemon is among its ancestors)"
	case len(matches) > 1:
		return stepJob{}, "refused: the caller's process tree reaches the step processes of several work items"
	case claimed != matches[0].wi:
		return stepJob{}, fmt.Sprintf("refused: the caller descends from the step process of %s but claims PF_WI_ID=%s", matches[0].wi, claimed)
	}
	return matches[0], ""
}

// forwardMemorySearch forwards one search scoped to project; who names
// the caller in the refusal of a foreign-project ask. The forwarded
// shape is fixed: the job's project, q, and — when present —
// type/tags/limit/match, plus the excerpt size. Nothing else rides: a
// browse-shaped request (a paging cursor or a sort value) is answered
// as a keyword search of the q it carries, and an empty q is refused —
// a step agent never reaches aihub's browse mode.
func (d *Daemon) forwardMemorySearch(w http.ResponseWriter, r *http.Request, project, who string) {
	d.forwardMemorySearchQuery(w, r.URL.Query(), project, who)
}

// forwardMemorySearchQuery is forwardMemorySearch over the query q.
func (d *Daemon) forwardMemorySearchQuery(w http.ResponseWriter, q url.Values, project, who string) {
	if p := q.Get("project"); p != "" && p != project {
		writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("%s reads project %q only (asked for %q)", who, project, p))
		return
	}
	if strings.TrimSpace(q.Get("q")) == "" {
		writeErr(w, api.ErrDaemonBadInput, "a query (q) is required")
		return
	}
	v := url.Values{"project": {project}, "q": {q.Get("q")}, "excerpt": {"120"}}
	for _, k := range []string{"type", "tags", "limit", "match"} {
		if s := q.Get(k); s != "" {
			v.Set(k, s)
		}
	}
	var raw json.RawMessage
	if err := d.hub.Do("GET", "/api/v1/memories/search?"+v.Encode(), nil, &raw); err != nil {
		writeErrOf(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// forwardMemoryRead answers one memory of project (MEM-404 for any other).
func (d *Daemon) forwardMemoryRead(w http.ResponseWriter, id, project string) {
	fail := func(err error) {
		var ae *api.Error
		if errors.As(err, &ae) && ae.HTTPStatus() < 500 {
			writeErr(w, api.ErrMemNotFound, "memory "+id+" not found in project "+project)
			return
		}
		writeErrOf(w, err)
	}
	var meta json.RawMessage
	var entry, proj struct {
		ID        string `json:"id"`
		ProjectID string `json:"project_id"`
	}
	if err := d.hub.Do("GET", "/api/v1/memories/"+url.PathEscape(id), nil, &meta); err != nil {
		fail(err)
		return
	}
	_ = json.Unmarshal(meta, &entry)
	if err := d.hub.Do("GET", "/api/v1/projects/"+url.PathEscape(project), nil, &proj); err != nil {
		writeErrOf(w, err)
		return
	}
	cr, ok := d.hub.(interface{ GetMemoryContent(string) ([]byte, error) })
	if entry.ProjectID == "" || entry.ProjectID != proj.ID || !ok {
		fail(&api.Error{Code: api.ErrMemNotFound})
		return
	}
	body, err := cr.GetMemoryContent(id)
	if err != nil {
		fail(err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"meta": meta, "content": string(body)})
}

// stepRememberRequest is a step's memory write.
type stepRememberRequest struct {
	Type, Title, Content, Project string
	Tags                          []string
	WorkItem                      string `json:"work_item"`
}

// stepRememberAs stores req as the step j's summary (stepRememberRoute),
// stamped with the calling dispatch's writer identity: kind "step" and
// the work item / step / attempt / model of the dispatch the caller runs
// under (stepWriterModel) — the caller cannot self-report a writer (its
// args are decoded strictly and carry none of the fields).
func (d *Daemon) stepRememberAs(w http.ResponseWriter, j stepJob, req stepRememberRequest) {
	switch {
	case req.Project != "" && req.Project != j.project:
		writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("a step of %s writes to project %q only (got %q)", j.wi, j.project, req.Project))
		return
	case req.WorkItem != "" && req.WorkItem != j.wi && req.WorkItem != j.slug:
		writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("a step memory is attributed to its own work item %s (got %q)", j.wi, req.WorkItem))
		return
	}
	why := stepmem.Check(req.Type, req.Title, req.Tags, req.Content)
	if why == "" && d.cfg.APIKey != "" && strings.Contains(req.Title+req.Content, d.cfg.APIKey) {
		why = "the memory carries the daemon's credential value"
	}
	if why != "" {
		writeErr(w, api.ErrMemBadInput, why)
		return
	}
	model, trusted := stepWriterModel(readExecutionFile(filepath.Join(j.capsuleDir, capsule.ExecutionJSON)), j.step, j.attempt)
	if !trusted {
		writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf(
			"a step memory is stamped with its own dispatch's model, and work item %s's execution record does not name the calling dispatch (step %q, attempt %d) as its current one; nothing was stored", j.wi, j.step, j.attempt))
		return
	}
	// Q4's 可空约定: a trusted dispatch whose candidate is empty stores a
	// null model, never an invented one — the null is what the show face
	// and the tables render as the missing value.
	var modelField any
	if model != "" {
		modelField = model
	}
	body := map[string]any{
		"project": j.project, "work_item_id": j.wi, "type": req.Type, "visibility": "project",
		"title": strings.TrimSpace(req.Title), "content": req.Content, "tags": req.Tags,
		"idempotency_key": stepmem.IdempotencyKey(j.wi, j.step, j.attempt),
		"writer_kind":     "step",
		"writer_details":  map[string]any{"work_item": j.wi, "step": j.step, "attempt": j.attempt, "model": modelField},
	}
	var stored json.RawMessage
	if err := d.hub.Do("POST", "/api/v1/memories", body, &stored); err != nil {
		var ae *api.Error
		if !errors.As(err, &ae) {
			err = &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: "aihub unreachable, the memory was not stored: " + err.Error()}
		}
		writeErrOf(w, err)
		return
	}
	// aihub replays the key's first memory without comparing bodies: a
	// different body is a second summary, refused rather than dropped
	var m struct{ ID string }
	cr, ok := d.hub.(interface{ GetMemoryContent(string) ([]byte, error) })
	if json.Unmarshal(stored, &m) == nil && m.ID != "" && ok {
		if prev, err := cr.GetMemoryContent(m.ID); err == nil && strings.TrimSpace(string(prev)) != strings.TrimSpace(req.Content) {
			writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("this step already stored its summary %s; summaries are not edited — put corrections into the one call", m.ID))
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(stored)
}

// stepWriterModel answers the model of the calling dispatch from the
// capsule's execution record. The record's own step_start wrote the
// candidate BEFORE the dispatch's agent ran and cleared the ordinal
// when the dispatch settled, so a record that still names the caller's
// spawn (Dispatch == attempt) on an in-progress step proves the model
// of THIS dispatch: a mid-flight model switch rewrites the candidate
// only with the death's archive entry, after the ordinal cleared, so an
// old attempt is never answered the next dispatch's model (the owner's
// Q4 ruling). trusted false — the record absent or torn, the step
// missing or settled, the ordinal naming another dispatch — refuses
// the write; trusted with an empty candidate stores a null model (Q4's
// 可空约定), never an invented one.
func stepWriterModel(ex *capsule.Execution, step string, attempt int) (model string, trusted bool) {
	if ex == nil {
		return "", false
	}
	st := ex.Steps[step]
	if st == nil || st.Status != capsule.StepInProgress || st.Dispatch != int64(attempt) {
		return "", false
	}
	return st.Candidate, true
}
