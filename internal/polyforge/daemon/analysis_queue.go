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

// bumpRequeue raises wi's entry's retry count (ruling 13's cap: at most
// `cap` re-queues per ticket); it answers whether the cap still allows
// the re-queue (the entry stays in the queue either way — the caller
// drops it when the answer is false).
func (q *analysisQueue) bumpRequeue(wi string, cap int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.entries {
		if q.entries[i].WorkItemID == wi {
			q.entries[i].Retries++
			return q.entries[i].Retries <= cap
		}
	}
	return false
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
// analysis section, not terminal.
func (d *Daemon) analysisNeeds(item *api.WorkItem) bool {
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
	return !workflow.HasAnalysis(content) && d.laneOf(item.ProjectName, *item.BundleName).RequiresAcceptance()
}

// analysisPrompt answers the whole stdin one analysis candidate runs
// with: the prohibited-actions section, the embedded template, then the
// ticket body (whose `## 分析` section is the current draft — AC10: a
// candidate that takes over continues it).
func (d *Daemon) analysisPrompt(item *api.WorkItem) (string, error) {
	dir, err := d.taskWorkDir(item.ProjectName)
	if err != nil {
		return "", err
	}
	content := ""
	if item.Content != nil {
		content = *item.Content
	}
	return harness.TaskProhibitedActions(dir) + "\n" + AnalysisPrompt + "\n\n---\n\n# 工单\n\n" + content, nil
}

// analysisSpawn starts one analysis task for the CURRENT record item
// (already read and judged by the caller). It answers the spawned task
// (the zero value with errNotNeeded = the ticket no longer needs one;
// the zero value with another err = why the caller may retry).
func (d *Daemon) analysisSpawn(item *api.WorkItem) (task.Task, error) {
	if !d.analysisNeeds(item) {
		return task.Task{}, errNotNeeded
	}
	prompt, err := d.analysisPrompt(item)
	if err != nil {
		return task.Task{}, err
	}
	tk, err := d.tasks.Spawn(task.Spec{
		Project:    item.ProjectName,
		Prompt:     prompt,
		AnalysisOf: item.ID,
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
	for {
		head := q.head()
		if head == nil {
			return
		}
		// The ticket is re-read and re-judged FIRST — a terminal or
		// analyzed ticket's entry is dropped even while the lane is full
		// (ruling 12's cleanup); a hub that cannot answer keeps the entry
		// for the next tick, never drops it.
		item, err := d.hub.GetWorkItem(head.WorkItemID)
		if err != nil {
			return
		}
		if !d.analysisNeeds(item) {
			q.dropHead("no longer needs an analysis")
			continue
		}
		// The cheap lane gate: a full bound must not attempt a spawn on
		// every tick (the spawn's own refusal re-checks under the
		// manager's lock, so the gate may race a spawn — the next tick
		// retries).
		slots := d.taskSlots()
		if slots.Running() >= slots.Max() {
			return
		}
		_, err = d.analysisSpawn(item)
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
	if !d.analysisNeeds(item) {
		writeJSON(w, http.StatusOK, map[string]any{"state": "not_needed"})
		return
	}
	tk, err := d.analysisSpawn(item)
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
