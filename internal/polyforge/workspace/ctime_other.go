//go:build !linux

package workspace

import (
	"io/fs"
	"time"
)

// changeTime: ctime is read on linux only; elsewhere mtime alone judges.
func changeTime(fs.FileInfo) time.Time { return time.Time{} }
