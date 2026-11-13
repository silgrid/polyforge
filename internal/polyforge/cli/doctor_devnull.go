package cli

// doctor_devnull.go: the `null device` row — whether the null device is
// still the platform's character device (Linux 1,3; macOS 3,2).
//
// When the null device is replaced by a regular file, every `2>/dev/null`
// redirect writes into it, and any git call that reads it as a config file
// fails on the text it now holds. The row names what the path is instead and
// the atomic recovery command (create a fresh node beside it, then rename it
// over the broken one). It reads only; out of --harness; a fail moves the
// exit code.

import (
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// docChDevNull is the null device row's name.
const docChDevNull = "null device"

// nullDeviceNumbers is the platform's null device major and minor number.
func nullDeviceNumbers() (uint32, uint32) {
	if runtime.GOOS == "darwin" {
		return 3, 2
	}
	return 1, 3
}

// devNullCheck reports whether path is the platform's null device.
func devNullCheck(path string) doctorCheck {
	c := doctorCheck{Name: docChDevNull}
	wantMaj, wantMin := nullDeviceNumbers()
	fix := fmt.Sprintf(" — restore it as root: mknod -m 666 %[1]s.new c %[2]d %[3]d && mv -f %[1]s.new %[1]s"+
		" (until then every 2>%[1]s redirect writes into it and git calls that read it as config fail)",
		os.DevNull, wantMaj, wantMin)
	fi, err := os.Lstat(path)
	if err != nil {
		c.Status, c.Detail = "fail", fmt.Sprintf("%s cannot be read: %v", path, err)+fix
		return c
	}
	mode := fi.Mode()
	if mode&fs.ModeDevice == 0 || mode&fs.ModeCharDevice == 0 {
		c.Status, c.Detail = "fail", fmt.Sprintf("%s is %s, not the character device %d,%d", path, describeNonDevice(fi), wantMaj, wantMin)+fix
		return c
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		c.Status, c.Detail = "fail", fmt.Sprintf("%s is a character device whose device number cannot be read", path)+fix
		return c
	}
	rdev := uint64(st.Rdev) // int32 on darwin, uint64 on linux
	maj, mnr := unix.Major(rdev), unix.Minor(rdev)
	if maj != wantMaj || mnr != wantMin {
		c.Status, c.Detail = "fail", fmt.Sprintf("%s is the character device %d,%d, not the null device %d,%d", path, maj, mnr, wantMaj, wantMin)+fix
		return c
	}
	c.Status, c.Detail = "pass", fmt.Sprintf("%s is the character device %d,%d", path, maj, mnr)
	return c
}

// describeNonDevice names what a path that is no character device is.
func describeNonDevice(fi fs.FileInfo) string {
	switch m := fi.Mode(); {
	case m.IsRegular():
		return fmt.Sprintf("a regular file of %d bytes (mode %v)", fi.Size(), m.Perm())
	case m&fs.ModeSymlink != 0:
		return "a symlink"
	case m.IsDir():
		return "a directory"
	case m&fs.ModeDevice != 0:
		return "a block device"
	default:
		return fmt.Sprintf("a special file (mode %v)", m)
	}
}
