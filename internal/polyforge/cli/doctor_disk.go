package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// doctor_disk.go: the "disk watermark" row of `pf doctor`.
// Capsules only grow until a retention prune slims them, and a full disk
// shows up as unrelated red steps (ENOSPC). For every mapped workspace
// that exists this row reports the filesystem holding it — used, total
// and the used percentage, `df`'s formula — and the bytes under its
// capsules/ (one walk that follows no symlink). A filesystem shared by
// several workspaces is reported once. Used above diskWatermarkPct warns;
// the row never fails and never moves the exit code. A workspace whose
// filesystem cannot be read, or whose capsules/ walk meets an unreadable
// entry, is named and warns: never a faked green. Read-only.

const docChDisk = "disk watermark"

// diskWatermarkPct is the used share of a filesystem above which the
// row warns.
const diskWatermarkPct = 70

// diskFS is one filesystem's usage in bytes; Dev identifies it.
type diskFS struct {
	Dev                uint64
	Total, Used, Avail uint64
}

// diskStat answers the filesystem holding path (a test seam: the tests
// put a filesystem on either side of the watermark).
var diskStat = statfsDisk

func statfsDisk(path string) (diskFS, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return diskFS{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return diskFS{}, err
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return diskFS{}, fmt.Errorf("%s: no device id", path)
	}
	bsize := uint64(st.Bsize)
	total, free := uint64(st.Blocks)*bsize, uint64(st.Bfree)*bsize
	return diskFS{Dev: uint64(sys.Dev), Total: total, Used: total - free, Avail: uint64(st.Bavail) * bsize}, nil
}

// usedPct is df's used percentage: used over what a user can fill
// (used + available), rounded up.
func (d diskFS) usedPct() int {
	room := d.Used + d.Avail
	if room == 0 {
		return 0
	}
	return int((d.Used*100 + room - 1) / room)
}

// capsuleBytes sums the regular files under <ws>/capsules (no symlink
// followed); unread counts the entries the walk could not read.
func capsuleBytes(ws string) (size int64, unread int) {
	root := filepath.Join(ws, "capsules")
	filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if !(p == root && os.IsNotExist(err)) {
				unread++
			}
			if e != nil && e.IsDir() && p != root {
				return fs.SkipDir
			}
			return nil
		}
		if e.Type().IsRegular() {
			info, err := e.Info()
			if err != nil {
				unread++
				return nil
			}
			size += info.Size()
		}
		return nil
	})
	return size, unread
}

// diskCheck is the row over the existing mapped workspaces.
func diskCheck(wss []string) doctorCheck {
	c := doctorCheck{Name: docChDisk, Status: "pass"}
	if len(wss) == 0 {
		c.Status, c.Detail = "skip", "no mapped workspace directory to measure"
		return c
	}
	type group struct {
		fs   diskFS
		rows []string
	}
	var order []uint64
	groups := map[uint64]*group{}
	var unreadable []string
	above := false
	for _, ws := range wss {
		d, err := diskStat(ws)
		if err != nil {
			unreadable = append(unreadable, ws+": "+err.Error())
			continue
		}
		size, unread := capsuleBytes(ws)
		row := fmt.Sprintf("%s capsules/ %s", ws, fmtSize(size))
		if unread > 0 {
			row += fmt.Sprintf(" (%d entries unreadable: a floor)", unread)
			unreadable = append(unreadable, fmt.Sprintf("%s capsules/: %d entries unreadable", ws, unread))
		}
		g := groups[d.Dev]
		if g == nil {
			g = &group{fs: d}
			groups[d.Dev] = g
			order = append(order, d.Dev)
		}
		g.rows = append(g.rows, row)
	}
	var parts []string
	for _, dev := range order {
		g := groups[dev]
		pct := g.fs.usedPct()
		parts = append(parts, fmt.Sprintf("%d%% used (%s of %s) — %s", pct, fmtSize(int64(g.fs.Used)), fmtSize(int64(g.fs.Total)), strings.Join(g.rows, ", ")))
		if pct > diskWatermarkPct {
			above = true
		}
	}
	c.Detail = fmt.Sprintf("filesystems holding the mapped workspaces (warn above %d%% used): %s", diskWatermarkPct, strings.Join(parts, "; "))
	if len(parts) == 0 {
		c.Detail = "no mapped workspace's filesystem could be read"
	}
	if above {
		c.Status = "warn"
		c.Detail += fmt.Sprintf("; above the %d%% watermark — free space before steps fail on a full disk (`pf doctor` names what `--prune-capsules` and `--prune-scratch` reclaim)", diskWatermarkPct)
	}
	if len(unreadable) > 0 {
		c.Status = "warn"
		c.Detail += "; not measured: " + strings.Join(capRows(unreadable), "; ")
	}
	return c
}
