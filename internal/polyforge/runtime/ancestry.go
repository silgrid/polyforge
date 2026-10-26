package runtime

// ancestry.go: the process-ancestry walk (polyforge#218). The daemon's
// step-origin journal matching (daemon/stepmem.go) parsed
// /proc/<pid>/stat directly; the walk lives here now, built on the
// proc seam, so it answers the same ancestry without /proc on darwin.

// AncestryOf answers pid, its ancestors and their process groups — the
// set peerStepOrigin matches the spawn journal against (a journal pid
// among the caller's ancestry or in one of its groups claims it). The
// walk stops at init and on any unreadable seam answer, so an
// unverifiable part of the chain is simply not claimed.
func AncestryOf(pid int) map[int]bool {
	out := map[int]bool{}
	for i := 0; pid > 1 && i < maxAncestry; i++ {
		out[pid] = true
		_, ppid, pgrp, _, ok := procStat(pid)
		if !ok {
			break
		}
		if pgrp > 1 {
			out[int(pgrp)] = true
		}
		pid = int(ppid)
	}
	return out
}
