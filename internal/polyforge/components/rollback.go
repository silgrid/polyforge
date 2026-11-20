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
	"fmt"
	"os"
	"path/filepath"
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
	if opts.Root == "" {
		return nil, fmt.Errorf("components rollback: Root is required")
	}
	if opts.Face == nil {
		return nil, fmt.Errorf("components rollback: the daemon face is required")
	}
	if opts.Poll <= 0 {
		opts.Poll = drainPollDefault
	}

	// ---- the previous version is judged in full before anything runs:
	// every refusal names its reason and changes nothing at all — not
	// the pointers, not the daemon (the drain is never set). The
	// status file carries the refusal too, so the page that asked
	// shows it without another command.
	refuse := func(err error) (*UpgradeResult, error) {
		_ = WriteUpgradeStatus(opts.Root, &UpgradeStatus{Operation: OperationRollback, State: UpgradeStateFailed,
			Phase: "refuse", Error: err.Error()})
		return nil, err
	}
	curTarget, err := CurrentTarget(opts.Root)
	if err != nil {
		return refuse(err)
	}
	if curTarget == "" {
		return refuse(fmt.Errorf("components rollback: the root carries no current: there is nothing to roll back from"))
	}
	curManifest, err := ReadManifest(filepath.Join(opts.Root, curTarget))
	if err != nil {
		return refuse(fmt.Errorf("components rollback: %w", err))
	}
	prevLink := filepath.Join(opts.Root, PreviousLinkname)
	prevTarget, err := os.Readlink(prevLink)
	if err != nil {
		if os.IsNotExist(err) {
			return refuse(fmt.Errorf("components rollback: there is no previous to roll back to (the first install leaves none, and a rollback consumes nothing)"))
		}
		return refuse(err)
	}
	prevDir := filepath.Join(opts.Root, prevTarget)
	prevManifest, err := ReadManifest(prevDir)
	if err != nil {
		return refuse(fmt.Errorf("components rollback: the previous version: %w", err))
	}
	if err := prevManifest.VerifyTree(prevDir); err != nil {
		return refuse(fmt.Errorf("components rollback: the previous version's tree does not match its manifest (%w): refusing to roll back onto a corrupt version — re-run the install of that version, or fix the tree by hand", err))
	}
	// the channel is the version's own line (public, -gmi.N, -dev.N),
	// never the repository: both lines of one repository are two
	// channels, and an implicit switch between them is refused exactly
	// like the install transaction refuses it.
	curVerParsed, err := ParseVersion(curManifest.Version)
	if err != nil {
		return refuse(fmt.Errorf("components rollback: the current version: %w", err))
	}
	prevVerParsed, err := ParseVersion(prevManifest.Version)
	if err != nil {
		return refuse(fmt.Errorf("components rollback: the previous version: %w", err))
	}
	if curVerParsed.Channel != prevVerParsed.Channel {
		return refuse(fmt.Errorf("components rollback: refusing the cross-channel switch: current is %s (%s channel), the previous is %s (%s channel)", curManifest.Version, curVerParsed.Channel, prevManifest.Version, prevVerParsed.Channel))
	}
	if ok, err := BaseSatisfies(opts.BaseVersion, prevManifest.MinBase); err != nil {
		return refuse(fmt.Errorf("components rollback: the previous version's minimum base requirement: %w", err))
	} else if !ok {
		return refuse(fmt.Errorf("components rollback: refusing the downgrade protection: the base runs %s, the previous version requires %s — the image's bootstrap pf cannot run it (upgrading the base is an image change)", opts.BaseVersion, prevManifest.MinBase))
	}
	if prevManifest.SchemaVersion != SchemaVersion {
		// ReadManifest already refuses a schema it cannot read; this is
		// the belt to the braces: a future reader that grows a
		// compatibility window states its own rule here.
		return refuse(fmt.Errorf("components rollback: the previous version's manifest carries schema version %d, this build reads %d — the version crossed a schema migration this build cannot judge", prevManifest.SchemaVersion, SchemaVersion))
	}
	prevVer := prevManifest.Version
	curVer := curManifest.Version
	if prevVer == curVer {
		return refuse(fmt.Errorf("components rollback: previous and current name the same version %s: there is nothing to roll back to", curVer))
	}

	record := func(state, errText string) {
		_ = WriteUpgradeStatus(opts.Root, &UpgradeStatus{Operation: OperationRollback, State: state,
			Phase: "drain", From: curVer, To: prevVer, Error: errText})
	}
	failed := func(phase string, err error) (*UpgradeResult, error) {
		_ = WriteUpgradeStatus(opts.Root, &UpgradeStatus{Operation: OperationRollback, State: UpgradeStateFailed,
			Phase: phase, From: curVer, To: prevVer, Error: err.Error()})
		return nil, err
	}

	// ---- the same drain the upgrade takes.
	record(UpgradeStateRunning, "")
	cancelled, err := drainAndWait(ctx, &drainWait{
		root: opts.Root, face: opts.Face, poll: opts.Poll, force: opts.Force,
		operation: OperationRollback, from: curVer, to: prevVer,
	})
	if err != nil {
		return failed("drain", err)
	}
	if cancelled {
		return &UpgradeResult{From: curVer, To: curVer}, nil
	}

	// ---- the swap, under the install lock and the journal's own
	// recovery rules: an interruption at any point leaves both
	// pointers on a complete version and explainable.
	_ = WriteUpgradeStatus(opts.Root, &UpgradeStatus{Operation: OperationRollback, State: UpgradeStateRunning,
		Phase: "switch", From: curVer, To: prevVer})
	lock, err := holdLock(opts.Root)
	if err != nil {
		return failed("switch", err)
	}
	defer lock.Close()
	if err := recoverPending(opts.Root); err != nil {
		return failed("switch", err)
	}
	if err := writeJournal(opts.Root, &journal{SchemaVersion: journalSchemaVersion, NewVersion: prevVer, OldVersion: curVer}); err != nil {
		return failed("switch", err)
	}
	if err := swapLink(opts.Root, CurrentLinkname, filepath.Join(ReleasesDirname, prevVer)); err != nil {
		return failed("switch", err)
	}
	if err := swapLink(opts.Root, PreviousLinkname, filepath.Join(ReleasesDirname, curVer)); err != nil {
		return failed("switch", err)
	}
	if err := removeJournal(opts.Root); err != nil {
		return failed("switch", err)
	}

	// ---- the same restarts: the daemon relaunches on the new current
	// (exit 75), and the tree current left is terminated so the
	// supervisor relaunches Pi Web on the new one.
	_ = WriteUpgradeStatus(opts.Root, &UpgradeStatus{Operation: OperationRollback, State: UpgradeStateRunning,
		Phase: "restart", From: curVer, To: prevVer})
	if err := opts.Face.Restart(ctx); err != nil {
		return failed("restart", fmt.Errorf("the swap is complete but the daemon could not be asked to restart (it still runs the old version until it does): %w", err))
	}
	termPiWeb := opts.RestartPiWeb
	if termPiWeb == nil {
		termPiWeb = func() error { return termPiWebUnder(opts.Root) }
	}
	if err := termPiWeb(); err != nil {
		return failed("restart", fmt.Errorf("the old Pi Web tree's processes could not be terminated: %w", err))
	}
	_ = WriteUpgradeStatus(opts.Root, &UpgradeStatus{Operation: OperationRollback, State: UpgradeStateDone,
		Phase: "restart", From: curVer, To: prevVer})
	return &UpgradeResult{From: curVer, To: prevVer}, nil
}
