package daemon

// analysis_queue.go (polyforge#72, AC2): the pending-analysis queue and
// the scheduler behind POST /analysis/enqueue. `pf file create` (a
// human session's CLI, Pi Web included) asks THIS daemon to schedule one
// analysis task for a fresh execution ticket whose lane requires
// acceptance and whose body carries no `## 分析` section (ruling 2). When
// the task lane cannot take it — the Host's max_concurrent_tasks bound
// is full, this Cell's execution plane is disabled, or a connection
// rotation is draining (ruling 12) — the request is queued: a persisted
// FIFO per Cell (`<DataDir>/analysis-queue.json`, atomic write, a
// corrupt copy set aside like the submit queue's), replayed at start, so
// a restart loses nothing. Every Cell's dispatch tick retries its own
// queue — the Host slot is shared, so ANY Cell's queued analysis wakes
// within one tick of a slot freeing, in enqueue order. A ticket that
// went terminal, or gained an analysis section meanwhile, has its entry
// dropped at dispatch (ruling 12's cleanup).
//
// Reach: the UDS face only, behind refuseStepOrigin like every POST
// here — a step or task process tree is refused DAEMON-403; only a
// human session's `pf` asks for an analysis. The spawned task is an
// ordinary task-lane record: its Spec.AnalysisOf binds the ticket (u8's
// narrow write path is the only thing that reads it), its candidates
// come from steps.<project>.task.models (else default.models) and its
// budgets from the task lane's defaults (ruling 4).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// analysisQueueFile is the persisted queue's name under the Cell's data
// dir, beside the submit queue's file.
const analysisQueueFile = "analysis-queue.json"

// analysisEntry is one queued analysis request. Retries counts how often
// a restarted daemon re-queued an interrupted analysis of this ticket
// (ruling 13's cap of 2; u9 keeps it).
type analysisEntry struct {
	WorkItemID string `json:"work_item_id"`
	Project    string `json:"project"`
	EnqueuedAt string `json:"enqueued_at"`
	Retries    int    `json:"retries"`
	// RedoRequestID marks an explicit redo round (polyforge#233): the
	// request id its pending marker carries until the task's first write
	// replaces the token with the task id. A plain create/retry entry
	// carries none.
	RedoRequestID string `json:"redo_request_id,omitempty"`
}

// analysisQueue is the Cell's persisted pending-analysis FIFO.
type analysisQueue struct {
	mu      sync.Mutex
	path    string
	entries []analysisEntry
}

func newAnalysisQueue(path string) *analysisQueue {
	return &analysisQueue{path: path}
}

// load reads the previous run's queue file (missing = none; an
// unparsable copy is set aside as .corrupt and reported, never fatal to
// the start — the submit queue's convention).
func (q *analysisQueue) load() {
	raw, err := os.ReadFile(q.path)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "daemon: %s unreadable: %v\n", analysisQueueFile, err)
		}
		return
	}
	var st struct {
		Entries []analysisEntry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		_ = os.Rename(q.path, q.path+".corrupt")
		fmt.Fprintf(os.Stderr, "daemon: %s unparsable (set aside as .corrupt): %v\n", analysisQueueFile, err)
		return
	}
	q.mu.Lock()
	q.entries = st.Entries
	q.mu.Unlock()
}

// persistLocked rewrites the file atomically; q.mu held.
func (q *analysisQueue) persistLocked() error {
	blob, err := json.MarshalIndent(struct {
		Version int             `json:"version"`
		Entries []analysisEntry `json:"entries"`
	}{Version: 1, Entries: q.entries}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(q.path, blob, 0o600)
}

// add appends one entry (a duplicate work item never queues twice);
// persist failures are loud but never undo the in-memory queue.
func (q *analysisQueue) add(e analysisEntry) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, x := range q.entries {
		if x.WorkItemID == e.WorkItemID {
			return false
		}
	}
	q.entries = append(q.entries, e)
	if err := q.persistLocked(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: %s persist failed: %v\n", analysisQueueFile, err)
	}
	return true
}

// addRedo registers one redo request BEFORE the ticket CAS and answers
// its persist failure — unlike add, a request that cannot land refuses the
// round instead of leaving an unbacked marker on the ticket.
func (q *analysisQueue) addRedo(e analysisEntry) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, x := range q.entries {
		if x.WorkItemID == e.WorkItemID {
			return false, nil
		}
	}
	q.entries = append(q.entries, e)
	if err := q.persistLocked(); err != nil {
		q.entries = q.entries[:len(q.entries)-1]
		return false, err
	}
	return true, nil
}

// has answers whether a request for wi waits in the queue (a redo or a
// create/retry): an explicit redo must not stack behind another round.
func (q *analysisQueue) has(wi string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range q.entries {
		if e.WorkItemID == wi {
			return true
		}
	}
	return false
}

// dropRequest removes one work item's queued request (the redo route's
// rollback after a failed CAS). A requestID narrows it to that round.
func (q *analysisQueue) dropRequest(wi, requestID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := q.entries[:0]
	for _, e := range q.entries {
		if e.WorkItemID == wi && (requestID == "" || e.RedoRequestID == requestID) {
			continue
		}
		kept = append(kept, e)
	}
	q.entries = kept
	if err := q.persistLocked(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: %s persist failed (redo rollback): %v\n", analysisQueueFile, err)
	}
}

// removeLocked drops the head entry; q.mu held.
func (q *analysisQueue) removeLocked() {
	q.entries = q.entries[1:]
}

// dropHead removes the head entry and persists; reason rides only the
// log of a persist failure.
func (q *analysisQueue) dropHead(reason string) {
	q.mu.Lock()
	q.removeLocked()
	if err := q.persistLocked(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: %s persist failed (%s): %v\n", analysisQueueFile, reason, err)
	}
	q.mu.Unlock()
}

// head answers the oldest queued entry (nil = none).
func (q *analysisQueue) head() *analysisEntry {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.headLocked()
}

func (q *analysisQueue) headLocked() *analysisEntry {
	if len(q.entries) == 0 {
		return nil
	}
	return &q.entries[0]
}

// empty answers whether nothing is queued.
func (q *analysisQueue) empty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries) == 0
}

// analysisQueuePath answers this Cell's queue file path.
func (d *Daemon) analysisQueuePath() string {
	return filepath.Join(d.cfg.DataDir, analysisQueueFile)
}

// analysisNeeds judges one ticket's CURRENT record: an execution ticket
// (a bundle) on a lane that requires acceptance, whose body carries no
// analysis section, not terminal. A re-queued entry (retries > 0,
// polyforge#115) needs one also when the section carries the unconfirmed
// draft an interrupted analysis left behind — the resume its re-queue
// comment「已重新入队，接着已写的部分继续」promises; an owner-confirmed
// or hand-written analysis (a section without the unconfirmed marker)
// needs none, first dispatch included.
func (d *Daemon) analysisNeeds(item *api.WorkItem, retries int) bool {
	if item.BundleName == nil || strings.TrimSpace(*item.BundleName) == "" {
		return false
	}
	if item.Status == api.StatusWrapped || item.Status == api.StatusCanceled || item.Status == api.StatusFailed {
		return false
	}
	content := ""
	if item.Content != nil {
		content = *item.Content
	}
	// a re-queued entry resumes its own unconfirmed draft (AnalysisUnconfirmed
	// implies HasAnalysis: the marker is the section's text) — the same
	// judgment analysis_write.go and decisions.go make, nothing new invented
	needs := !workflow.HasAnalysis(content) || (retries > 0 && workflow.AnalysisUnconfirmed(content))
	return needs && d.laneOf(item.ProjectName, *item.BundleName).RequiresAcceptance()
}

// analysisPrompt answers the whole stdin one analysis candidate runs
// with: the prohibited-actions section, the embedded template, the
// daemon-attached component division of the ticket's repositories
// (polyforge#89 — one fallback line when it could not be computed),
// then the ticket body (whose `## 分析` section is the current draft —
// AC10: a candidate that takes over continues it).
func (d *Daemon) analysisPrompt(item *api.WorkItem) (string, error) {
	dir, err := d.taskWorkDir(item.ProjectName)
	if err != nil {
		return "", err
	}
	content := ""
	if item.Content != nil {
		content = *item.Content
	}
	p := harness.TaskProhibitedActions(dir) + "\n" + harness.ConciseRule() + "\n" + AnalysisPrompt
	if sec := d.analysisComponentsSection(item, dir); sec != "" {
		p += "\n\n---\n\n" + sec
	}
	return p + "\n\n---\n\n# 工单\n\n" + content, nil
}

// analysisPromptFor is task.Config.PromptFor: an analysis task's every
// candidate is prompted from the ticket as it stands NOW — re-read, so a
// candidate that takes over continues the draft its predecessor already
// wrote into `## 分析` (AC10). A re-read that fails falls back to the
// spawn-time prompt (already the whole stdin). Any other task answers ""
// (the manager's own prompt).
func (d *Daemon) analysisPromptFor(t task.Task) string {
	if t.AnalysisOf == "" {
		return ""
	}
	if item, err := d.hub.GetWorkItem(t.AnalysisOf); err == nil {
		if p, err := d.analysisPrompt(item); err == nil {
			return p
		}
	}
	return t.Prompt
}

// analysisSpawn starts one analysis task for the CURRENT record item
// (already read and judged by the caller). It answers the spawned task
// (the zero value with errNotNeeded = the ticket no longer needs one;
// the zero value with another err = why the caller may retry).
func (d *Daemon) analysisSpawn(item *api.WorkItem, retries int) (task.Task, error) {
	if !d.analysisNeeds(item, retries) {
		return task.Task{}, errNotNeeded
	}
	prompt, err := d.analysisPrompt(item)
	if err != nil {
		return task.Task{}, err
	}
	tk, err := d.tasks.Spawn(task.Spec{
		Project:         item.ProjectName,
		Prompt:          prompt,
		AnalysisOf:      item.ID,
		AnalysisRetries: retries,
	})
	if err != nil {
		return task.Task{}, err
	}
	return tk, nil
}

// errNotNeeded marks a dispatch-time drop (terminal, analyzed or unbound
// since the enqueue): the caller removes the entry, never logs an error.
var errNotNeeded = errors.New("the ticket needs no analysis anymore")

// analysisRetryable answers whether err is one of the reasons an
// analysis is queued rather than dropped: the task lane's bound, this
// Cell's disabled execution plane or a pending connection rotation
// (ruling 12) — all the daemon can retry on its own.
func analysisRetryable(err error) bool {
	if errors.Is(err, task.ErrLimit) {
		return true
	}
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == api.ErrDaemonConflict
}

// dispatchAnalysis drains the queue while the lane takes work: the head
// entry whose ticket still needs it spawns; one that does not is dropped;
// one the lane still cannot take stays queued and stops the walk. It
// runs on every Cell's dispatch tick, right after a task ended (the slot
// that freed is Host-wide) and at the enqueue route.
func (d *Daemon) dispatchAnalysis() {
	// One walk at a time: the tick, a task's end and the enqueue route
	// may arrive together; a second caller's head entry is already being
	// judged by the first.
	if !d.analysisDispatch.TryLock() {
		return
	}
	defer d.analysisDispatch.Unlock()
	// Never spawn on a shutting-down daemon: the terminal wave of
	// taskDone calls (the interrupted tasks of Close) must not schedule
	// new work — the restarted daemon replays the queue instead.
	if d.modes.ShuttingDown() {
		return
	}
	q := d.analysisQ
	swept := false
	for {
		head := q.head()
		if head == nil {
			return
		}
		// The lane gate comes before any hub read: a lane that cannot take
		// the entry (its slots full, the execution plane disabled, a
		// rotation draining) is judged locally, and its queue is read only
		// for the cleanup of ruling 12 — a terminal or analyzed ticket's
		// entry is dropped even while the lane is blocked — at most once
		// per DrainInterval, the period of the Cell's other hub polling
		// (never on every 200ms tick: review r1#4).
		blocked := d.analysisLaneBlocked()
		if blocked && !swept {
			now := time.Now()
			if now.Sub(d.analysisSweptAt) < d.cfg.DrainInterval {
				return
			}
			d.analysisSweptAt = now
			swept = true
		}
		// The ticket is re-read and re-judged; a hub that cannot answer
		// keeps the entry for the next walk, never drops it.
		item, err := d.hub.GetWorkItem(head.WorkItemID)
		if err != nil {
			// a ticket the daemon can never read (gone, or not this key's)
			// would block every entry behind it: dropped, loudly
			var ae *api.Error
			if errors.As(err, &ae) && (ae.Code == api.ErrCoreNotFound || ae.Code == api.ErrCoreForbidden) {
				fmt.Fprintf(os.Stderr, "daemon: analysis queue: %s cannot be read (%v) — entry dropped\n", head.WorkItemID, err)
				q.dropHead("the ticket cannot be read")
				continue
			}
			return
		}
		if !d.analysisNeeds(item, head.Retries) {
			q.dropHead("no longer needs an analysis")
			continue
		}
		if blocked {
			return // a live head the lane cannot take yet: it stays queued
		}
		_, err = d.analysisSpawn(item, head.Retries)
		if err == nil || errors.Is(err, errNotNeeded) {
			// dispatched — or the ticket changed between the two reads:
			// the head entry is gone either way
			q.dropHead("dispatched")
			continue
		}
		if analysisRetryable(err) {
			return // stays queued; the next tick (or slot) retries
		}
		// Anything else (an unmapped project, no task models declared)
		// will not fix itself on a retry: the entry is dropped loudly,
		// exactly once.
		fmt.Fprintf(os.Stderr, "daemon: analysis of %s dropped: %v\n", head.WorkItemID, err)
		q.dropHead("dropped: " + err.Error())
	}
}

// analysisLaneBlocked answers whether the task lane cannot take an
// analysis now — a full bound, a disabled execution plane or a pending
// rotation — judged locally, with no hub call. It is a cheap gate: the
// spawn's own refusals re-check under the manager's lock, so the gate may
// race a spawn — the next walk retries.
func (d *Daemon) analysisLaneBlocked() bool {
	slots := d.taskSlots()
	return slots.Running() >= slots.Max() || d.execDisabledRefusal() != nil || d.drainRefusal() != nil
}

// analysisRequeueCap is ruling 13's per-ticket cap: an interrupted
// analysis is re-queued at most twice; the third interrupt comments and
// stops.
const analysisRequeueCap = 2

// onAnalysisTaskTerminal is the analysis half of task.Config.OnTerminal
// (AC5): a stopped analysis never disappears silently — capped, failed
// and interrupted analyses leave ONE comment on the ticket naming the
// task id, the state and the draft's item count (the top-level 1–7
// numbers of the section as it stands); an interrupted one is re-queued
// (ruling 13), its retry count on the record so the restarts themselves
// survive. A completed analysis comments nothing — its gate is the
// unconfirmed marker (u10). Comment and re-queue failures are loud on
// stderr, never a failure of the task's own terminal record.
func (d *Daemon) onAnalysisTaskTerminal(t task.Task) {
	if t.Spec.AnalysisOf == "" || t.Spec.Synchronous {
		return
	}
	switch t.State {
	case task.StateCapped, task.StateFailed, task.StateInterrupted:
	default:
		return
	}
	state := "截停"
	cancelled := t.State == task.StateInterrupted && t.Error == task.ErrorCancelled
	if t.State == task.StateFailed {
		state = "失败"
	} else if cancelled {
		state = "被取消（`pf task cancel`）"
	} else if t.State == task.StateInterrupted {
		state = "被中断"
	}
	items := 0
	if item, err := d.hub.GetWorkItem(t.Spec.AnalysisOf); err == nil && item.Content != nil {
		items = workflow.AnalysisItemCount(*item.Content)
	}
	body := fmt.Sprintf("分析任务 %s %s：已写到第 %d 项（按 1–7 编号），已写部分保留在 `## 分析`。", t.ID, state, items)
	if cancelled {
		body += "不自动重派；需要时请重新安排或手动分析并写入。"
	} else if t.State == task.StateInterrupted {
		n := t.Spec.AnalysisRetries + 1
		if n <= analysisRequeueCap {
			d.analysisQ.add(analysisEntry{
				WorkItemID: t.Spec.AnalysisOf,
				Project:    t.Spec.Project,
				EnqueuedAt: time.Now().UTC().Format(time.RFC3339Nano),
				Retries:    n,
			})
			body += fmt.Sprintf("已重新入队，接着已写的部分继续（第 %d/%d 次重试）。", n, analysisRequeueCap)
		} else {
			body += "重试已达上限（2 次），不再自动重派；需要时请手动分析并写入。"
		}
	}
	if err := d.hub.Do("POST", "/api/v1/work-items/"+t.Spec.AnalysisOf+"/comments",
		api.AddCommentRequest{Body: body}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: analysis %s of %s: the failure comment failed: %v\n", t.ID, t.Spec.AnalysisOf, err)
	}
}

// analysisEnqueueRoute serves POST /analysis/enqueue
// {work_item_id, project}. The ticket is re-read and re-judged here (the
// CLI decided on its own read; this is the daemon's own gate): one that
// needs no analysis answers state not_needed. Otherwise the request is
// scheduled at once when the lane takes it, or queued (persisted) when
// only the bound, the disabled plane or a pending rotation hold it. Any
// other failure answers state failed with the reason — the CLI prints
// 「未安排分析」 (AC1).
func (d *Daemon) analysisEnqueueRoute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkItemID string `json:"work_item_id"`
		Project    string `json:"project"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.WorkItemID) == "" {
		writeErr(w, api.ErrDaemonBadInput, "work_item_id is required")
		return
	}
	item, err := d.hub.GetWorkItem(req.WorkItemID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"state": "failed", "detail": err.Error()})
		return
	}
	if !d.analysisNeeds(item, 0) {
		writeJSON(w, http.StatusOK, map[string]any{"state": "not_needed"})
		return
	}
	// entries already wait: the request queues behind them (AC2's enqueue
	// order), and the walk dispatches what the lane takes now
	if !d.analysisQ.empty() {
		d.analysisQ.add(analysisEntry{
			WorkItemID: req.WorkItemID,
			Project:    req.Project,
			EnqueuedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
		d.dispatchAnalysis()
		writeJSON(w, http.StatusOK, map[string]any{"state": "queued"})
		return
	}
	tk, err := d.analysisSpawn(item, 0)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"state": "scheduled", "task_id": tk.ID})
		return
	}
	if analysisRetryable(err) {
		d.analysisQ.add(analysisEntry{
			WorkItemID: req.WorkItemID,
			Project:    req.Project,
			EnqueuedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
		writeJSON(w, http.StatusOK, map[string]any{"state": "queued"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": "failed", "detail": err.Error()})
}
