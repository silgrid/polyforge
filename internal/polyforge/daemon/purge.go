package daemon

// purge.go: POST /profiles/{profile}/cleanup — the explicit, CLI-only
// permanent cleanup of a removed Cell. Without a matching confirmation it
// only reports the impact (the retained state dir and the Host decision
// feed's entries naming it); with {"confirm": "<profile>"} it drops every
// Host-side reference to the profile and the state dir, so nothing can
// route to it again.
// `default` is never cleaned and a still-configured Cell is refused (remove
// it first). Reach: a human session only (step/task DAEMON-403); the page
// offers no control for it.
//
// Order: the state dir is renamed to a tombstone first (one atomic rename —
// from then on the profile has no state), then the feed entries are
// dropped (one atomic write), then the tombstone is deleted. A cleanup
// interrupted anywhere is finished by running it again (idempotent).

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

// CleanupImpact is the cleanup route's answer.
type CleanupImpact struct {
	Profile string `json:"profile"`
	// StateDir is the Cell's retained state directory (StateDirExists
	// whether it is still on disk).
	StateDir       string `json:"state_dir"`
	StateDirExists bool   `json:"state_dir_exists"`
	// Decisions counts the Host decision feed's entries naming the
	// profile (decisions.go).
	Decisions int `json:"decisions"`
	// Applied is false for the impact report, true once everything is gone.
	Applied bool `json:"applied"`
}

// cleanupProfile reports (confirm == "") or performs the permanent cleanup.
func (h *Host) cleanupProfile(profile, confirm string) (*CleanupImpact, error) {
	if err := cellcfg.ValidName(profile); err != nil {
		return nil, &api.Error{Code: api.ErrDaemonBadInput, Detail: err.Error()}
	}
	if profile == cellcfg.DefaultProfile {
		return nil, &api.Error{Code: api.ErrDaemonBadInput, Detail: "the default Cell cannot be cleaned up (its state is the Host's own)"}
	}
	h.lifeMu.Lock()
	defer h.lifeMu.Unlock()
	if e := h.cell(profile); e != nil {
		h.mu.Lock()
		removed := e.removed
		h.mu.Unlock()
		if !removed {
			return nil, &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("Cell %q is still configured — `pf cell remove %s` first; nothing was cleaned", profile, profile)}
		}
	}
	if _, err := os.Stat(h.cellPath(profile)); err == nil {
		return nil, &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("Cell %q still has its file %s — `pf cell remove %s` first; nothing was cleaned", profile, h.cellPath(profile), profile)}
	}
	dir := h.cellDataDir(profile)
	tomb := dir + ".cleanup"
	out := &CleanupImpact{Profile: profile, StateDir: dir}
	_, err := os.Stat(dir)
	out.StateDirExists = err == nil
	n, err := h.feed.PurgeProfile(profile, false)
	if err != nil {
		return nil, err
	}
	out.Decisions = n
	if confirm == "" {
		return out, nil
	}
	if confirm != profile {
		return nil, &api.Error{Code: api.ErrDaemonBadInput, Detail: fmt.Sprintf("cleanup of %q needs the confirmation %q (got %q); nothing was cleaned", profile, profile, confirm)}
	}
	if out.StateDirExists {
		if err := os.Rename(dir, tomb); err != nil {
			return nil, &api.Error{Code: api.ErrDaemonInternal, Detail: "cleanup: retire the state dir: " + err.Error()}
		}
	}
	if _, err := h.feed.PurgeProfile(profile, true); err != nil {
		return nil, &api.Error{Code: api.ErrDaemonInternal, Detail: "cleanup: drop the decision feed's entries (run the cleanup again to finish): " + err.Error()}
	}
	if err := os.RemoveAll(tomb); err != nil {
		return nil, &api.Error{Code: api.ErrDaemonInternal, Detail: "cleanup: delete " + filepath.Base(tomb) + " (run the cleanup again to finish): " + err.Error()}
	}
	h.mu.Lock()
	delete(h.cells, profile)
	h.mu.Unlock()
	out.Applied = true
	return out, nil
}

// cleanupProfileRoute serves POST /profiles/{profile}/cleanup.
func (h *Host) cleanupProfileRoute(w http.ResponseWriter, r *http.Request) {
	if h.lifecycleRefusal(w, r) {
		return
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	out, err := h.cleanupProfile(r.PathValue("profile"), req.Confirm)
	if err != nil {
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
