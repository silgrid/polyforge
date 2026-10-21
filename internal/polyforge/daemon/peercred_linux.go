package daemon

// peercred_linux.go: the Linux half of the UDS peer-credential read
// (polyforge#218; the shared model and the Control discipline live in
// peercred.go). SO_PEERCRED answers a ucred with the peer's pid and
// effective uid directly.

import "golang.org/x/sys/unix"

// acceptPeerCred reads the SO_PEERCRED credential of the peer on fd.
// ok=false on a failed read — the caller then fails closed (treats the
// conn as carrying no credential).
func acceptPeerCred(fd int) peerCred {
	if ucred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
		return peerCred{pid: int(ucred.Pid), uid: int(ucred.Uid), ok: true}
	}
	return peerCred{}
}
