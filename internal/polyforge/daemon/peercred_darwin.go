package daemon

// peercred_darwin.go: the darwin half of the UDS peer-credential read
// (polyforge#218; the shared model and the Control discipline live in
// peercred.go). macOS has no SO_PEERCRED: the equivalent primitives are
// getsockopt(SOL_LOCAL, LOCAL_PEERCRED), which answers a struct xucred
// carrying only the effective uid (76 bytes: version, uid, ngroups,
// groups — no pid field), and getsockopt(SOL_LOCAL, LOCAL_PEERPID),
// which answers the peer's pid. The pid is what the ancestry walk
// needs, so both options are read: ok only when BOTH succeed and the
// pid is positive. Any failure yields ok=false; the caller then fails
// closed and refuses the connection's caller (never guesses an
// identity).

import (
	"golang.org/x/sys/unix"
)

// acceptPeerCred reads the LOCAL_PEERCRED credential of the peer on fd.
func acceptPeerCred(fd int) peerCred {
	xucred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil || xucred == nil {
		return peerCred{}
	}
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil || pid <= 0 {
		return peerCred{}
	}
	return peerCred{pid: pid, uid: int(xucred.Uid), ok: true}
}
