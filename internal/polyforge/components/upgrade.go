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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// drainPollDefault is the drain wait's re-ask interval.
const drainPollDefault = 5 * time.Second

// Upgrade runs the whole transaction. A refusal — a failed fetch or
// assembly, a refused seed, a transport failure — answers the error
// with the machine untouched (the status file records it); a cancel
// answers nil with the status file's cancelled record.
func Upgrade(ctx context.Context, opts UpgradeOptions) (*UpgradeResult, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("components upgrade: Root is required")
	}
	if opts.Face == nil {
		return nil, fmt.Errorf("components upgrade: the daemon face is required")
	}
	if opts.Poll <= 0 {
		opts.Poll = drainPollDefault
	}
	cur, _, err := currentManifestOf(opts.Root)
	if err != nil {
		return nil, err
	}
	record := func(state, phase, to string, errText string) {
		s := &UpgradeStatus{Operation: OperationUpgrade, State: state, Phase: phase,
			From: cur.Version, To: to, Error: errText}
		_ = WriteUpgradeStatus(opts.Root, s)
	}
	failed := func(phase string, err error) (*UpgradeResult, error) {
		record(UpgradeStateFailed, phase, "", err.Error())
		return nil, err
	}

	// ---- stage: the source becomes one complete version directory,
	// verified against its own manifest. Nothing of the machine is
	// touched by any of it.
	record(UpgradeStateRunning, "stage", opts.Version, "")
	seed := opts.SeedDir
	if seed == "" {
		// the assembly's temporary directory lives in the volume's own
		// root (one filesystem with releases/, sized for the tree it
		// holds) and never survives the run: the deferred removal is
		// the ordinary path, the next run's sweep the crashed one.
		sweepStaleAssembly(opts.Root)
		dir, err := os.MkdirTemp(opts.Root, ".upgrade-")
		if err != nil {
			return failed("stage", err)
		}
		defer os.RemoveAll(dir)
		version := opts.Version
		if version == "" {
			latest, err := LatestRelease(ctx, cur.ChannelRepo)
			if err != nil {
				return failed("stage", err)
			}
			version = latest.Tag
		}
		assets, err := os.MkdirTemp(dir, "assets")
		if err != nil {
			return failed("stage", err)
		}
		info, err := FetchRelease(ctx, assets, cur.ChannelRepo, version, platformArch(opts.Platform))
		if err != nil {
			return failed("stage", err)
		}
		checkout, err := os.MkdirTemp(dir, "checkout")
		if err != nil {
			return failed("stage", err)
		}
		if err := FetchReleaseCheckout(ctx, checkout, cur.ChannelRepo, version); err != nil {
			return failed("stage", err)
		}
		assembledSeed := filepath.Join(dir, "seed")
		if _, err := AssembleVersionDir(AssembleOptions{
			Root: opts.Root, Dest: assembledSeed, Version: version, Commit: info.Commit,
			Platform: opts.Platform, AssetsDir: assets, CheckoutDir: checkout,
			NPMInstall: opts.NPMInstall,
		}); err != nil {
			return failed("stage", err)
		}
		seed = assembledSeed
	} else if _, err := ReadManifest(seed); err != nil {
		// an explicit seed is judged in place before anything runs: a
		// bad seed never reaches the drain.
		return failed("stage", err)
	}
	seedManifest, err := ReadManifest(seed)
	if err != nil {
		return failed("stage", fmt.Errorf("the seed: %w", err))
	}
	if err := seedManifest.VerifyTree(seed); err != nil {
		return failed("stage", fmt.Errorf("%w: the seed at %s", ErrDigestMismatch, seed))
	}

	// ---- drain: the Host refuses new work from here on. The wait
	// that follows re-asks until the work ends, and a cancel marker
	// stands the run down.
	record(UpgradeStateRunning, "drain", seedManifest.Version, "")
	cancelled, err := drainAndWait(ctx, &drainWait{
		root: opts.Root, face: opts.Face, poll: opts.Poll, force: opts.Force,
		operation: OperationUpgrade, from: cur.Version, to: seedManifest.Version,
	})
	if err != nil {
		return failed("drain", err)
	}
	if cancelled {
		return &UpgradeResult{From: cur.Version, To: cur.Version}, nil
	}

	// ---- switch: the install transaction's own commit point.
	record(UpgradeStateRunning, "switch", seedManifest.Version, "")
	res, err := Install(Options{
		Root: opts.Root, Seed: seed, DataDir: opts.DataDir,
		BaseVersion: opts.BaseVersion, Platform: opts.Platform,
	})
	if err != nil {
		return failed("switch", err)
	}

	// ---- restart: the daemon relaunches on the new current (its exit
	// 75), and the old tree's Pi Web is terminated so the supervisor
	// relaunches it the same way.
	record(UpgradeStateRunning, "restart", res.Version, "")
	if err := opts.Face.Restart(ctx); err != nil {
		return failed("restart", fmt.Errorf("the switch is complete but the daemon could not be asked to restart (it still runs the old version until it does): %w", err))
	}
	termPiWeb := opts.RestartPiWeb
	if termPiWeb == nil {
		termPiWeb = func() error { return termPiWebUnder(opts.Root) }
	}
	if err := termPiWeb(); err != nil {
		return failed("restart", fmt.Errorf("the old Pi Web tree's processes could not be terminated: %w", err))
	}
	record(UpgradeStateDone, "restart", res.Version, "")
	return &UpgradeResult{From: res.From, To: res.Version}, nil
}

// sweepStaleAssembly removes the assembly directories a crashed run
// left in the root (the ".*upgrade-*" temporaries of this package; the
// install lock's own staging/ is the install transaction's to clean).
func sweepStaleAssembly(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".upgrade-") {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// termPiWebUnder terminates every process whose working directory sits
// inside a version directory's pi-web tree of the given components
// root — the old tree's Pi Web, whose supervisor relaunch is the new
// current's. The judgment is the kernel's own record (/proc/<pid>/cwd,
// resolved by chdir when the process started): the tree a process
// serves out of is where it sits, never a command line to parse.
func termPiWebUnder(root string) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		// no /proc to read: there is nothing this mechanism can see,
		// and the honest answer is that, not an error.
		return nil
	}
	self := os.Getpid()
	releasePrefix := filepath.Join(root, ReleasesDirname) + string(os.PathSeparator)
	for _, e := range entries {
		pid, err := parsePid(e.Name())
		if err != nil || pid == self {
			continue
		}
		exe, exeErr := os.Readlink(filepath.Join("/proc", e.Name(), "exe"))
		if exeErr == nil && strings.HasPrefix(exe, releasePrefix) {
			argv, readErr := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
			if readErr == nil && strings.Contains(string(argv), "\x00aihub\x00") {
				if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
					return fmt.Errorf("restart AIHub %d: %w", pid, err)
				}
				continue
			}
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err != nil {
			continue // a process whose cwd cannot be read is not ours to judge
		}
		if !strings.HasPrefix(cwd, releasePrefix) {
			continue
		}
		if !insidePiWebOfRelease(cwd, releasePrefix) {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			return fmt.Errorf("SIGTERM %d (serving %s): %w", pid, cwd, err)
		}
	}
	return nil
}

// insidePiWebOfRelease answers whether the resolved cwd sits inside
// <root>/releases/<ver>/pi-web.
func insidePiWebOfRelease(cwd, releasePrefix string) bool {
	rel, err := filepath.Rel(releasePrefix, cwd)
	if err != nil {
		return false
	}
	parts := strings.SplitN(rel, string(os.PathSeparator), 3)
	return len(parts) >= 2 && parts[1] == PathPiWeb
}

// parsePid answers the numeric pid a /proc entry name is, or an error.
func parsePid(name string) (int, error) {
	var pid int
	for _, c := range name {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a pid: %s", name)
		}
		pid = pid*10 + int(c-'0')
	}
	if pid == 0 {
		return 0, fmt.Errorf("not a pid: %s", name)
	}
	return pid, nil
}

// drainWait is the drain-and-wait half the upgrade and the rollback
// share (the ticket's Q6 ruling: the rollback goes through the same
// drain).
type drainWait struct {
	root      string
	face      DaemonFace
	poll      time.Duration
	force     bool
	operation string
	from, to  string
}

// drainAndWait sets the Host's upgrade drain, waits out the running
// work (re-asking until it ends; --force skips the wait) and answers
// whether the run was stood down by the cancel marker: the daemon was
// asked to restart (the drain ends with that restart), the marker was
// consumed and the status file carries the cancelled record. A caller
// that went away leaves the drain set (the documented recovery is the
// daemon restart) and answers its own context's error.
func drainAndWait(ctx context.Context, d *drainWait) (bool, error) {
	if d.poll <= 0 {
		d.poll = drainPollDefault
	}
	record := func(state, errText string) {
		_ = WriteUpgradeStatus(d.root, &UpgradeStatus{Operation: d.operation, State: state,
			Phase: "drain", From: d.from, To: d.to, Error: errText})
	}
	busy, err := d.face.Drain(ctx, d.force)
	if err != nil {
		return false, err
	}
	for len(busy) > 0 && !d.force {
		record(UpgradeStateWaiting,
			fmt.Sprintf("waiting for the running work of Cell(s) %s to end; new work is refused while the drain is set", strings.Join(busy, ", ")))
		select {
		case <-ctx.Done():
			record(UpgradeStateFailed, ctx.Err().Error())
			return false, ctx.Err()
		case <-time.After(d.poll):
		}
		if UpgradeCancelRequested(d.root) {
			if err := ClearUpgradeCancel(d.root); err != nil {
				return false, err
			}
			if err := d.face.Restart(ctx); err != nil {
				return false, fmt.Errorf("standing the run down asks the daemon to restart (the drain ends with that restart): %w", err)
			}
			record(UpgradeStateCancelled, "")
			return true, nil
		}
		busy, err = d.face.Drain(ctx, d.force)
		if err != nil {
			return false, err
		}
	}
	return false, nil
}
