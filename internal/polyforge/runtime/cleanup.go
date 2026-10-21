// cleanup.go: the layered process cleanup (reference.md §6.2, minus its
// layer-0 cgroup kill). One WI's agent tree — the agent itself, its step
// agents and command children — is wiped with escalating precision,
// because a surviving orphan keeps writing the capsule next to the
// re-derived agent (single-writer domain).
//
//	L1 PPID snapshot: walk the process list (procfs on Linux, the
//	   kern.proc.all sysctl on darwin) and snapshot the FULL descendant
//	   set BEFORE any signal (100% precise while the tree is intact).
//	L2 process group: kill(-pgid, TERM→KILL) — one signal collects the
//	   group the agent was made leader of at spawn.
//	L3 identity supplementary kill: the snapshot's (pid, starttime)
//	   pairs re-kill survivors, refusing to signal a pid whose starttime
//	   no longer matches (PID-reuse mis-kill protection).
//	L4 env marker scan: processes whose environment (procfs environ on
//	   Linux, kern.procargs2 on darwin) carries PF_WI_ID=<wi> — catches
//	   double-forked escapees that left the process group.
//	L5 cwd scan: processes whose cwd sits inside the capsule are
//	   reported as WARNINGS only, never killed (an operator's shell
//	   cd'd into the capsule must survive).
package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	// killFn is the signal(2) face; a variable so EPERM seams are testable.
	killFn = syscall.Kill
	// sleepFn is the inter-poll pause of the bounded waits.
	sleepFn = time.Sleep
)

// procIdent is one (pid, starttime) identity pair — L3's mis-kill guard.
type procIdent struct {
	pid   int
	start int64 // ticks since boot on the 100 Hz clock (Linux: /proc/<pid>/stat field 22; darwin: P_starttime − kern.boottime, procinfo_darwin.go)
}

// CleanupTarget names one WI's agent tree.
type CleanupTarget struct {
	PID       int
	Pgid      int // 0 ⇒ use PID (the agent spawns as its own group leader)
	StartedAt string
	WIID      string
	// DaemonID is the second env-marker half (PF_DAEMON_ID): the L4 sweep
	// kills only processes carrying BOTH this WI's and this daemon's
	// markers, so a daemon whose WI moved to another daemon on the same
	// host never reaps that daemon's live step process.
	DaemonID   string
	CapsuleDir string
}

// CleanupReport records what each layer did.
type CleanupReport struct {
	Layers    []string
	Warnings  []string
	Killed    []int
	Survivors []int
}

// ErrCleanupIncomplete is returned when processes provably belonging to the
// WI survive every layer.
var ErrCleanupIncomplete = errors.New("runtime: process cleanup left survivors")

// Cleanup runs the kill layers over one target and returns the report. The
// bound bounds every bounded wait inside.
func Cleanup(t CleanupTarget, bound time.Duration) (*CleanupReport, error) {
	rep := &CleanupReport{}
	if t.Pgid == 0 {
		t.Pgid = t.PID
	}
	rootOurs := t.PID <= 0 || rootIdentityHolds(t)
	if !rootOurs {
		rep.Warnings = append(rep.Warnings,
			fmt.Sprintf("recorded pid %d no longer matches its creation time (pid reuse); root/group signals skipped, relying on identity-marked layers", t.PID))
	}

	// L1 — PPID snapshot of the full descendant set, BEFORE any signal.
	var snap []procIdent
	if rootOurs {
		rep.Layers = append(rep.Layers, "l1-ppid-snapshot")
		snap = snapshotDescendants(t.PID)
		if t.Pgid <= 0 && len(snap) > 0 {
			// no group to signal: the snapshot itself is the kill set
			for _, id := range snap {
				signalIdent(id, syscall.SIGTERM, rep)
			}
			waitIdents(snap, bound)
			for _, id := range snap {
				signalIdent(id, syscall.SIGKILL, rep)
			}
		}
	}

	// L2 — process group signal. Skipped when the root identity failed:
	// an innocent group may now carry the number.
	if rootOurs && t.Pgid > 0 {
		if !groupProvableOurs(t) {
			rep.Warnings = append(rep.Warnings,
				fmt.Sprintf("group %d cannot be proven ours (leader gone, member predates the agent); group signal skipped", t.Pgid))
		} else {
			rep.Layers = append(rep.Layers, "l2-pgid")
			if err := killGroup(t.Pgid, bound); err != nil {
				rep.Warnings = append(rep.Warnings, "layer 2 group kill: "+err.Error())
			}
		}
	}

	// L3 — identity supplementary kill from the snapshot: survivors whose
	// (pid, starttime) still match are killed; a mismatched starttime is
	// proof of PID reuse and is skipped with a warning.
	if len(snap) > 0 {
		rep.Layers = append(rep.Layers, "l3-identity")
		for _, id := range snap {
			cur, ok := procIdentOf(id.pid)
			if !ok {
				continue // gone
			}
			if cur.start != id.start {
				rep.Warnings = append(rep.Warnings,
					fmt.Sprintf("pid %d reused (starttime %d ≠ snapshot %d); not signaled", id.pid, cur.start, id.start))
				continue
			}
			signalIdent(cur, syscall.SIGKILL, rep)
		}
		waitIdents(snap, bound)
	}

	// L4 — env marker scan.
	if t.WIID != "" {
		rep.Layers = append(rep.Layers, "l4-env-marker")
		marked := scanEnvMarker(t.WIID, t.DaemonID)
		for _, pid := range marked {
			signalPID(pid, syscall.SIGTERM, rep)
		}
		deadline := time.Now().Add(bound)
		for _, pid := range marked {
			for procExists(pid) && time.Now().Before(deadline) {
				sleepFn(20 * time.Millisecond)
			}
			if procExists(pid) {
				signalPID(pid, syscall.SIGKILL, rep)
			}
		}
	}

	// L5 — cwd scan: warnings only, never a kill.
	rep.Layers = append(rep.Layers, "l5-cwd-scan")
	cwdScan(t, rep)

	// Verify: snapshot identities and env-marked pids still alive.
	if rootOurs {
		for _, id := range snap {
			if cur, ok := procIdentOf(id.pid); ok && cur.start == id.start {
				rep.Survivors = append(rep.Survivors, id.pid)
			}
		}
	}
	if t.WIID != "" {
		for _, pid := range scanEnvMarker(t.WIID, t.DaemonID) {
			rep.Survivors = append(rep.Survivors, pid)
		}
	}
	if len(rep.Survivors) > 0 {
		return rep, fmt.Errorf("%w: %v", ErrCleanupIncomplete, rep.Survivors)
	}
	return rep, nil
}

// ---------------------------------------------------------------------------
// proc helpers (the per-OS readers live in procinfo_linux.go /
// procinfo_darwin.go — the proc seam)

func procIdentOf(pid int) (procIdent, bool) {
	if pid <= 0 || !procExists(pid) {
		return procIdent{}, false
	}
	_, _, _, start, ok := procStat(pid)
	if !ok {
		return procIdent{}, false
	}
	return procIdent{pid: pid, start: start}, true
}

// procExists reports whether pid has a live (non-zombie) process entry.
func procExists(pid int) bool {
	state, _, _, _, ok := procStat(pid)
	if !ok {
		return false
	}
	return state != "Z"
}

// bootTime is the wall-clock anchor for starttime comparisons; the
// per-OS reader (procinfo_linux.go/procinfo_darwin.go) reads it once —
// it never changes while the machine is up.
var bootOnce = sync.OnceValue(readBootTime)

// rootIdentityHolds reports whether the recorded pid is still OUR process:
// alive, or its recorded creation time still matches an un-reaped corpse.
// A mismatched live occupant is pid reuse — the tree it roots cannot be
// proven ours.
func rootIdentityHolds(t CleanupTarget) bool {
	if t.PID <= 0 {
		return true // nothing recorded: vacuously ours
	}
	_, _, _, start, ok := procStat(t.PID)
	if !ok {
		return true // gone (and reaped): the group id may still hold orphans
	}
	if t.StartedAt == "" {
		return true // no recorded identity: nothing to compare against
	}
	rec, err := time.Parse(time.RFC3339Nano, t.StartedAt)
	if err != nil {
		return true
	}
	bt := bootOnce()
	if bt == 0 {
		return true // boot time unreadable: fall back to pid existence
	}
	startWall := bt + start/100
	recUnix := rec.Unix()
	// a live occupant and a corpse alike carry their own creation time
	return startWall > recUnix-2 && startWall < recUnix+2
}

// snapshotDescendants walks the process list once and returns the full descendant set
// of root (including root itself) with each member's starttime — L1's
// pre-signal snapshot.
func snapshotDescendants(root int) []procIdent {
	if root <= 0 {
		return nil
	}
	children := map[int64][]int{}
	pids, err := procListAll()
	if err != nil {
		return nil
	}
	idents := map[int]procIdent{}
	for _, pid := range pids {
		state, ppid, _, start, ok := procStat(pid)
		if !ok || state == "Z" {
			continue
		}
		children[ppid] = append(children[ppid], pid)
		idents[pid] = procIdent{pid: pid, start: start}
	}
	var out []procIdent
	queue := []int{root}
	seen := map[int]bool{}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if id, ok := idents[pid]; ok {
			out = append(out, id)
			queue = append(queue, children[int64(pid)]...)
		}
	}
	return out
}

// signalIdent signals one identity — the direct expression of L3's rule:
// a pid whose current occupant does not match the snapshot is NEVER
// signaled.
func signalIdent(id procIdent, sig syscall.Signal, rep *CleanupReport) {
	cur, ok := procIdentOf(id.pid)
	if !ok {
		return
	}
	if cur.start != id.start {
		return // pid reuse: refuse the signal
	}
	if err := killFn(id.pid, sig); err == nil && sig == syscall.SIGKILL {
		rep.Killed = append(rep.Killed, id.pid)
	}
}

func signalPID(pid int, sig syscall.Signal, rep *CleanupReport) {
	if err := killFn(pid, sig); err == nil && sig == syscall.SIGKILL {
		rep.Killed = append(rep.Killed, pid)
	}
}

func waitIdents(ids []procIdent, bound time.Duration) {
	deadline := time.Now().Add(bound)
	for {
		live := 0
		for _, id := range ids {
			if cur, ok := procIdentOf(id.pid); ok && cur.start == id.start {
				live++
			}
		}
		if live == 0 || time.Now().After(deadline) {
			return
		}
		sleepFn(20 * time.Millisecond)
	}
}

// killGroup signals the process group TERM→bounded wait→KILL→wait and
// proves the group empty (§八 layer 2; also the agent-handle teardown).
func killGroup(pgid int, bound time.Duration) error {
	if pgid <= 0 {
		return nil
	}
	_ = killFn(-pgid, syscall.SIGTERM)
	if !waitGroupEmpty(pgid, bound) {
		_ = killFn(-pgid, syscall.SIGKILL)
		if !waitGroupEmpty(pgid, 5*time.Second) {
			return fmt.Errorf("process group %d refused to die", pgid)
		}
	}
	return nil
}

// groupProvableOurs guards the L2 group signal against pgid reuse. With a
// LIVE leader whose identity holds, the group is ours by construction. With
// the leader gone, the group id is just a number the kernel may have handed
// to a new session: every live member must have been created no earlier
// than the recorded agent start (a member predating the agent cannot be
// its child — provably an innocent group). An unparseable/absent record
// falls back to the group signal (the residue risk outranks); an
// unreadable process list fails closed (layer 4's env marker remains).
func groupProvableOurs(t CleanupTarget) bool {
	if t.PID > 0 {
		if _, _, _, _, ok := procStat(t.PID); ok {
			return true // live leader: the group id is still ours
		}
	}
	if t.StartedAt == "" {
		return true // no record to compare against: fall back to the signal
	}
	rec, err := time.Parse(time.RFC3339Nano, t.StartedAt)
	if err != nil {
		return true
	}
	bt := bootOnce()
	if bt == 0 {
		return false // member birth times unverifiable: fail closed
	}
	floor := rec.Unix() - 2
	pids, err := procListAll()
	if err != nil {
		return false
	}
	for _, pid := range pids {
		state, _, pgrp, start, ok := procStat(pid)
		if !ok || state == "Z" || int(pgrp) != t.Pgid {
			continue
		}
		if bt+start/100 < floor {
			return false // a live member predates the agent: innocent group
		}
	}
	return true
}

// groupEmpty reports whether NO live member remains in the group. Fails
// closed: an unreadable process list makes emptiness unprovable and answers
// false.
func groupEmpty(pgid int) bool {
	pids, err := procListAll()
	if err != nil {
		return false
	}
	for _, pid := range pids {
		state, _, pgrp, _, ok := procStat(pid)
		if !ok {
			// A still-present pid with an unreadable stat makes the whole
			// answer unprovable — never fail open.
			if procStillListed(pid) {
				return false
			}
			continue
		}
		if state != "Z" && int(pgrp) == pgid {
			return false
		}
	}
	return true
}

func waitGroupEmpty(pgid int, bound time.Duration) bool {
	deadline := time.Now().Add(bound)
	for {
		if groupEmpty(pgid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		sleepFn(20 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// env marker scan (L4) and cwd scan (L5)

// scanEnvMarker returns the live pids whose environment carries BOTH this
// WI's PF_WI_ID marker and this daemon's PF_DAEMON_ID marker. Excludes
// self (pid 1 and below are outside procListAll). Unreadable environments
// (another uid, an exit race) are skipped: not provably ours.
func scanEnvMarker(wi, daemonID string) []int {
	self := os.Getpid()
	pids, err := procListAll()
	if err != nil {
		return nil
	}
	var out []int
	for _, pid := range pids {
		if pid == self {
			continue
		}
		state, _, _, _, ok := procStat(pid)
		if !ok || state == "Z" {
			continue
		}
		envs, err := procEnviron(pid)
		if err != nil {
			continue // unreadable (permission/exit race): not provably ours
		}
		if envHasMarkers(envs, wi, daemonID) {
			out = append(out, pid)
		}
	}
	return out
}

// cwdScan reports (never kills) live processes whose cwd sits inside the
// capsule — an operator shell must survive the cleanup.
func cwdScan(t CleanupTarget, rep *CleanupReport) {
	if t.CapsuleDir == "" {
		return
	}
	self := os.Getpid()
	pids, err := procListAll()
	if err != nil {
		return
	}
	for _, pid := range pids {
		if pid == self {
			continue
		}
		state, _, _, _, ok := procStat(pid)
		if !ok || state == "Z" {
			continue
		}
		cwd, err := procCwd(pid)
		if err != nil {
			continue
		}
		if strings.HasPrefix(cwd, t.CapsuleDir+string(filepath.Separator)) || cwd == t.CapsuleDir {
			rep.Warnings = append(rep.Warnings,
				fmt.Sprintf("layer 5: pid %d has cwd inside the capsule (%s); reported, not killed", pid, cwd))
		}
	}
}

// envHasMarkers reports whether an environment (one KEY=VALUE token per
// string) carries both PF_WI_ID=<wi> and PF_DAEMON_ID=<daemonID>.
func envHasMarkers(envs []string, wi, daemonID string) bool {
	wiMark, dMark := "PF_WI_ID="+wi, "PF_DAEMON_ID="+daemonID
	var hasWI, hasD bool
	for _, kv := range envs {
		switch kv {
		case wiMark:
			hasWI = true
		case dMark:
			hasD = true
		}
	}
	return hasWI && hasD
}
