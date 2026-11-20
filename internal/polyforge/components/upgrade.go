package components

// upgrade.go (polyforge#190, 84-inplace-upgrade): the in-container
// upgrade transaction — the one orchestration the CLI verb and Pi Web
// both drive. Its settled shape (the ticket's Q3/Q4/Q5 rulings):
//
//   stage    fetch the channel's release, verify it against its own
//            SHA256SUMS, assemble the complete version directory —
//            nothing of the machine is touched yet;
//   drain    set the Host's upgrade drain (the running work goes on,
//            new work is refused DAEMON-409), wait for the Host to go
//            idle and continue on its own; --force skips the wait; a
//            cancel marker laid in the wait stands the run down: the
//            daemon is asked to restart (the drain ends with that
//            restart) and nothing else changes;
//   switch   the install transaction's own stage/verify/publish/swap
//            (polyforge#162) — current is the single commit point;
//   restart  ask the daemon for the restart-for-an-upgrade (its exit
//            75, polyforge#175: the supervisor relaunches it at once
//            on the new current) and terminate the Pi Web processes
//            serving the old tree — the supervisor relaunches them
//            the same way.
//
// Every phase change is written to the volume's status file: Pi Web
// reads it across its own restart, so the page survives the upgrade
// of the very process that serves it.

import (
	"context"
	"time"
)

// DaemonFace is the daemon's face the orchestration drives — the two
// calls it needs: the upgrade drain (POST /ops/drain, polyforge#160)
// and the restart-for-an-upgrade ask (POST /ops/restart, this ticket).
// The CLI provides the real implementation over the daemon socket;
// the tests provide the fake.
type DaemonFace interface {
	// Drain sets the Host's upgrade drain and answers which Cells
	// still hold work: a non-empty answer means the drain is set and
	// the Host is busy (the caller waits and asks again); an empty
	// answer means the Host is idle (or force accepted the work) and
	// the upgrade may switch. The error is a transport failure, never
	// a busy Host.
	Drain(ctx context.Context, force bool) (busy []string, err error)
	// Restart asks the daemon to shut down for the restart-for-an-upgrade
	// (it answers at once and exits 75 once its shutdown completes).
	Restart(ctx context.Context) error
}

// UpgradeOptions is one upgrade's inputs.
type UpgradeOptions struct {
	// Root is the components volume's root.
	Root string
	// DataDir is the machine's data directory (the install
	// transaction's own input: the pre-#162 layout is refused there).
	DataDir string
	// BaseVersion is the running base's (bootstrap pf's) version, for
	// the seed's minimum base requirement.
	BaseVersion string
	// Platform is the running platform (os/arch).
	Platform string
	// Version is the release to upgrade to (the tag); "" resolves the
	// channel's latest formal release.
	Version string
	// Force passes the drain its force (in-flight work is interrupted
	// and resumes after the restart).
	Force bool
	// SeedDir names a complete local version directory as the source
	// (the operator and smoke path, no channel fetch); "" fetches the
	// channel repository's release assets.
	SeedDir string
	// Face is the daemon face (required).
	Face DaemonFace
	// Poll is the drain wait's re-ask interval (<= 0: 5s).
	Poll time.Duration
	// NPMInstall is the assembly's dependency seam (nil: the real npm).
	NPMInstall func(dir string) error
	// RestartPiWeb terminates the old tree's Pi Web processes (nil:
	// the /proc scan).
	RestartPiWeb func() error
}

// UpgradeResult is what one upgrade did.
type UpgradeResult struct {
	From string
	To   string
}

// Upgrade runs the whole transaction. A refusal — a failed fetch or
// assembly, a refused seed, a transport failure — answers the error
// with the machine untouched (the status file records it); a cancel
// answers nil with the status file's cancelled record.
func Upgrade(ctx context.Context, opts UpgradeOptions) (*UpgradeResult, error) {
	_ = ctx
	_ = opts
	return nil, errUpgradeNotImplemented
}

// termPiWebUnder terminates every process whose working directory sits
// inside a version directory's pi-web tree of the given components
// root — the old tree's Pi Web, whose supervisor relaunch is the new
// current's.
func termPiWebUnder(root string) error {
	_ = root
	return errUpgradeNotImplemented
}

// errUpgradeNotImplemented is the zero-value stub's answer, so the
// first red is an assertion failure, never a compile error.
var errUpgradeNotImplemented = notImplementedError("the upgrade transaction")

// notImplementedError names the stub.
type notImplementedError string

func (e notImplementedError) Error() string { return "not implemented: " + string(e) }
