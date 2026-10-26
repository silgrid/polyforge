package runtime

// procinfo_darwin.go: the darwin half of the proc seam (polyforge#218)
// — the same primitives procinfo_linux.go answers from procfs, read
// from BSD sysctls instead (no /proc exists on macOS). The semantics
// every caller relies on are preserved exactly:
//
//   - state "Z" is P_stat == SZOMB, so the zombie filtering keeps
//     working; callers distinguish only "Z" vs everything else;
//   - start is ticks-since-boot on the Linux 100-ticks-per-second
//     clock, derived from P_starttime minus kern.boottime, so the
//     wall-clock arithmetic in rootIdentityHolds/groupProvableOurs
//     (bt + start/100) runs unchanged;
//   - environment markers come from kern.procargs2: an error for
//     another uid or an exit race (the callers skip it as they skipped
//     /proc's EACCES/ENOENT), and — unlike /proc — the environment is
//     omitted (the blob ends after argv) for a cross-process read of a
//     CS_RESTRICT target, i.e. a binary signed with entitlements such
//     as the official node and claude builds (XNU sysctl_procargsx,
//     production kernel with SIP on). A step agent's own markers are
//     therefore unreadable on darwin; the daemon identifies its spawn
//     by the journaled pid + start time instead (polyforge#234);
//   - another process's cwd is read with the proc_info syscall
//     (SYS_proc_info = 336, XNU bsd/kern/syscalls.master) using the
//     PROC_PIDVNODEPATHINFO flavor — the same source lsof and psutil
//     use. On failure procCwd answers an error, which the callers
//     skip exactly as they skipped /proc's EACCES/ENOENT.

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// darwinSZOMB is the bsd/sys/proc.h P_stat value of a zombie; x/sys
// carries no darwin constant for it.
const darwinSZOMB = 5

// linuxClockTicks is the Linux _SC_CLK_TCK the starttime comparison in
// cleanup.go assumes (ticks = 1/100 s).
const linuxClockTicks = 100

// procStat reads one process's state, parent, process group and start
// time from kern.proc.pid.
func procStat(pid int) (state string, ppid, pgrp, start int64, ok bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", 0, 0, 0, false
	}
	bt := bootOnce()
	if bt == 0 {
		return "", 0, 0, 0, false
	}
	// callers distinguish only zombies from everything live
	if kp.Proc.P_stat == darwinSZOMB {
		state = "Z"
	} else {
		state = "R"
	}
	return state, int64(kp.Eproc.Ppid), int64(kp.Eproc.Pgid), startTicks(kp.Proc.P_starttime, bt), true
}

// startTicks converts the wall-clock P_starttime (an absolute Timeval,
// not a time-since-boot) into ticks since boot on the
// Linux clock (bt + start/100 must equal the wall-clock start time,
// with sub-tick truncation; the comparisons allow ±2 s).
func startTicks(tv unix.Timeval, bt int64) int64 {
	return (int64(tv.Sec) - bt) * linuxClockTicks
}

// readBootTime is kern.boottime, the wall-clock anchor for starttime
// comparisons (cleanup.go's bootOnce caches it).
func readBootTime() int64 {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return 0
	}
	return tv.Sec
}

// procListAll answers every process's pid (>1) from kern.proc.all.
// Fails closed when kern.boottime is unreadable: process listing
// depends on a valid boot time for the starttime comparisons in
// cleanup.go (bt + start/100); without it the callers cannot
// distinguish a live process from a reused pid, so an empty answer
// would let cleanup proceed on unverified state (fail open).
func procListAll() ([]int, error) {
	if bootOnce() == 0 {
		return nil, errors.New("kern.boottime is unreadable; process listing disabled (fail closed)")
	}
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(kps))
	for _, kp := range kps {
		if pid := int(kp.Proc.P_pid); pid > 1 {
			out = append(out, pid)
		}
	}
	return out, nil
}

// procEnviron answers the process's environment from kern.procargs2
// (another uid or an exit race is an error — the sysctl answers
// "invalid argument" for both, which the callers skip; a CS_RESTRICT
// target answers no environment at all, see the file comment).
func procEnviron(pid int) ([]string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, err
	}
	return procargsEnviron(raw), nil
}

// darwinProcInfoCallPIDInfo is PROC_INFO_CALL_PIDINFO
// (libproc_internal.h); the syscall number comes from x/sys as
// unix.SYS_PROC_INFO (SYS_proc_info = 336 on both darwin arches).
const (
	darwinProcInfoCallPIDInfo  = 2
	darwinProcPidVNodePathInfo = 9 // PROC_PIDVNODEPATHINFO (bsd/sys/proc_info.h)
)

// darwinVnodePathCwdOffset is offsetof(struct vnode_info_path,
// vip_path): vinfo_stat (136) + vi_type (4) + vi_pad (4) + fsid (8).
const darwinVnodePathCwdOffset = 152

// darwinVnodePathInfoSize is sizeof(struct proc_vnodepathinfo):
// 2 * (sizeof(vnode_info) 152 + MAXPATHLEN 1024).
const darwinVnodePathInfoSize = 2352

// procCwd answers the process's working directory via the proc_info
// syscall (SYS_proc_info = 336, XNU bsd/kern/syscalls.master) with the
// PROC_PIDVNODEPATHINFO flavor — the equivalent of the
// proc_pidinfo(PROC_PIDVNODEPATHINFO) call lsof and psutil use; there
// is no sysctl for another process's cwd. The buffer is parsed at the
// struct-derived offset and validated (plausible absolute path) so a
// layout mismatch fails closed instead of yielding a wrong path.
// Same-uid reads succeed; another uid or an exit race is an error,
// which the callers skip as they skipped /proc's EACCES/ENOENT.
func procCwd(pid int) (string, error) {
	buf := make([]byte, darwinVnodePathInfoSize)
	r1, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		darwinProcInfoCallPIDInfo,
		uintptr(pid),
		darwinProcPidVNodePathInfo,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if errno != 0 {
		return "", fmt.Errorf("proc_info(PROC_PIDVNODEPATHINFO) for pid %d failed: %v", pid, errno)
	}
	if int(r1) <= darwinVnodePathCwdOffset {
		return "", fmt.Errorf("proc_info(PROC_PIDVNODEPATHINFO) for pid %d was truncated (%d bytes)", pid, r1)
	}
	path := string(buf[darwinVnodePathCwdOffset:])
	if idx := strings.IndexByte(path, 0); idx >= 0 {
		path = path[:idx]
	}
	if path == "" || path[0] != '/' {
		return "", fmt.Errorf("proc_info(PROC_PIDVNODEPATHINFO) for pid %d returned no usable cwd", pid)
	}
	return path, nil
}

// procStillListed reports whether pid is still answerable — on darwin
// the kinfo read is the only test, and a failing one means the process
// is gone (or unseen), which groupEmpty may then skip (the kernel holds
// no partially readable per-pid record).
func procStillListed(pid int) bool {
	_, _, _, _, ok := procStat(pid)
	return ok
}
