package daemon

// stepbarrun.go: the deliver step's bar-run record entry.
// Inside a deliver step `pf bar-run -- '<carried test_bar line>'` runs the
// command in the repo it was carried for and posts what happened here; the
// daemon resolves the calling step from its process tree, refuses every
// step but `deliver`, checks that the cwd is one task repo of the caller's
// own capsule, reads that repo's HEAD itself, stamps the caller's spawn
// ordinal and appends the entry to the work item's record
// (driver.AppendBarRun: <data-dir>/bar-runs/<work-item-id>.jsonl).
//
//	POST /step/bar-run   {argv: [<line>], cwd, exit, duration_ms}
//
// Reachability: the live `deliver` step process of the calling work item
// only — stepCaller refuses every non-step caller (a human session, a task
// agent, another daemon's process), and this route refuses every other
// step (code, verification, …). The step-origin guard admits this one
// POST beside /step/result and /step/memories (routes.go). The exit and
// duration are the caller's word; the HEAD, the repo, the spawn and the
// time are the daemon's. The record is evidence the driver reads when it
// judges the deliver result (deliver_facts.go), never a verdict here.

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

const stepBarRunPath = "/step/bar-run"

// barRunStep is the one step the bar-run entry point serves.
const barRunStep = "deliver"

// stepBarRunRoute records one bar run of the calling deliver step.
func (d *Daemon) stepBarRunRoute(w http.ResponseWriter, r *http.Request) {
	j, ok := d.stepCaller(w, r)
	if !ok {
		return
	}
	if j.step != barRunStep {
		writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("%s serves the deliver step only (the caller is step %q of %s)", stepBarRunPath, j.step, j.wi))
		return
	}
	var req struct {
		Argv       []string `json:"argv"`
		Cwd        string   `json:"cwd"`
		Exit       *int     `json:"exit"`
		DurationMS int64    `json:"duration_ms"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.Argv) != 1 || strings.TrimSpace(req.Argv[0]) == "" || req.Exit == nil {
		writeErr(w, api.ErrDaemonBadInput, "a bar run carries argv (exactly one non-empty command line) and its exit")
		return
	}
	repo, dir, why := barRunRepo(j.capsuleDir, req.Cwd)
	if why != "" {
		writeErr(w, api.ErrDaemonBadInput, why)
		return
	}
	gctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(gctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	head := strings.TrimSpace(string(out))
	if err != nil || head == "" {
		writeErr(w, api.ErrDaemonInternal, fmt.Sprintf("repos/%s: HEAD unreadable: %v", repo, err))
		return
	}
	entry := driver.BarRun{Repo: repo, SpawnN: int64(j.attempt), Argv: req.Argv, Cwd: req.Cwd,
		Exit: *req.Exit, DurationMS: req.DurationMS, Head: head, At: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := driver.AppendBarRun(d.cfg.DataDir, j.wi, entry); err != nil {
		writeErr(w, api.ErrDaemonInternal, "the bar run was not recorded: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// barRunRepo answers the task repo a bar run's cwd names: exactly
// <capsule>/repos/<name> (symlinks resolved on both sides), an existing
// directory. Anything else is refused with the reason.
func barRunRepo(capsuleDir, cwd string) (string, string, string) {
	if !filepath.IsAbs(cwd) {
		return "", "", fmt.Sprintf("the bar run's cwd %q is not an absolute path", cwd)
	}
	repos := filepath.Join(capsuleDir, capsule.DirRepos)
	if rr, err := filepath.EvalSymlinks(repos); err == nil {
		repos = rr
	}
	dir := filepath.Clean(cwd)
	if rd, err := filepath.EvalSymlinks(dir); err == nil {
		dir = rd
	} else {
		return "", "", fmt.Sprintf("the bar run's cwd %q does not exist", cwd)
	}
	name := filepath.Base(dir)
	if filepath.Dir(dir) != repos || name == "" || name == "." || name == ".." {
		return "", "", fmt.Sprintf("the bar run's cwd %q is not a task repo of the calling work item (run pf bar-run from %s/<name>)", cwd, filepath.Join(capsuleDir, capsule.DirRepos))
	}
	return name, dir, ""
}
