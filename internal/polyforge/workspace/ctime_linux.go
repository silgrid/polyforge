package workspace

import (
	"io/fs"
	"syscall"
	"time"
)

// changeTime answers the inode change time (ctime) of info.
func changeTime(info fs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctim.Sec, st.Ctim.Nsec)
	}
	return time.Time{}
}
