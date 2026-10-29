package core

// session.go — the v1.5 session-id ownership model:
// docs/design/polyforge-vnext/reference.md §3 (the archived
// v1.5-api-contract.md / v1.5-session-model.md hold the original rulings;
// B1 pre-claim journal is client-side; B3 identity/credential split and
// the M1–M5 rulings are enforced here).
//
//   - Claim  (POST .../claim, replaced handler): CAS in ONE transaction —
//     status==queued AND session_hash IS NULL AND bundle present AND no
//     unfinished blockers (M1 joint gate). Idempotent on the session
//     secret itself (D4): the holder's repeated claim answers the same
//     claim; a secret that claimed the WI before but no longer holds it
//     (superseded, force-ended, ended) answers CORE-403 STALE_SESSION.
//   - Verify (POST .../session-verify): compare-only (B3): the hub stores
//     only hex(sha256(session_id)); no state change, no secret echo.
//   - Attach (POST .../session-attach): same-session resume of a paused
//     WI (M3); clean_pause WIs refuse (operator pause is not
//     auto-resumable).
//   - ForceEnd (POST .../force-end-session): the admin/maintainer recovery
//     door — ends a stuck holder's session without its secret (WI ->
//     queued, old secret fenced); the supersession event carries the OLD
//     fingerprint (m4 audit). Any daemon then claims normally.
//   - End (POST .../session-end): session-fenced terminal verdict; the
//     session row is cleared entirely (M2: blocked = status blocked +
//     cleared session, excluded from the claim gate).
//   - Pause: the M3 pause transition for session-held WIs (session KEPT,
//     status=paused, clean_pause set), reached as session-end's 4th
//     verdict ("paused").
//
// The git push credential's session path (fencing-on-use, §1.6) lives in
// owner.go's CheckSessionCredential and validates against the SAME stored
// hash, so a claim here is the only thing that can make a push valid.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

// SessionManager is the v1.5 ownership-arbitration face.
type SessionManager struct{ db *DB }

func NewSessionManager(db *DB) *SessionManager { return &SessionManager{db: db} }

// queryRower is the shared read surface of *DB and *sql.Tx (the single
// writer's snapshot is authoritative for every in-transaction recheck).
type queryRower interface {
	QueryRow(query string, args ...any) *sql.Row
}

// ---------------------------------------------------------------------------
// Session row + public view (m4)

// sessionRow is the session block of a work_items row (plus the fields
// the claim/diagnosis paths need on the same read).
type sessionRow struct {
	Status      string
	Hash        sql.NullString
	Fingerprint sql.NullString
	Machine     sql.NullString
	StartedAt   sql.NullString
	CleanPause  bool
	BundleName  sql.NullString
	ProjectID   string
}

// sessionHeld reports whether the row carries a live session.
func sessionHeld(r *sessionRow) bool { return r.Hash.Valid && r.Hash.String != "" }

// loadSessionRow reads the session block (one query, works on *DB and
// *sql.Tx alike). A missing WI is the typed CORE-404.
func loadSessionRow(q queryRower, wiID string) (*sessionRow, error) {
	var r sessionRow
	err := q.QueryRow(`SELECT status, session_hash, session_fingerprint, session_machine,
		session_started_at, clean_pause, bundle_name, project_id
		FROM work_items WHERE id=?1`, wiID).
		Scan(&r.Status, &r.Hash, &r.Fingerprint, &r.Machine, &r.StartedAt,
			&r.CleanPause, &r.BundleName, &r.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(api.ErrCoreNotFound, "", "work item %s", wiID)
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// SessionPublic is the m4 public audit block every WorkItem JSON response
// carries. NEVER a hash, never the secret — the fingerprint is the public
// handle (B3).
type SessionPublic struct {
	SessionFingerprint string `json:"session_fingerprint,omitempty"`
	SessionMachine     string `json:"session_machine,omitempty"`
	SessionStartedAt   string `json:"session_started_at,omitempty"`
	CleanPause         bool   `json:"clean_pause"`
}

// WIView is a WorkItem with its public session audit block attached. It
// marshals as the api.WorkItem JSON plus the SessionPublic fields
// (embedded structs flatten); Phase 0's api.WorkItem itself is frozen and
// gains nothing, so the block rides alongside it at the server boundary.
// The frozen §4.6 repo inventory rides the same way: a
// well-formed create must be observable on the WI view, not silently
// discarded; it is omitted when the WI declared none.
type WIView struct {
	api.WorkItem
	SessionPublic
	RepoInventory []RepoDecl `json:"repo_inventory,omitempty"`
	// WaitingFor (polyforge#161) is read-only and derived: the unfinished
	// `blocks` blockers (status other than wrapped) of a QUEUED work item
	// — who it is waiting for. Never persisted, not a state; omitted
	// when the WI waits on nobody.
	WaitingFor []WaitingBlocker `json:"waiting_for,omitempty"`
	// inv is the raw frozen inventory column (the ready queue's gate).
	inv string
}

// WaitingBlocker is one entry of the derived waiting_for list.
type WaitingBlocker struct {
	Slug   string `json:"slug"`
	Status string `json:"status"`
}

// waitingForBatch derives the waiting_for lists of many WIs in ONE query.
// Only queued dependents carry one (the only status a claim starts
// from); the release predicate is unfinishedBlockers' (only wrapped
// releases).
func (db *DB) waitingForBatch(ids []string) (map[string][]WaitingBlocker, error) {
	out := map[string][]WaitingBlocker{}
	if len(ids) == 0 {
		return out, nil
	}
	var placeholders []string
	var args []any
	for i, id := range ids {
		placeholders = append(placeholders, "?"+strconv.Itoa(i+1))
		args = append(args, id)
	}
	rows, err := db.Query(`SELECT d.blocked_wi_id, p.name, b.seq, b.status
		FROM wi_dependencies d
		JOIN work_items w ON w.id = d.blocked_wi_id
		JOIN work_items b ON b.id = d.blocking_wi_id
		JOIN projects p ON p.id = b.project_id
		WHERE d.kind='blocks' AND w.status='queued' AND b.status <> 'wrapped'
		AND d.blocked_wi_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY b.seq`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var blocked, project, status string
		var seq int64
		if err := rows.Scan(&blocked, &project, &seq, &status); err != nil {
			return nil, err
		}
		out[blocked] = append(out[blocked], WaitingBlocker{
			Slug: project + "#" + strconv.FormatInt(seq, 10), Status: status,
		})
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Claim (reference.md §3.2)

// resolveSessionMachine resolves the machine id a claim/attach records
//, on the caller's own transaction snapshot: a machines
// row owned by the caller records its id; a row owned by another
// operator is refused AUTH-403 with nothing written; an absent row, or
// a row with no operator, records NULL and the claim/attach succeeds.
// Admins follow the same rule — the ownership is the caller's, not the
// role's. An empty machine_id (the attach default) keeps the recorded
// machine and never reaches the machines table.
func resolveSessionMachine(q queryRower, machineID, callerID string) (string, error) {
	if machineID == "" {
		return "", nil
	}
	var operator string
	err := q.QueryRow(`SELECT COALESCE(operator_id,'') FROM machines WHERE id=?1`, machineID).Scan(&operator)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if operator == "" {
		return "", nil // an operator-less row records NULL too
	}
	if operator == callerID {
		return machineID, nil
	}
	return "", Fail(api.ErrAuthForbidden, "",
		"machine %s is registered to another operator; claim or attach with a --machine-id you operate", machineID)
}

// Claim executes the v1.5 CAS claim. callerIdentity is the api-key's
// user id (the claim's assignee). The response carries the public
// fingerprint only — the secret never echoes.
//
// The session secret is the replay key (D4): a claim whose answer was
// lost is repeated with the same secret, and while that session holds the
// WI the repeat answers the same claim, re-read from the row. A secret
// that claimed this WI before but no longer holds it is refused
// STALE_SESSION: a superseded or ended session never claims again.
// An idempotency_key an older daemon still sends is an unknown body field
// and ignored by the decoder.
func (m *SessionManager) Claim(wiID string, req api.SessionClaimRequest, callerIdentity string) (*api.SessionClaim, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "session_id required (the client-minted secret)")
	}
	hash := api.SessionHashHex(req.SessionID)
	fp := api.SessionFingerprint(req.SessionID)
	// The joint gate is diagnosed on the transaction's own snapshot and the
	// guarded UPDATE writes it; the store has a single connection (Open:
	// SetMaxOpenConns(1)), so nothing interleaves between the two.
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row, replay, err := claimGate(tx, wiID, req.SessionID)
	if err != nil {
		return nil, err
	}
	if replay {
		return claimAnswerTx(tx, wiID, row) // the holder's replay: no write
	}

	machine, err := resolveSessionMachine(tx, req.MachineID, callerIdentity)
	if err != nil {
		return nil, err
	}
	now := Now()
	var machineAny any
	if machine != "" {
		machineAny = machine
	}
	res, err := tx.Exec(`UPDATE work_items SET status='running', session_hash=?1, session_fingerprint=?2,
		session_machine=?3, session_started_at=?4, clean_pause=0, assignee=?5,
		waiting_on=NULL, updated_at=?4
		WHERE id=?6 AND status='queued' AND session_hash IS NULL`,
		hash, fp, machineAny, now, callerIdentity, wiID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, Fail(api.ErrCoreConflict, "RACE_LOST", "state changed concurrently")
	}
	if err := appendEventTx(tx, wiID, now, callerIdentity, "session_claimed",
		map[string]any{"session_fingerprint": fp, "machine_id": machine}); err != nil {
		return nil, err
	}
	row, err = loadSessionRow(tx, wiID)
	if err != nil {
		return nil, err
	}
	claim, err := claimAnswerTx(tx, wiID, row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claim, nil
}

// ClaimGate answers the refusal a claim with this secret would meet now
// (nil: eligible, a holder's replay included), read on the store's own
// snapshot with nothing written. The server asks it before creating the
// work item's bare repo, so a refused claim (terminal, held, not queued,
// dependency-blocked, registration-only) never leaves an empty repo;
// Claim's own transaction stays the authority. A claim that passes here
// and then loses the CAS race still created (idempotently) the repo.
func (m *SessionManager) ClaimGate(wiID, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return Fail(api.ErrCoreBadInput, "", "session_id required (the client-minted secret)")
	}
	_, _, err := claimGate(m.db, wiID, sessionID)
	return err
}

// claimGate is the claim's eligibility diagnosis on one snapshot: the
// holder's replay (replay true), else STALE_SESSION for a secret that
// claimed before, else the joint gate, blockers and bundle legs.
func claimGate(q queryRower, wiID, sessionID string) (*sessionRow, bool, error) {
	hash := api.SessionHashHex(sessionID)
	fp := api.SessionFingerprint(sessionID)
	row, err := loadSessionRow(q, wiID)
	if err != nil {
		return nil, false, err
	}
	if sessionHeld(row) && row.Hash.String == hash {
		return row, true, nil
	}
	if claimed, err := sessionClaimedBefore(q, wiID, fp); err != nil {
		return nil, false, err
	} else if claimed {
		return nil, false, Fail(api.ErrCoreForbidden, "STALE_SESSION",
			"session %s no longer holds the work item (current holder %s)", fp, row.Fingerprint.String)
	}
	if err := diagnoseClaimGate(row); err != nil {
		return nil, false, err
	}
	if blocked, err := unfinishedBlockers(q, wiID); err != nil {
		return nil, false, err
	} else if blocked {
		return nil, false, Fail(api.ErrCoreConflict, "DEPENDENCY_BLOCKED", dependencyBlockedDetail)
	}
	if err := diagnoseClaimBundle(row); err != nil {
		return nil, false, err
	}
	return row, false, nil
}

// claimAnswerTx assembles the claim answer of the session that holds row:
// identity from the row, repos and the blocked seal read on the same
// snapshot.
func claimAnswerTx(tx *sql.Tx, wiID string, row *sessionRow) (*api.SessionClaim, error) {
	claim := &api.SessionClaim{
		WorkItemID:         wiID,
		SessionFingerprint: row.Fingerprint.String,
		MachineID:          row.Machine.String,
		BundleName:         row.BundleName.String,
		ClaimedAt:          row.StartedAt.String,
	}
	var err error
	claim.Repos, err = claimReposTx(tx, wiID, row.ProjectID)
	if err != nil {
		return nil, err
	}
	claim.SealedHead, claim.SealedMachine = blockedSealTx(tx, wiID)
	return claim, nil
}

// sessionClaimedBefore reports whether the session with fingerprint fp
// ever claimed the work item: its session_claimed event is the durable
// record.
func sessionClaimedBefore(q queryRower, wiID, fp string) (bool, error) {
	var one int
	err := q.QueryRow(`SELECT 1 FROM wi_events WHERE work_item_id=?1 AND event_type='session_claimed'
		AND json_extract(payload, '$.session_fingerprint')=?2 LIMIT 1`, wiID, fp).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// dependencyBlockedDetail is the DEPENDENCY_BLOCKED refusal text
// (polyforge#161: failed/canceled blockers keep the dependent waiting).
const dependencyBlockedDetail = "work item has an unfinished blocking dependency " +
	"(only a wrapped blocker releases; remove the edge with `pf file dep rm` to proceed)"

// diagnoseClaimGate is the M1 joint CAS gate's error face. Order matters:
// terminal first (CORE-410), then session-held (the precise double-claim
// diagnosis), then status; blockers and the bundle are checked after
// these by the caller (the v1 precedence — DEPENDENCY_BLOCKED wins over
// NOT_CLAIMABLE for a WI with both problems — is preserved).
func diagnoseClaimGate(row *sessionRow) error {
	if isTerminal(row.Status) {
		return Fail(api.ErrCoreGone, "TERMINAL_STATE", "work item is in terminal state %s", row.Status)
	}
	if sessionHeld(row) {
		return Fail(api.ErrCoreConflict, "ALREADY_HELD",
			"the work item is already held by session %s", row.Fingerprint.String)
	}
	if row.Status != api.StatusQueued {
		return Fail(api.ErrCoreConflict, "NOT_CLAIMABLE",
			"claim requires a queued work item (got %s)", row.Status)
	}
	return nil
}

// diagnoseClaimBundle is the gate's bundle leg (registration tickets never
// execute, #17).
func diagnoseClaimBundle(row *sessionRow) error {
	if !row.BundleName.Valid || row.BundleName.String == "" {
		return Fail(api.ErrCoreConflict, "NOT_CLAIMABLE",
			"registration-only work item (no workflow bundle) is not claimable")
	}
	return nil
}

// projectReposTx reads the project's task-repo inventory on the claim's
// own snapshot (the runtime clones each entry into the capsule before the
// agent starts).
func projectReposTx(tx queryRower, projectID string) ([]api.ProjectRepo, error) {
	var reposJSON string
	err := tx.QueryRow(`SELECT repos FROM projects WHERE id=?1`, projectID).Scan(&reposJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(api.ErrCoreNotFound, "", "project %s of the work item not found", projectID)
	}
	if err != nil {
		return nil, err
	}
	repos := []api.ProjectRepo{}
	if reposJSON != "" {
		if err := json.Unmarshal([]byte(reposJSON), &repos); err != nil {
			return nil, err
		}
		if repos == nil {
			repos = []api.ProjectRepo{}
		}
	}
	return repos, nil
}

// ---------------------------------------------------------------------------
// Verify (reference.md §3.3, compare-only B3)

// Verify compares the presented secret's hash against the stored one.
// No state change, no event, no secret echo: the answer says whether the
// caller's session is still the holder and — for a superseded daemon to
// self-diagnose — who holds it now (public fingerprint only).
func (m *SessionManager) Verify(wiID, sessionID string) (*api.SessionVerifyResponse, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "session_id required")
	}
	row, err := loadSessionRow(m.db, wiID)
	if err != nil {
		return nil, err
	}
	resp := &api.SessionVerifyResponse{
		WorkItemID: wiID,
		Status:     row.Status,
		CleanPause: row.CleanPause,
	}
	if sessionHeld(row) {
		resp.CurrentFingerprint = row.Fingerprint.String
		resp.Matches = api.SessionHashHex(sessionID) == row.Hash.String
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Attach (reference.md §3.3, M3 same-session resume)

// Attach resumes a paused WI under the SAME session: hash matches AND
// status==paused AND clean_pause==0 → running. Machine/owner refresh to
// the requester's values; session_started_at keeps the original claim
// time (the session never changed).
func (m *SessionManager) Attach(wiID string, req api.SessionAttachRequest, callerIdentity string) (*api.SessionClaim, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "session_id required")
	}
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row, err := attachGate(tx, wiID, req.SessionID)
	if err != nil {
		return nil, err
	}
	// The recorded machine: the resolved ownership of the body's
	// machine_id, or the row's own machine when the body
	// names none.
	machine := row.Machine.String
	if req.MachineID != "" {
		if machine, err = resolveSessionMachine(tx, req.MachineID, callerIdentity); err != nil {
			return nil, err
		}
	}
	now := Now()
	var machineAny any
	if machine != "" {
		machineAny = machine
	}
	res, err := tx.Exec(`UPDATE work_items SET status='running', session_machine=?1,
		assignee=?2, updated_at=?3
		WHERE id=?4 AND status='paused' AND session_hash=?5 AND clean_pause=0`,
		machineAny, callerIdentity, now, wiID, row.Hash.String)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, Fail(api.ErrCoreConflict, "NOT_ATTACHABLE", "state changed concurrently")
	}
	if err := appendEventTx(tx, wiID, now, callerIdentity, "session_attached",
		map[string]any{"session_fingerprint": row.Fingerprint.String, "machine_id": machine}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &api.SessionClaim{
		WorkItemID:         wiID,
		SessionFingerprint: row.Fingerprint.String,
		MachineID:          machine,
		BundleName:         row.BundleName.String,
		ClaimedAt:          row.StartedAt.String, // the session's own start
	}, nil
}

// AttachGate is ClaimGate for the attach route: the refusal an attach
// with this secret would meet now, read with nothing written.
func (m *SessionManager) AttachGate(wiID, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return Fail(api.ErrCoreBadInput, "", "session_id required")
	}
	_, err := attachGate(m.db, wiID, sessionID)
	return err
}

// attachGate is the attach's eligibility diagnosis on one snapshot: the
// secret holds the work item, which is paused and not clean-paused.
func attachGate(q queryRower, wiID, sessionID string) (*sessionRow, error) {
	row, err := loadSessionRow(q, wiID)
	if err != nil {
		return nil, err
	}
	if !sessionHeld(row) || api.SessionHashHex(sessionID) != row.Hash.String {
		return nil, Fail(api.ErrCoreForbidden, "STALE_SESSION",
			"session does not match the current holder %s", row.Fingerprint.String)
	}
	if row.Status != api.StatusPaused || row.CleanPause {
		return nil, Fail(api.ErrCoreConflict, "NOT_ATTACHABLE",
			"attach requires a paused, non-clean-paused work item (got status %s, clean_pause %v)",
			row.Status, row.CleanPause)
	}
	return row, nil
}

// ---------------------------------------------------------------------------
// Force end (v2-converge §6, §8 tier four)

// ForceEnd is the admin/maintainer recovery door for a work item held by a
// dead or stuck daemon: it ends the live session WITHOUT the holder's
// secret, returning the WI to queued with the session row cleared. The old
// holder is fenced by construction (its secret no longer matches any
// stored hash: git push -> GIT-401, session-end/pause/verify -> stale), and
// any daemon then claims the queued WI through the ordinary claim path,
// restoring progress from the capsule revision chain. Gate: running or
// paused (a session is held) and a non-empty reason. The session_superseded
// event carries the OLD fingerprint (m4 audit).
func (m *SessionManager) ForceEnd(wiID, reason, callerIdentity string) error {
	if strings.TrimSpace(reason) == "" {
		return Fail(api.ErrCoreBadInput, "", "reason required")
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	row, err := loadSessionRow(tx, wiID)
	if err != nil {
		return err
	}
	if isTerminal(row.Status) {
		return Fail(api.ErrCoreGone, "TERMINAL_STATE", "work item is in terminal state %s", row.Status)
	}
	if !sessionHeld(row) || (row.Status != api.StatusRunning && row.Status != api.StatusPaused) {
		return Fail(api.ErrCoreConflict, "NOT_HELD",
			"end-session requires a running or paused work item holding a session (got %s)", row.Status)
	}
	now := Now()
	// Fenced on the hash read in this snapshot: a concurrent end/swap
	// refuses instead of ending someone else's fresh session.
	res, err := tx.Exec(`UPDATE work_items SET status='queued', session_hash=NULL, session_fingerprint=NULL,
		session_machine=NULL, session_started_at=NULL, clean_pause=0,
		waiting_on=NULL, updated_at=?1
		WHERE id=?2 AND session_hash=?3 AND status IN ('running','paused')`, now, wiID, row.Hash.String)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Fail(api.ErrCoreConflict, "RACE_LOST", "state changed concurrently")
	}
	if err := setBlockedSealTx(tx, wiID, "", "", true); err != nil {
		return err
	}
	if err := appendEventTx(tx, wiID, now, callerIdentity, "session_superseded",
		map[string]any{
			"old_session_fingerprint": row.Fingerprint.String,
			"old_machine_id":          row.Machine.String,
			"from":                    row.Status,
			"to":                      api.StatusQueued,
			"reason":                  reason,
		}); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Session end (reference.md §3.3) and pause (M3)

// blockedSealKey is the WI attrs key carrying the blocked verdict's sealed
// capsule head (v2-converge §7): {"head": sha, "machine": id}. It rides the
// existing business attrs JSON — no schema change. Set by End(blocked) with
// a head, cleared by every other End/Pause, read by the claim answer so a
// continue on a different machine can check its clone. Create refuses the
// key (checkAttrs) and attrs is not PATCH-able (patchableFields),
// so only this file writes the seal; a missing or unreadable one only
// disables the check (fail open).
const blockedSealKey = "blocked_seal"

type txExecer interface {
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

func loadAttrsTx(tx txExecer, wiID string) (map[string]any, error) {
	var raw sql.NullString
	if err := tx.QueryRow(`SELECT attrs FROM work_items WHERE id=?1`, wiID).Scan(&raw); err != nil {
		return nil, err
	}
	attrs := map[string]any{}
	if raw.Valid && raw.String != "" {
		_ = json.Unmarshal([]byte(raw.String), &attrs)
	}
	if attrs == nil {
		attrs = map[string]any{}
	}
	return attrs, nil
}

// setBlockedSealTx records (head != "") or clears (clear) the seal.
func setBlockedSealTx(tx txExecer, wiID, head, machine string, clear bool) error {
	if head == "" && !clear {
		return nil
	}
	attrs, err := loadAttrsTx(tx, wiID)
	if err != nil {
		return err
	}
	if _, had := attrs[blockedSealKey]; !had && head == "" {
		return nil
	}
	if head != "" {
		attrs[blockedSealKey] = map[string]any{"head": head, "machine": machine}
	} else {
		delete(attrs, blockedSealKey)
	}
	blob, err := json.Marshal(attrs)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE work_items SET attrs=?1 WHERE id=?2`, string(blob), wiID)
	return err
}

// blockedSealTx reads the recorded seal ("" when none).
func blockedSealTx(tx txExecer, wiID string) (head, machine string) {
	attrs, err := loadAttrsTx(tx, wiID)
	if err != nil {
		return "", ""
	}
	m, _ := attrs[blockedSealKey].(map[string]any)
	head, _ = m["head"].(string)
	machine, _ = m["machine"].(string)
	return head, machine
}

// End is the session-fenced terminal verdict: hash matches → set the
// status (wrapped|failed|blocked), CLEAR the session row entirely (M2:
// blocked = status blocked + cleared session, excluded from the claim
// gate until a human unblocks), event session_ended.
func (m *SessionManager) End(wiID string, req api.SessionEndRequest, callerIdentity string) error {
	switch req.Status {
	case api.StatusWrapped, api.StatusFailed, api.StatusBlocked:
	case api.StatusPaused:
		// "paused" routes to the M3
		// pause transition — the session is KEPT (restart attaches), the
		// clean_pause flag rides the same atomic fenced UPDATE. Only the
		// three terminal verdicts clear the session row.
		return m.Pause(wiID, req.SessionID, req.CleanPause, req.Note, callerIdentity)
	default:
		return Fail(api.ErrCoreBadInput, "", "status must be one of wrapped|failed|blocked|paused")
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	row, err := loadSessionRow(tx, wiID)
	if err != nil {
		return err
	}
	if !sessionHeld(row) || api.SessionHashHex(req.SessionID) != row.Hash.String {
		return Fail(api.ErrCoreForbidden, "STALE_SESSION",
			"session does not match the current holder %s", row.Fingerprint.String)
	}
	now := Now()
	sets := []string{"status=?", "updated_at=?",
		"session_hash=NULL", "session_fingerprint=NULL", "session_machine=NULL",
		"session_started_at=NULL", "clean_pause=0"}
	args := []any{req.Status, now}
	if req.Status == api.StatusWrapped || req.Status == api.StatusFailed {
		sets = append(sets, "closed_at=?")
		args = append(args, now)
	}
	if req.Status == api.StatusBlocked {
		sets = append(sets, "waiting_on='owner'")
	}
	// Fenced: the guarded hash in the WHERE is the fence — a session that
	// was swapped concurrently refuses instead of terminalizing someone
	// else's WI.
	args = append(args, wiID, row.Hash.String)
	res, err := tx.Exec(`UPDATE work_items SET `+strings.Join(sets, ",")+` WHERE id=? AND session_hash=?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Fail(api.ErrCoreForbidden, "STALE_SESSION", "the session changed concurrently")
	}
	payload := map[string]any{
		"status":              req.Status,
		"session_fingerprint": row.Fingerprint.String,
	}
	if req.Note != "" {
		payload["note"] = req.Note
	}
	// The blocked verdict records the sealed head (+ the sealing machine,
	// from the session row); any other verdict clears a stale seal. A
	// blocked verdict without a head (an abandoned claim) keeps the
	// standing seal.
	if err := setBlockedSealTx(tx, wiID, req.HeadSHA, row.Machine.String, req.Status != api.StatusBlocked); err != nil {
		return err
	}
	if req.HeadSHA != "" && req.Status == api.StatusBlocked {
		payload["head_sha"] = req.HeadSHA
	}
	if err := appendEventTx(tx, wiID, now, callerIdentity, "session_ended", payload); err != nil {
		return err
	}
	return tx.Commit()
}

// Pause is the M3 pause transition for session-held WIs, reached as
// session-end's "paused" verdict (the merge amend): the session is KEPT
// (a same-machine restart attaches back through it), status becomes
// paused, and clean_pause records WHO paused — 1 = operator pause (never auto-resumed;
// only a CLI unpause (or a force-end + re-claim) may resume), 0 = daemon shutdown.
func (m *SessionManager) Pause(wiID, sessionID string, cleanPause bool, note, callerIdentity string) error {
	if strings.TrimSpace(sessionID) == "" {
		return Fail(api.ErrCoreBadInput, "", "session_id required")
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	row, err := loadSessionRow(tx, wiID)
	if err != nil {
		return err
	}
	if !sessionHeld(row) || api.SessionHashHex(sessionID) != row.Hash.String {
		return Fail(api.ErrCoreForbidden, "STALE_SESSION",
			"session does not match the current holder %s", row.Fingerprint.String)
	}
	if row.Status != api.StatusRunning {
		return Fail(api.ErrCoreConflict, "NOT_RUNNING",
			"pause requires a running work item (got %s)", row.Status)
	}
	now := Now()
	var cp int
	if cleanPause {
		cp = 1
	}
	res, err := tx.Exec(`UPDATE work_items SET status='paused', clean_pause=?1, updated_at=?2
		WHERE id=?3 AND session_hash=?4 AND status='running'`, cp, now, wiID, row.Hash.String)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Fail(api.ErrCoreForbidden, "STALE_SESSION", "the session changed concurrently")
	}
	if err := setBlockedSealTx(tx, wiID, "", "", true); err != nil {
		return err
	}
	payload := map[string]any{
		"session_fingerprint": row.Fingerprint.String,
		"clean_pause":         cleanPause,
	}
	if note != "" {
		payload["note"] = note
	}
	if err := appendEventTx(tx, wiID, now, callerIdentity, "session_paused", payload); err != nil {
		return err
	}
	return tx.Commit()
}
