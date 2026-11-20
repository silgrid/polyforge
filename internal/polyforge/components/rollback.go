package components

// rollback.go (polyforge#190, 84-inplace-upgrade): the one-click
// rollback — the explicit, user-asked downgrade of current to
// previous, with the same drain, wait and restarts the upgrade takes
// (the ticket's Q6 ruling). Where the install transaction refuses
// every older seed, the rollback is the one sanctioned way back: an
// explicit act, so it is allowed to go down a version — but never
// blindly. It refuses, naming the reason and changing nothing at all,
// when there is no previous to go back to, when the previous tree is
// corrupt or its manifest a schema this build cannot judge (a version
// that crossed a schema migration), when the previous is of another
// channel, or when the running base does not meet the previous's
// minimum base requirement — the downgrade protection the layout
// itself carries.

import (
	"context"
	"errors"
	"time"
)

// RollbackOptions is one rollback's inputs.
type RollbackOptions struct {
	// Root is the components volume's root.
	Root string
	// BaseVersion is the running base's (bootstrap pf's) version, for
	// the previous version's minimum base requirement.
	BaseVersion string
	// Face is the daemon face (required).
	Face DaemonFace
	// Force passes the drain its force.
	Force bool
	// Poll is the drain wait's re-ask interval (<= 0: the upgrade's).
	Poll time.Duration
	// RestartPiWeb terminates the old tree's Pi Web processes (nil:
	// the /proc scan).
	RestartPiWeb func() error
}

// Rollback swaps current and previous under the install lock — the
// drain and the restarts of the upgrade included. A refusal answers
// the error with nothing changed; a cancel answers nil with the
// status file's cancelled record.
func Rollback(ctx context.Context, opts RollbackOptions) (*UpgradeResult, error) {
	_ = ctx
	_ = opts
	return nil, errRollbackNotImplemented
}

// errRollbackNotImplemented is the zero-value stub's answer, so the
// first red is an assertion failure, never a compile error.
var errRollbackNotImplemented = errors.New("the rollback is not implemented")
