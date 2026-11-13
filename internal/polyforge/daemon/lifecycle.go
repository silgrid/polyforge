package daemon

// lifecycle.go: Cells added, repaired, updated and removed at runtime —
// no Host restart. The config files are the truth (`pf cell add` and
// `pf config set` write them; a hand edit needs an explicit reload); the
// Host applies one profile's file on request:
//
//	POST   /profiles              {"profile"}   add or repair a Cell from its file
//	PATCH  /profiles/{profile}                  re-apply a Cell's file (update)
//	DELETE /profiles/{profile}                  remove a Cell (state retained)
//	POST   /config/reload  + X-PF-Profile       `pf --profile P config reload`
//
// A change that is invalid — a bad file, a conflict with a serving Cell —
// is refused and the registry keeps serving what it served. An existing
// profile never changes its AIHub identity in place: a Cell's identity
// (normalized server, and the Hub user its credential answers for) is
// recorded in its state dir, and restoring the same name over another
// server is refused with the new-profile route at every assembly (main.go
// cellConfig), and a file credential other than the effective one is
// adopted only for the same Hub user (drain.go adoptFileKey). Removal refuses while the
// Cell holds any work, never removes default, and keeps the Cell's state
// directory (history, identity) on disk.
//
// Reach: a human session only — every route refuses a step or task agent
// with DAEMON-403. Lifecycle operations are serialized by Host.lifeMu (no
// reader takes it); file reads and Cell starts run outside Host.mu.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// identityFile records a Cell's AIHub identity domain in its state dir.
const identityFile = "identity.json"

// cellIdentity is one Cell's recorded identity domain.
type cellIdentity struct {
	// Server is the normalized AIHub server.
	Server string `json:"server"`
	// User is the Hub user the Cell's credential answers for (learned from
	// the first successful heartbeat; "" until then).
	User string `json:"user,omitempty"`
}

// readIdentity answers the identity recorded in dir (ok=false: none).
func readIdentity(dir string) (cellIdentity, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, identityFile))
	if err != nil {
		return cellIdentity{}, false
	}
	var id cellIdentity
	if json.Unmarshal(raw, &id) != nil || id.Server == "" {
		return cellIdentity{}, false
	}
	return id, true
}

// writeIdentity records an identity atomically.
func writeIdentity(dir string, id cellIdentity) error {
	raw, _ := json.MarshalIndent(id, "", "  ")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, identityFile), append(raw, '\n'), 0o600)
}

// recordIdentity records this Cell's identity domain the first time it is
// known: the server at start, the Hub user once a heartbeat answered it.
// A Hub user that differs from the recorded one is never recorded over it:
// the mismatch is answered ("" = consistent) and the caller isolates the
// Cell. Called with no lock held (one small file write).
func (d *Daemon) recordIdentity(user string) string {
	server, err := cellcfg.NormalizeServer(d.cfg.Server)
	if err != nil {
		return ""
	}
	cur, ok := readIdentity(d.cfg.DataDir)
	if ok && cur.User != "" && user != "" && user != cur.User {
		return fmt.Sprintf("the AIHub answered this Cell's credential for user %s, but the Cell belongs to user %s (recorded in %s) — a Cell never changes its user in place; restore that user's api_key, or create a new profile for %s", user, cur.User, filepath.Join(d.cfg.DataDir, identityFile), user)
	}
	if ok && (cur.User != "" || user == "") {
		return ""
	}
	if !ok {
		cur = cellIdentity{Server: server}
	}
	if user != "" {
		cur.User = user
	}
	_ = writeIdentity(d.cfg.DataDir, cur)
	return ""
}

// identityMismatch answers why a Cell file's server cannot take over a
// profile whose state dir records another identity ("" = it can).
func identityMismatch(profile, dir, server string) string {
	rec, ok := readIdentity(dir)
	if !ok {
		return ""
	}
	norm, err := cellcfg.NormalizeServer(server)
	if err != nil || norm == rec.Server {
		return ""
	}
	return fmt.Sprintf("profile %q belongs to the AIHub identity %s (recorded in %s); a different AIHub (%s) is another identity domain — create a new profile with another name instead of reusing %q (its state and history stay with the old identity)",
		profile, rec.Server, filepath.Join(dir, identityFile), norm, profile)
}

// lifecycleRoutes mounts the lifecycle routes on the Host mux.
func (h *Host) lifecycleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /profiles", h.addProfileRoute)
	mux.HandleFunc("PATCH /profiles/{profile}", h.updateProfileRoute)
	mux.HandleFunc("DELETE /profiles/{profile}", h.removeProfileRoute)
	mux.HandleFunc("POST /profiles/{profile}/cleanup", h.cleanupProfileRoute)
	mux.HandleFunc("POST /config/reload", h.configReloadRoute)
}

// lifecycleRefusal applies the user-only reach to a lifecycle request.
func (h *Host) lifecycleRefusal(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := requestPeerCred(r.Context()); !ok {
		writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("%s %s refused: the caller's identity (UDS peer credential) is unavailable — only a human session may change the Cells", r.Method, r.URL.Path))
		return true
	}
	if reason := agentRefusal(r, h.machineIDs()...); reason != "" {
		writeErr(w, api.ErrDaemonForbidden, reason)
		return true
	}
	return false
}

func (h *Host) addProfileRoute(w http.ResponseWriter, r *http.Request) {
	if h.lifecycleRefusal(w, r) {
		return
	}
	var req struct {
		Profile string `json:"profile"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	h.answerApply(w, req.Profile)
}

func (h *Host) updateProfileRoute(w http.ResponseWriter, r *http.Request) {
	if h.lifecycleRefusal(w, r) {
		return
	}
	h.answerApply(w, r.PathValue("profile"))
}

func (h *Host) removeProfileRoute(w http.ResponseWriter, r *http.Request) {
	if h.lifecycleRefusal(w, r) {
		return
	}
	profile := r.PathValue("profile")
	if err := h.removeProfile(profile); err != nil {
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.profileRow(h.cell(profile)))
}

// configReloadRoute serves POST /config/reload: a named profile is the
// Host's to apply (add, repair or reload that Cell); default (or none) is
// the default Cell's own reload.
func (h *Host) configReloadRoute(w http.ResponseWriter, r *http.Request) {
	p := r.Header.Get(protocol.ProfileHeader)
	if p == "" || p == cellcfg.DefaultProfile {
		r.Header.Del(protocol.ProfileHeader)
		h.forward(w, r)
		return
	}
	if h.lifecycleRefusal(w, r) {
		return
	}
	h.answerApply(w, p)
}

// answerApply applies one profile and answers the reload response.
func (h *Host) answerApply(w http.ResponseWriter, profile string) {
	resp, err := h.applyProfile(profile)
	if err != nil {
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// applyProfile re-reads one named profile's file and makes the registry
// match it: an absent Cell is added, an invalid or removed one repaired, a
// serving one reloaded in place (same identity only). A refused change
// leaves the registry as it was.
func (h *Host) applyProfile(profile string) (*cli.ConfigReloadResponse, error) {
	if err := cellcfg.ValidName(profile); err != nil {
		return nil, &api.Error{Code: api.ErrDaemonBadInput, Detail: err.Error()}
	}
	h.lifeMu.Lock()
	defer h.lifeMu.Unlock()
	if profile == cellcfg.DefaultProfile {
		d, ae := h.routeTo(cellcfg.DefaultProfile)
		if ae != nil {
			return nil, ae
		}
		return d.reloadConfig()
	}
	set, err := cli.LoadHostSet(h.cfg.Set.HostPath)
	if err != nil {
		return nil, &api.Error{Code: api.ErrDaemonBadInput, Detail: "the Host file is invalid; nothing was applied: " + err.Error()}
	}
	var c *cli.CellConfig
	for i := range set.Cells {
		if set.Cells[i].Profile == profile {
			c = &set.Cells[i]
		}
	}
	e := h.cell(profile)
	if c == nil {
		if e != nil {
			// the file is gone: a hand removal, applied like `pf cell remove`
			if err := h.removeLocked(profile); err != nil {
				return nil, err
			}
			return &cli.ConfigReloadResponse{Outcomes: []cli.ConfigKeyOutcome{{Key: "cell " + profile, Status: "removed", Detail: "its file is gone; the state dir is kept"}}}, nil
		}
		return nil, &api.Error{Code: api.ErrDaemonNotFound, Detail: fmt.Sprintf("unknown profile %q: no Cell file %s exists — write it (or `pf cell add %s …`) first", profile, h.cellPath(profile), profile)}
	}
	if why := identityMismatch(profile, h.cellDataDir(profile), c.Cfg.Server); why != "" {
		return nil, &api.Error{Code: api.ErrDaemonConflict, Detail: why}
	}
	if c.Invalid != "" {
		// a serving Cell keeps serving its last valid runtime; an absent or
		// broken one stays (or becomes) visibly invalid
		h.mu.Lock()
		var serving *Daemon
		if e != nil && e.d == nil {
			e.invalid = c.Invalid
		} else if e == nil {
			h.cells[profile] = &cell{profile: profile, file: c.Path, invalid: c.Invalid}
		} else {
			serving = e.d
		}
		h.mu.Unlock()
		if serving != nil {
			// never silent: GET /profiles says the running Cell is not on
			// its file (an api_key removed from it included)
			serving.setConnNotice("its file is not applied (" + c.Invalid + "): the Cell serves its last valid runtime until the file is fixed; at the next Host start an invalid file leaves the Cell invalid")
		}
		return nil, &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("Cell %q was not changed: %s", profile, c.Invalid)}
	}
	if e != nil {
		h.mu.Lock()
		d := e.d
		h.mu.Unlock()
		if d != nil {
			return d.reloadConfig()
		}
	}
	// add, repair or restore: assemble and start a fresh runtime
	ne := h.assembleNamed(*c)
	if ne.d != nil {
		for _, other := range h.sortedCells() {
			h.mu.Lock()
			od := other.d
			h.mu.Unlock()
			if other.profile != profile && od != nil && od.cfg.MachineID == ne.d.cfg.MachineID {
				ne.d, ne.invalid = nil, machineIDConflict(profile, od.cfg.MachineID, []string{other.profile, profile})
				break
			}
		}
	}
	if ne.d != nil {
		if err := h.startCell(ne); err != nil {
			ne.d, ne.invalid = nil, fmt.Sprintf("load failure: %v", err)
		}
	}
	h.mu.Lock()
	h.cells[profile] = ne
	h.mu.Unlock()
	if ne.d == nil {
		return nil, &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("Cell %q could not be started: %s", profile, ne.invalid)}
	}
	ne.d.recordIdentity("")
	go ne.d.adoptFileKey()
	return &cli.ConfigReloadResponse{Outcomes: []cli.ConfigKeyOutcome{{Key: "cell " + profile, Status: "applied", Detail: "the Cell serves (no Host restart)"}}}, nil
}

// removeProfile removes one Cell (lifecycle-serialized).
func (h *Host) removeProfile(profile string) error {
	h.lifeMu.Lock()
	defer h.lifeMu.Unlock()
	return h.removeLocked(profile)
}

// removeLocked stops a Cell that holds no work and keeps its state dir.
func (h *Host) removeLocked(profile string) error {
	if profile == cellcfg.DefaultProfile {
		return &api.Error{Code: api.ErrDaemonBadInput, Detail: "the default Cell cannot be removed (it is the Host file's own Cell)"}
	}
	e := h.cell(profile)
	if e == nil {
		return &api.Error{Code: api.ErrDaemonNotFound, Detail: fmt.Sprintf("unknown profile %q: no such Cell", profile)}
	}
	h.mu.Lock()
	d := e.d
	h.mu.Unlock()
	if d != nil {
		if held := d.heldWork(); len(held) > 0 {
			return &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("Cell %q still holds work (%s) — finish, wrap or cancel it first; nothing was removed", profile, strings.Join(held, ", "))}
		}
		_ = d.Shutdown(context.Background(), "Cell removed")
		if e.stopped != nil {
			<-e.stopped
		}
	}
	h.mu.Lock()
	e.d, e.invalid, e.removed = nil, "removed (its state dir "+h.cellDataDir(profile)+" is kept)", true
	h.mu.Unlock()
	return nil
}

// heldWork lists the work a Cell holds: every job record (running, paused,
// waiting on a human — held state is not idle), every queued submit and
// every running task.
func (d *Daemon) heldWork() []string {
	var out []string
	for _, ji := range d.rt.List() {
		out = append(out, fmt.Sprintf("job %s (%s)", ji.WorkItem, ji.State))
	}
	if n := d.q.LenPending(); n > 0 {
		out = append(out, fmt.Sprintf("%d queued submit(s)", n))
	}
	if s := d.tasks.Summarize(); s.Running > 0 {
		out = append(out, fmt.Sprintf("%d running task(s)", s.Running))
	}
	sort.Strings(out)
	return out
}
