package daemon

// analysis_write.go (polyforge#72, u8 — AC3/AC4): the narrow write path —
// the ONE write a task agent may make. POST /task/analysis-write takes the
// running analysis task's CURRENT DRAFT and replaces its ticket's
// `## 分析` section with it (ruling 1/10: the task writes as it analyzes, a
// stop keeps what is written). The binding is triple-checked the way
// ruling 6 fixes it: the caller's process tree carries the task's
// PF_TASK_ID (runtime.TaskProcessOrigin, the same read the memory face
// uses), the task is running on THIS daemon, and its record names the
// ticket (Spec.AnalysisOf) — the request never names a ticket, so a task
// cannot write another one and cannot write another section. The section
// someone else already wrote (an owner's 「免分析」, no auto-analysis
// marker) is refused with the ticket untouched (AC4); the write itself
// carries the aihub content CAS and retries on a stale version,
// re-reading and re-replacing only its section, so an owner edit
// elsewhere survives (AC4). The unconfirmed marker (ruling 9) rides the
// head of every write: the auto-pull wave skips the ticket until the
// owner confirms (`pf file analysis confirm`, u10).
//
// Reach: the UDS face only; the task-origin guard admits a task peer on
// exactly this path (taskOriginRefusal's one exception, routes.go) — a
// step process tree is still refused there, and any other caller (a human
// session included) is refused here: the route serves a running analysis
// task of this daemon only. This is an accident guard, not a boundary
// (the task-trust ruling, mem_TdI0isaGmTgG): a local process may set
// PF_TASK_ID and pose as the running task — documented in
// docs/reference/uds-routes.md.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// taskAnalysisWritePath is the narrow write path's route.
const taskAnalysisWritePath = "/task/analysis-write"

func (d *Daemon) taskAnalysisWriteRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := d.taskPeer(r)
	if !ok {
		writeErr(w, api.ErrDaemonForbidden, "POST /task/analysis-write serves task agents only — the caller is no task process of this daemon")
		return
	}
	tk, err := d.tasks.Show(id)
	if err != nil || tk.State != task.StateRunning || tk.Spec.AnalysisOf == "" {
		writeErr(w, api.ErrDaemonForbidden, "task "+id+" is not a running analysis task of this daemon — analysis-write serves a running analysis task, and only its own ticket")
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeErr(w, api.ErrDaemonBadInput, "analysis-write: the draft content is required (the CLI's --content takes text, @file or -)")
		return
	}
	if err := d.writeAnalysisSection(tk.ID, tk.Spec.AnalysisOf, req.Content); err != nil {
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": "written"})
}

// analysisWriteAttempts bounds the CAS retry (a pathologically
// contended ticket answers the third refusal to the task, which retries
// its own write on its next draft).
const analysisWriteAttempts = 3

// writeAnalysisSection replaces wi's analysis section with the task's
// stamped draft. The refusal that keeps someone else's section is
// judged on every (re-)read; a CONTENT_STALE answer re-reads and
// re-replaces only the section, so concurrent edits elsewhere survive.
func (d *Daemon) writeAnalysisSection(taskID, wi, draft string) error {
	marker := workflow.AutoAnalysisMarker(taskID)
	var lastErr error
	for attempt := 0; attempt < analysisWriteAttempts; attempt++ {
		item, err := d.hub.GetWorkItem(wi)
		if err != nil {
			return err
		}
		content := ""
		if item.Content != nil {
			content = *item.Content
		}
		if workflow.HasAnalysis(content) && !workflow.AnalysisUnconfirmed(content) {
			return &api.Error{Code: api.ErrDaemonConflict, Detail: "the analysis section of " + wi + " was already written by someone else (it carries no auto-analysis marker): the task's write is refused and the ticket is unchanged"}
		}
		newContent, _ := workflow.ReplaceAnalysisSection(content, marker+"\n\n"+draft)
		err = d.hub.Do("PATCH", "/api/v1/work-items/"+item.ID,
			map[string]any{"content": newContent, "expected_updated_at": item.UpdatedAt}, nil)
		if err == nil {
			return nil
		}
		lastErr = err
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != api.ErrCoreConflict {
			return err
		}
		// stale: another writer moved the ticket between the read and
		// the write — re-read, re-replace the section, try again
	}
	return lastErr
}
