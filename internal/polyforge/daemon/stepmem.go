package daemon

// stepmem.go: the step agent's memory face. A step agent holds no aihub
// credential path: inside a step `pf recall`, `pf memory show` and `pf
// remember` come here and the daemon forwards them with its own key. The
// work item — and with it the project — comes from the caller's process
// tree (peerStepOrigin), never from the request or the caller's env.
//
//	GET  /step/memories/search   ?q&type&tags&limit&match[&project]
//	GET  /step/memories/{id}     {meta, content}
//	POST /step/memories          {type, title, tags, content[, project, work_item]}
//
// The POST is one of the step-origin guard's two step-allowed writes
// (routes.go); the other is the result submit (stepresult.go).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/stepmem"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

const stepMemoriesPath = "/step/memories"

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

// stepSearchRoute forwards one search scoped to the caller's project.
func (d *Daemon) stepSearchRoute(w http.ResponseWriter, r *http.Request) {
	j, ok := d.stepCaller(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if p := q.Get("project"); p != "" && p != j.project {
		writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("a step of %s reads project %q only (asked for %q)", j.wi, j.project, p))
		return
	}
	if strings.TrimSpace(q.Get("q")) == "" {
		writeErr(w, api.ErrDaemonBadInput, "a query (q) is required")
		return
	}
	v := url.Values{"project": {j.project}, "q": {q.Get("q")}, "excerpt": {"120"}}
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

// stepMemoryRoute reads one memory of the caller's project; any other id
// answers MEM-404 whatever aihub said.
func (d *Daemon) stepMemoryRoute(w http.ResponseWriter, r *http.Request) {
	j, ok := d.stepCaller(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	fail := func(err error) {
		var ae *api.Error
		if errors.As(err, &ae) && ae.HTTPStatus() < 500 {
			writeErr(w, api.ErrMemNotFound, "memory "+id+" not found in project "+j.project)
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
	if err := d.hub.Do("GET", "/api/v1/projects/"+url.PathEscape(j.project), nil, &proj); err != nil {
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

// stepRememberRoute stores the calling step's experience.wrap summary:
// checked (stepmem.Check plus the daemon's own key), attributed to the
// resolved job, created with the step dispatch's idempotency key so a
// retry after a lost answer returns the memory already stored.
func (d *Daemon) stepRememberRoute(w http.ResponseWriter, r *http.Request) {
	j, ok := d.stepCaller(w, r)
	if !ok {
		return
	}
	var req struct {
		Type, Title, Content, Project string
		Tags                          []string
		WorkItem                      string `json:"work_item"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
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
	body := map[string]any{
		"project": j.project, "work_item_id": j.wi, "type": req.Type, "visibility": "project",
		"title": strings.TrimSpace(req.Title), "content": req.Content, "tags": req.Tags,
		"idempotency_key": stepmem.IdempotencyKey(j.wi, j.step, j.attempt),
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
