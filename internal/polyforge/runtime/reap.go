// reap.go: orphan reaping after a daemon crash (v2-converge §6 bug 3).
//
// A kill -9'd daemon leaves the step processes the Go driver spawned (pi,
// its own process-group leader) and their children running: the job record
// carries agent pid 0 (the driver handle has no pid) so killAgentTree had
// nothing to signal, and the orphans kept writing artifacts/ and repos/
// next to the re-dispatched step (#99 double-writer). reapOrphans closes
// that gap from two independent directions:
//
//  1. the capsule spawn journal (runtime-state/spawn-journal.jsonl) names
//     every step pid the daemon ever spawned for the WI; each pid whose
//     identity still verifies is torn down through the six-layer Cleanup
//     (its process group, TERM -> bounded wait -> KILL);
//  2. one pid-less Cleanup sweep: layer 4's PF_WI_ID + PF_DAEMON_ID env-marker scan
//     catches whatever the journal could not name (torn journal, escapees
//     that left the group).
package runtime

import (
	"fmt"
	"os"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

type journalPID struct {
	pid       int
	startedAt string
}

// journalPIDs reads every distinct (pid, started_at) the capsule spawn
// journal records. The reader is loose: unparsable lines are skipped.
func journalPIDs(capsuleDir string) []journalPID {
	if capsuleDir == "" {
		return nil
	}
	entries, _ := capsule.ReadSpawnJournal(capsuleDir)
	var out []journalPID
	seen := map[journalPID]bool{}
	for _, e := range entries {
		if e.PID <= 1 {
			continue
		}
		k := journalPID{e.PID, e.StartedAt}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// pidHasWIMarker reports whether pid's environment carries both
// PF_WI_ID=wi and PF_DAEMON_ID=daemonID.
func pidHasWIMarker(pid int, wi, daemonID string) bool {
	envs, err := procEnviron(pid)
	if err != nil {
		return false
	}
	return envHasMarkers(envs, wi, daemonID)
}

// journalPIDIsOurs is the pid-reuse guard. A journal pid is signaled only
// when it is gone (its group may still hold orphans; Cleanup's group check
// then requires every member to postdate the recorded start) or when the
// live occupant provably is the recorded process: its starttime matches
// the journaled started_at, or - for a line with no usable stamp - it
// carries this WI's PF_WI_ID and this daemon's PF_DAEMON_ID markers. Never the daemon itself.
func journalPIDIsOurs(jp journalPID, wi, daemonID string) bool {
	if jp.pid == os.Getpid() {
		return false
	}
	if _, _, _, _, ok := procStat(jp.pid); !ok {
		return true
	}
	if jp.startedAt != "" {
		return rootIdentityHolds(CleanupTarget{PID: jp.pid, StartedAt: jp.startedAt})
	}
	return pidHasWIMarker(jp.pid, wi, daemonID)
}

// reapOrphans kills every leftover step process tree of one WI: the
// journaled step pids (identity-verified) and then an env-marker sweep that
// needs no recorded pid; then it removes the index locks the killed
// processes may have left. Safe to call when nothing is left (a no-op) and
// when the WI has no capsule yet. Failures are logged, never fatal: the
// caller's re-dispatch proceeds either way.
func (r *Runtime) reapOrphans(wi, capsuleDir string) {
	reaped := true
	for _, jp := range journalPIDs(capsuleDir) {
		if !journalPIDIsOurs(jp, wi, r.cfg.MachineID) {
			fmt.Fprintf(os.Stderr, "runtime: %s: journal pid %d no longer matches its recorded identity (pid reuse); not signaled\n", wi, jp.pid)
			continue
		}
		// WIID "" here: the env sweep runs once, below.
		if rep, err := Cleanup(CleanupTarget{
			PID: jp.pid, Pgid: jp.pid, StartedAt: jp.startedAt,
			CapsuleDir: capsuleDir,
		}, cleanupBound); err != nil {
			reaped = false
			fmt.Fprintf(os.Stderr, "runtime: %s: orphan step tree %d cleanup incomplete: %v (warnings: %v)\n", wi, jp.pid, err, rep.Warnings)
		}
	}
	if rep, err := Cleanup(CleanupTarget{WIID: wi, DaemonID: r.cfg.MachineID, CapsuleDir: capsuleDir}, cleanupBound); err != nil {
		reaped = false
		fmt.Fprintf(os.Stderr, "runtime: %s: orphan env-marker sweep incomplete: %v (warnings: %v)\n", wi, err, rep.Warnings)
	}
	// A step or git process killed mid-command leaves its index.lock
	// behind; with the WI's processes gone it guards nothing, and left in
	// place it would fail every commit and push until the age bound. An
	// incomplete reap leaves the locks to that bound.
	if !reaped || r.cfg.DataDir == "" {
		return
	}
	for _, p := range gitops.ClearIndexLocks(wi, r.gitDirFor(wi), capsuleDir) {
		fmt.Fprintf(os.Stderr, "runtime: %s: removed index lock %s left by a dead process\n", wi, p)
	}
}

// spawnJournalMaxN is the B2 fresh-clone counter seed: the highest
// spawn_n the capsule journal records (0 when absent or unreadable).
func spawnJournalMaxN(capsuleDir string) int {
	n, _ := capsule.SpawnJournalMaxN(capsuleDir)
	return n
}
