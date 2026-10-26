package runtime

// procinfo_linux.go: the Linux half of the proc seam (polyforge#218).
// cleanup.go, stepproc.go and reap.go read process state ONLY through
// these primitives; procinfo_darwin.go answers the same five from BSD
// sysctls, so the guard and the cleanup layers keep their semantics on
// macOS where no /proc exists. These are today's /proc reads, moved
// verbatim from cleanup.go/stepproc.go — behavior on Linux is unchanged.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procRoot is the procfs mount; a variable only so the fail-closed
// branches are testable.
var procRoot = "/proc"

// procStat parses one /proc/<pid>/stat read. The comm field may contain
// spaces inside parentheses, so the split happens after the LAST ')'.
func procStat(pid int) (state string, ppid, pgrp, start int64, ok bool) {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", 0, 0, 0, false
	}
	s := string(raw)
	idx := strings.LastIndex(s, ")")
	if idx < 0 || idx+2 > len(s) {
		return "", 0, 0, 0, false
	}
	fields := strings.Fields(s[idx+2:])
	if len(fields) < 20 {
		return "", 0, 0, 0, false
	}
	ppid, err1 := strconv.ParseInt(fields[1], 10, 64)
	pgrp, err2 := strconv.ParseInt(fields[2], 10, 64)
	start, err3 := strconv.ParseInt(fields[19], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return "", 0, 0, 0, false
	}
	return fields[0], ppid, pgrp, start, true
}

// readBootTime is the btime of /proc/stat, the wall-clock anchor for
// starttime comparisons (cleanup.go's bootOnce caches it).
func readBootTime() int64 {
	raw, err := os.ReadFile(filepath.Join(procRoot, "stat"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "btime ") {
			v, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "btime ")), 10, 64)
			if err == nil {
				return v
			}
			return 0
		}
	}
	return 0
}

// procListAll answers the pids (>1) of every live-listed process: the
// walker behind the L1 snapshot, the L2 group proofs and the L4/L5
// sweeps. A read error is an error (the callers fail closed).
func procListAll() ([]int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var out []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 {
			continue
		}
		out = append(out, pid)
	}
	return out, nil
}

// procEnviron reads /proc/<pid>/environ: another uid's environ is
// unreadable (EACCES) and an exit race removes the file — both answer
// an error, and the callers skip exactly as they did on the raw read.
func procEnviron(pid int) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "environ"))
	if err != nil {
		return nil, err
	}
	return strings.Split(string(raw), "\x00"), nil
}

// procCwd answers the process's working directory (the cwd readlink).
func procCwd(pid int) (string, error) {
	return os.Readlink(filepath.Join(procRoot, strconv.Itoa(pid), "cwd"))
}

// procStillListed reports whether pid still has a procfs directory —
// groupEmpty's distinction between a vanished entry (skip) and a
// still-present one with an unreadable stat (fail closed).
func procStillListed(pid int) bool {
	_, err := os.Stat(filepath.Join(procRoot, strconv.Itoa(pid)))
	return err == nil
}
