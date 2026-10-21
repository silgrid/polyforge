package runtime

// claiming.go: the B1 pre-claim durable journal (reference.md §3.5). A
// crash anywhere between the aihub session claim and the first spawned
// agent would otherwise orphan a RUNNING session that nobody can name
// again — the v0 known gap "#Start-crash" (D5). The journal closes it
// from both sides:
//
//   - BEFORE the claim call, the minted session secret is persisted
//     durably. The entry is cleared once
//     the start settles (live job registered, the session ended blocked
//     as compensation — or kept end_pending in state.json — or the claim
//     refused).
//   - an uncleared entry is the start still under way: a later start of
//     the work item, and the next daemon start (Restart seeds a CLAIMING
//     placeholder for it), finish it through claimIntent — the journaled
//     session still holds the WI → the SAME session secret hands back the
//     SAME claim (D4); it does not → a fresh claim.
//   - a claim call whose answer is lost (transport error, timeout, hub
//     5xx) KEEPS the entry: the claim may have landed and the entry is
//     the only copy of its secret.
//
// The file is a map keyed by the canonical work-item id a start claims
// under. claimMu serializes the read-modify-write of every writer: they
// are different work items' actors.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"

	"github.com/silgrid/polyforge/internal/common/api"
)

// claimingFile is the on-disk shape of the journal: one entry per
// submitted id spelling whose start has not settled yet.
type claimingFile struct {
	Version int                     `json:"version"`
	Items   map[string]*claimingRec `json:"items"`
}

// claimingRec is one journaled, not-yet-settled claim intention (the
// reference.md §3.5 shape): the minted session secret is the replay anchor — a
// verify of it answers "did our claim land, and is it still ours?".
type claimingRec struct {
	WorkItemID string `json:"work_item_id"` // the claimed id (an older entry: the submitted spelling)
	SessionID  string `json:"session_id"`   // the minted SECRET (replay anchor, D4)
	// Models is the manual `pf submit --models` list of this start, so a
	// crash-replayed claim keeps it.
	Models []string `json:"models,omitempty"`
}

func (r *Runtime) claimingPath() string {
	return filepath.Join(r.cfg.DataDir, "claiming.json")
}

// loadClaiming reads the journal; a missing file is an empty one. A
// journal that exists but will not parse is QUARANTINED and logged
// (NOTE 6, v1.5 review): a torn/corrupt pre-registration journal is
// LOST — D5's window, which requires fs corruption to hit at all —
// and pretending nothing happened would hide it. The entries it
// carried cannot be replayed, and a session such an entry may have opened
// is not closed by this daemon. A quarantined journal never blocks a
// start.
func (r *Runtime) loadClaiming() map[string]*claimingRec {
	raw, err := os.ReadFile(r.claimingPath())
	if err != nil {
		if !os.IsNotExist(err) {
			// an unreadable-but-present journal is the same corruption
			// class as an unparsable one: loud, never silent.
			fmt.Fprintf(os.Stderr, "runtime: claiming journal unreadable (%v); starting empty\n", err)
		}
		return map[string]*claimingRec{}
	}
	var cf claimingFile
	if json.Unmarshal(raw, &cf) != nil || cf.Items == nil {
		fmt.Fprintf(os.Stderr, "runtime: claiming journal unparsable (%d bytes): the pre-claim entries it carried are LOST (D5's window; requires fs corruption)\n", len(raw))
		// quarantine beside the journal so the operator can inspect the
		// corruption while the next start runs on an empty one.
		ts := strconv.FormatInt(time.Now().UnixNano(), 36)
		if qerr := os.Rename(r.claimingPath(), r.claimingPath()+".corrupt."+ts); qerr != nil {
			fmt.Fprintf(os.Stderr, "runtime: claiming journal quarantine failed: %v\n", qerr)
		} else {
			fmt.Fprintf(os.Stderr, "runtime: corrupt claiming journal quarantined as claiming.json.corrupt.%s\n", ts)
		}
		return map[string]*claimingRec{}
	}
	return cf.Items
}

// writeClaiming persists the whole journal map atomically.
func (r *Runtime) writeClaiming(items map[string]*claimingRec) error {
	if items == nil {
		items = map[string]*claimingRec{}
	}
	blob, err := json.MarshalIndent(&claimingFile{Version: 2, Items: items}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(r.claimingPath(), blob, stateFilePerm)
}

// noteClaiming journals one claim intention — the minted session secret
// — BEFORE the claim call. A write failure is
// fatal to the start (the caller refuses to claim): a claim we cannot
// journal is exactly the dangling-session risk this file exists to close.
func (r *Runtime) noteClaiming(wi, sessionID string, models []string) error {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()
	items := r.loadClaiming()
	items[wi] = &claimingRec{
		WorkItemID: wi,
		SessionID:  sessionID,
		Models:     models,
	}
	return r.writeClaiming(items)
}

// clearClaiming settles one journal entry (the start completed, the
// session ended, or the claim never opened). A missing entry is a no-op;
// a write failure is loud but never blocks the caller.
func (r *Runtime) clearClaiming(wi string) { r.clearClaimingSession(wi, "") }

// clearClaimingSession is clearClaiming that, with sessionID set, only
// drops an entry still journaling that session: a close settling in the
// background must never drop the entry a newer start of the same work
// item wrote meanwhile.
func (r *Runtime) clearClaimingSession(wi, sessionID string) {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()
	items := r.loadClaiming()
	rec, ok := items[wi]
	if !ok || (sessionID != "" && rec.SessionID != sessionID) {
		return
	}
	delete(items, wi)
	if err := r.writeClaiming(items); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: claiming journal clear failed: %v\n", wi, err)
	}
}

// claimAnswerID is the canonical work-item id a claim answer carries.
func claimAnswerID(resp *api.SessionClaimResponse) string {
	if resp.WorkItem.ID != "" {
		return resp.WorkItem.ID
	}
	return resp.Claim.WorkItemID
}
