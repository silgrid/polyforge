package daemon

// profiles.go: GET /profiles — the Host's Cell registry and its EFFECTIVE
// workspace index, the facts the CLI resolver matches a cwd against
// (cli/profile.go). Reach: a human session only; a step or task agent's
// read is refused DAEMON-403 (its Cell is the Host's ownership record, it
// never resolves one itself).

import (
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// agentRefusal answers why a request from a step or task agent's process
// tree is refused on a user-only route ("" = the caller is not an agent).
// An unidentifiable peer is not refused here: user-only reads follow the
// GET posture of refuseStepOrigin; the mutating routes keep its fail-closed
// rule.
func agentRefusal(r *http.Request, machineIDs ...string) string {
	cred, ok := requestPeerCred(r.Context())
	if !ok {
		return ""
	}
	for _, id := range machineIDs {
		if wi, isStep := runtimeStepOrigin(cred.pid, id); isStep {
			return fmt.Sprintf("%s %s refused: the caller (pid %d) belongs to the step process tree of work item %s — this route serves a human session only", r.Method, r.URL.Path, cred.pid, wi)
		}
		if task, isTask := runtimeTaskOrigin(cred.pid, id); isTask {
			return fmt.Sprintf("%s %s refused: the caller (pid %d) belongs to the task process tree of %s — this route serves a human session only", r.Method, r.URL.Path, cred.pid, task)
		}
	}
	return ""
}

// profileInfo answers this Cell's registry row.
func (d *Daemon) profileInfo(profile, file string) cli.ProfileInfo {
	server, _ := cellcfg.NormalizeServer(d.cfg.Server)
	hb, _ := d.heartbeatSnapshot()
	ws := d.plan().Workspaces()
	if ws == nil {
		ws = []string{}
	}
	state, reason := "serving", ""
	if why := d.identityConflict(); why != "" {
		state, reason = "degraded", "identity mismatch: "+why
	} else if why := d.missingCredential(); why != "" {
		state, reason = "degraded", why
	} else if why := d.degradedReason(); why != "" {
		state, reason = "degraded", "AIHub unreachable or refusing: "+why
	} else if why := d.connectionNotice(); why != "" {
		reason = why
	}
	return cli.ProfileInfo{
		Profile:    profile,
		File:       file,
		State:      state,
		Reason:     reason,
		Valid:      true,
		Server:     server,
		Workspaces: ws,
		DataDir:    d.cfg.DataDir,
		MachineID:  d.cfg.MachineID,
		Heartbeat:  hb,
		Running:    len(d.occupiedSlots()),
		Pending:    d.q.LenPending(),

		ExecutionDisabled:     d.executionDisabled(),
		CellMaxConcurrentJobs: d.cellMaxConcurrentJobs(),
		MaxConcurrent:         d.q.MaxConcurrent(),

		Draining:      d.pendingChange() != "",
		PendingChange: d.pendingChange(),
	}
}

// cellMaxConcurrentJobs answers the Cell's applied optional ceiling (0 =
// none): the startup file value, then each reload's (reload.go applyLive).
func (d *Daemon) cellMaxConcurrentJobs() int {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	return d.cfg.CellMaxConcurrentJobs
}

// profilesRoute serves GET /profiles for a Daemon serving its one Cell.
func (d *Daemon) profilesRoute(w http.ResponseWriter, r *http.Request) {
	if reason := agentRefusal(r, d.cfg.MachineID); reason != "" {
		writeErr(w, api.ErrDaemonForbidden, reason)
		return
	}
	file := d.cfg.ConfigPath
	if file == "" {
		file = cli.DefaultConfigPath()
	}
	info := d.profileInfo(cellcfg.DefaultProfile, file)
	var idx []cellcfg.IndexEntry
	for _, ws := range info.Workspaces {
		idx = append(idx, cellcfg.IndexEntry{Profile: cellcfg.DefaultProfile, Workspace: ws})
	}
	writeJSON(w, http.StatusOK, cli.ProfilesResponse{Dir: filepath.Dir(file), Profiles: []cli.ProfileInfo{info}, Index: idx})
}
