package daemon

// tasks.go: the one-shot task agent's daemon face (carried
// from #40).
//
//	POST /task/spawn          {project, prompt, max_duration?, max_tokens?, origin_chat?, caller_id?}
//	GET  /task/list
//	GET  /task/show/{id}
//	POST /task/cancel/{id}
//	GET  /task/memories/search   (task peers only; scoped to the task's project)
//	GET  /task/memories/{id}     (task peers only; scoped to the task's project)
//
// Reach (,): a task agent cannot
// reach the read-only web console or the browse mode — browse is the human
// direct-aihub `pf recall` (no query), which also refuses it locally inside
// a task or step. The daemon proxies no /web/
// route at all (routes.go registers none), a task process holds no
// aihub key so it cannot authenticate there either, and the memory search both faces
// share (forwardMemorySearch) forwards only q/type/tags/limit/match plus
// the task's project — a cursor or a sort value a caller may send is
// dropped on the floor and the empty-q browse shape is refused before
// anything is forwarded.
//
// Reachability (AGENTS.md: every new entry point states it;
// docs/reference/uds-routes.md carries the table):
//
//   - the UDS face only — no aihub route and no cross-machine face;
//   - the spawn and cancel POSTs sit behind refuseStepOrigin, so a STEP
//     process tree AND a TASK process tree are refused DAEMON-403 (no
//     recursion, no lifecycle side channel). Only a human session may
//     spawn or cancel;
//   - /task/list, /task/show and the two /task/memories GETs are reads. Any
//     local UDS peer (a step or task agent included) may call list/show,
//     exactly like GET /status (the list's rows carry only a bounded
//     first-line prompt_summary, the reporter snapshot and a bounded
//     last-line report_tail — no full prompt, no full report — and show
//     the full record;,); the two
//     memory reads answer a task process tree only, and the route itself
//     scopes them to the caller's own running task's project.
//
// Reporter: the spawn route resolves the request's
// self-asserted caller_id once, before the record is saved, through the
// aihub member face with include_caller=true under the daemon's own key.
// That read is internal to the route — no member route is proxied to any
// UDS peer, and a step or task agent holds no aihub key — and its failure
// never fails the spawn (the snapshot is then empty, read as `machine`).
// The read is bounded by reporterResolveTimeout: a slow or hung hub is that
// same failure, reached in seconds rather than the client's global timeout.
//
// The task manager is assembled here; the runner IS the real harness
// (Config.TaskRunner is the test seam). There is no OS-level confinement:
// a task agent's filesystem/network trust equals a session agent's (owner
// ruling 2026-10-26). The one capability gate is the write-verb guard the
// spawn env arms (PF_TASK_ID + PF_DAEMON_ID).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

const (
	taskMemSearchPath = "/task/memories/search"
	taskMemPrefix     = "/task/memories/"
)

// newTaskManager assembles the daemon's task manager: live candidate
// resolution, the project's workspace as the runner's working directory,
// and the exec runner (Config.TaskRunner swaps it for tests).
func (d *Daemon) newTaskManager(rtCfg *runtime.Config) *task.Manager {
	runner := d.cfg.TaskRunner
	if runner == nil {
		runner = &task.ExecRunner{
			PiBin:           d.cfg.PiBin,
			ClaudeBin:       d.live.ClaudeBin,
			ClaudeConfigDir: rtCfg.ClaudeConfigDir(),
			PiAgentDir:      rtCfg.StepAgentDir(),
			DaemonID:        d.cfg.MachineID,
			SocketPath:      d.cfg.SocketPath,
		}
	}
	return task.NewManager(task.Config{
		Store:   task.NewStore(d.cfg.DataDir),
		Runner:  runner,
		Resolve: func(project string) ([]string, string, error) { return d.rt.TaskModels(project) },
		WorkDir: d.taskWorkDir,
	})
}

// taskWorkDir answers the directory one task's harness runs in: the
// project's own workspace. A project that is not mapped on this daemon is
// refused with the typed unmapped-project error (the same refusal the
// claim path makes); nothing is widened to another workspace.
func (d *Daemon) taskWorkDir(project string) (string, error) {
	ws, err := d.plan().WorkspaceFor(project)
	if err != nil {
		return "", &api.Error{Code: api.ErrDaemonUnmappedProject, Detail: fmt.Sprintf(
			"project %q is not mapped on this daemon — task spawn takes a mapped project name, never a path (`pf doctor` checks the mapping)", project)}
	}
	return ws, nil
}

// taskErr maps a manager error to its typed wire answer.
func taskErr(w http.ResponseWriter, err error) {
	var ae *api.Error
	switch {
	case errors.As(err, &ae):
		writeErrOf(w, err)
	case errors.Is(err, task.ErrLimit):
		writeErr(w, api.ErrDaemonConflict, err.Error())
	case errors.Is(err, task.ErrNotFound):
		writeErr(w, api.ErrDaemonNotFound, err.Error())
	case errors.Is(err, task.ErrNotRunning):
		writeErr(w, api.ErrDaemonConflict, err.Error())
	default:
		writeErr(w, api.ErrDaemonBadInput, err.Error())
	}
}

func (d *Daemon) taskSpawnRoute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Project     string `json:"project"`
		Prompt      string `json:"prompt"`
		MaxDuration string `json:"max_duration"`
		MaxTokens   int64  `json:"max_tokens"`
		OriginChat  string `json:"origin_chat"`
		CallerID    string `json:"caller_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	spec := task.Spec{Project: req.Project, Prompt: req.Prompt, OriginChat: req.OriginChat, MaxTokens: req.MaxTokens,
		CallerID: req.CallerID}
	if req.MaxDuration != "" {
		dur, err := task.ParseDuration(req.MaxDuration)
		if err != nil {
			writeErr(w, api.ErrDaemonBadInput, err.Error())
			return
		}
		spec.MaxDuration = dur
	}
	spec.Reporter = d.resolveReporter(r.Context(), spec.Project, spec.CallerID)
	t, err := d.tasks.Spawn(spec)
	if err != nil {
		taskErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, withoutReporterFacts(t))
}

// reporterResolveTimeout bounds the spawn-time member read (the same
// short-probe bound ensure.go gives /status).
const reporterResolveTimeout = 3 * time.Second

// resolveReporter answers the display name of callerID as a human member
// of project, read once at spawn within reporterResolveTimeout of ctx;
// "" (read as task.ReporterMachine) for no id, no match, a blank name or
// any hub failure, the bound expiring included — the spawn never fails on
// it.
func (d *Daemon) resolveReporter(ctx context.Context, project, callerID string) string {
	if callerID == "" || d.hub == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, reporterResolveTimeout)
	defer cancel()
	contacts, err := d.hub.ProjectContactsWithCaller(ctx, project)
	if err != nil {
		return ""
	}
	for _, c := range contacts {
		if c.ID == callerID && strings.TrimSpace(c.DisplayName) != "" {
			return c.DisplayName
		}
	}
	return ""
}

// withoutReporterFacts clears the record's internal caller id and reporter
// snapshot: spawn and show answer the task document unchanged, and the
// list carries the reporter only as its own row field.
func withoutReporterFacts(t task.Task) task.Task {
	t.CallerID, t.Reporter = "", ""
	return t
}

func (d *Daemon) taskListRoute(w http.ResponseWriter, r *http.Request) {
	ts, err := d.tasks.List()
	if err != nil {
		taskErr(w, err)
		return
	}
	// The list carries no report bodies or full prompts (pf task show
	// reads one); each row's only prompt text is its bounded first-line
	// summary and its only report text the bounded last
	// line of a terminal task. reporter is the stored
	// spawn-time snapshot — the list never consults aihub.
	rows := make([]taskListRow, len(ts))
	for i := range ts {
		rows[i] = taskListRow{Task: ts[i], PromptSummary: task.PromptSummary(ts[i].Prompt),
			Reporter: ts[i].Reporter}
		if rows[i].Reporter == "" {
			rows[i].Reporter = task.ReporterMachine
		}
		if ts[i].State != task.StateRunning {
			rows[i].ReportTail = task.ReportTail(ts[i].Report)
		}
		rows[i].Task = withoutReporterFacts(rows[i].Task)
		rows[i].Report = ""
		rows[i].Prompt = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": rows, "now": time.Now().UTC()})
}

// taskListRow is one GET /task/list row: the task record (report and
// prompt cleared) plus the prompt's bounded summary, the reporter (a
// human member's display name or task.ReporterMachine) and the report's
// bounded last line ("" while running or for an empty report).
type taskListRow struct {
	task.Task
	PromptSummary string `json:"prompt_summary"`
	Reporter      string `json:"reporter"`
	ReportTail    string `json:"report_tail"`
}

func (d *Daemon) taskShowRoute(w http.ResponseWriter, r *http.Request) {
	t, err := d.tasks.Show(r.PathValue("id"))
	if err != nil {
		taskErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, withoutReporterFacts(t))
}

func (d *Daemon) taskCancelRoute(w http.ResponseWriter, r *http.Request) {
	if err := d.tasks.Cancel(r.PathValue("id")); err != nil {
		taskErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "cancelling": true})
}

// taskPeer answers the task whose process tree the UDS peer belongs to
// ("" = not a task process of this daemon).
func (d *Daemon) taskPeer(r *http.Request) (string, bool) {
	cred, ok := requestPeerCred(r.Context())
	if !ok {
		return "", false
	}
	return runtime.TaskProcessOrigin(cred.pid, d.cfg.MachineID)
}

// taskProject resolves a /task/memories caller to its running task's
// project, or writes the refusal and answers false.
func (d *Daemon) taskProject(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := d.taskPeer(r)
	if !ok {
		writeErr(w, api.ErrDaemonForbidden, r.URL.Path+": serves task agents only (the caller is no task process of this daemon)")
		return "", false
	}
	t, err := d.tasks.Show(id)
	if err != nil || t.State != task.StateRunning {
		writeErr(w, api.ErrDaemonNotFound, "task "+id+" of the caller is not running on this daemon")
		return "", false
	}
	return t.Project, true
}

func (d *Daemon) taskSearchRoute(w http.ResponseWriter, r *http.Request) {
	project, ok := d.taskProject(w, r)
	if !ok {
		return
	}
	d.forwardMemorySearch(w, r, project, "a task of "+project)
}

func (d *Daemon) taskMemoryRoute(w http.ResponseWriter, r *http.Request) {
	project, ok := d.taskProject(w, r)
	if !ok {
		return
	}
	d.forwardMemoryRead(w, r.PathValue("id"), project)
}
