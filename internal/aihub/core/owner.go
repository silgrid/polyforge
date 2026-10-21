package core

// owner.go — the git-face credential check of the v1.5 session model.
// The v1 generation/attempt machinery (owner_generations, intent-gated
// claims, minted push credentials, /end-generation) is deleted; the
// only survivor is CheckSessionCredential, which validates a push's
// Basic pair against the WI's LIVE session (fencing-on-use, contract
// §1.6): hex(sha256(password)) == work_items.session_hash AND
// status==running. Claim/attach/force-end/end live in session.go.

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

// OwnerManager owns the git-face credential check.
type OwnerManager struct{ db *DB }

func NewOwnerManager(db *DB) *OwnerManager { return &OwnerManager{db: db} }

// CheckSessionCredential validates a git-face Basic pair: username must be
// the work-item id, the password is the session secret — validated as
// hex(sha256(password)) == work_items.session_hash AND status == running
// (M3: pushes are refused while paused).
// A WI with no live session answers GIT-401: only a session claim can
// make a push valid.
func (m *OwnerManager) CheckSessionCredential(wiID, username, secret string) error {
	if wiID == "" || username != wiID || secret == "" {
		return Fail(api.ErrGitUnauthorized, "BAD_CREDENTIAL", "credential rejected")
	}
	var hash, fp sql.NullString
	var status string
	err := m.db.QueryRow(`SELECT session_hash, session_fingerprint, status FROM work_items WHERE id=?1`, wiID).
		Scan(&hash, &fp, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return Fail(api.ErrGitUnauthorized, "NO_SESSION", "credential rejected: no live session")
	}
	if err != nil {
		return err
	}
	if !hash.Valid || hash.String == "" {
		return Fail(api.ErrGitUnauthorized, "NO_SESSION", "credential rejected: no live session")
	}
	// Fencing-on-use: a mismatch names the CURRENT holder's public
	// fingerprint so a superseded daemon can self-diagnose from the
	// rejection alone.
	if api.SessionHashHex(secret) != hash.String {
		return Fail(api.ErrGitUnauthorized, "BAD_CREDENTIAL",
			"credential rejected: the work item is held by session %s", fp.String)
	}
	if status != api.StatusRunning {
		return Fail(api.ErrGitForbidden, "PAUSED",
			"pushes are refused while the work item is %s (session holder %s)", status, fp.String)
	}
	return nil
}

// unfinishedBlockers is the shared query (owner must not depend on the
// WIManager instance; both reach the same single-writer connection, and
// the queryRower surface lets the session claim recheck it inside its own
// transaction snapshot).
//
// polyforge#161: only a WRAPPED blocker releases its dependent. A failed
// or canceled blocker keeps the dependent waiting — "B runs after A
// succeeded" — until a human removes the edge (`pf file dep rm`).
func unfinishedBlockers(q queryRower, wiID string) (bool, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM wi_dependencies d
		JOIN work_items b ON b.id = d.blocking_wi_id
		WHERE d.blocked_wi_id=?1 AND d.kind='blocks'
		AND b.status <> 'wrapped'`, wiID).Scan(&n)
	return n > 0, err
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT")
}
