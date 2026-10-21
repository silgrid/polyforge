package daemon

// peercred.go: the shared half of the daemon's UDS peer-credential read
// (the per-OS getsockopt halves live in peercred_linux.go /
// peercred_darwin.go)
// (polyforge#110). The v1 daemon lost the v0 capability
// (v0/internal/daemon/api.go in git history, reference.md §6.1): with a bare net.Listen served
// by &http.Server{Handler: ...} there is no ConnContext hook and no
// credential-capturing Accept, so the POST /shutdown handler has no
// net.Conn to read the dialing process's credential from — a UDS
// request's RemoteAddr is "@" and carries nothing — making a
// route-originated graceful shutdown indistinguishable from a
// signal-originated one. credListener captures the credential at
// accept time (no I/O in flight on the conn yet) and credConnContext
// hands it to the route handlers via the request context.

import (
	"context"
	"net"
)

// peerCred is the peer credential (pid + effective uid) of the process
// on the other end of an accepted UDS conn.
type peerCred struct {
	pid int
	uid int
	ok  bool
}

// peerCredKey is the context key the request context carries the
// accepted conn's peerCred under.
type peerCredKey struct{}

// credConn wraps an accepted conn with its peer credential.
type credConn struct {
	net.Conn
	cred peerCred
}

// credListener wraps the UDS listener and reads each accepted conn's
// peer credential at accept time. The credential is read with
// SyscallConn().Control + the per-OS getsockopt (peercred_linux.go /
// peercred_darwin.go) — NEVER uc.File()/Fd(): a
// dup'd fd shares the open file description, and File/Fd deliberately
// return a BLOCKING descriptor, so that dance cleared O_NONBLOCK on
// the SHARED description — flipping the accepted conn into
// kernel-blocking mode whose server-side read then blocked inside the
// syscall, where no close could reach it: a live pooled CLI keep-alive
// conn hung a real daemon shutdown (v0 polyforge#11). Control
// runs against the real fd while no I/O is in flight (we are still at
// accept time) and changes no modes.
type credListener struct{ net.Listener }

func (l *credListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	cc := &credConn{Conn: c}
	if uc, ok := c.(*net.UnixConn); ok {
		if rc, rerr := uc.SyscallConn(); rerr == nil {
			_ = rc.Control(func(fd uintptr) {
				cc.cred = acceptPeerCred(int(fd))
			})
		}
	}
	return cc, nil
}

// credConnContext is the http.Server ConnContext hook: it puts the
// accepted conn's peer credential into the connection's context so the
// routes can attribute a route-originated shutdown to the dialing
// process (the kernel's record of the peer — never anything from the
// request body, which is caller-supplied).
func credConnContext(ctx context.Context, c net.Conn) context.Context {
	if cc, ok := c.(*credConn); ok {
		return context.WithValue(ctx, peerCredKey{}, cc.cred)
	}
	return ctx
}

// requestPeerCred returns the UDS peer credential of the connection a
// request arrived on; ok=false when the face carries none (a face
// without the credListener, a non-UDS conn, or a failed credential
// read).
func requestPeerCred(ctx context.Context) (peerCred, bool) {
	cred, ok := ctx.Value(peerCredKey{}).(peerCred)
	return cred, ok && cred.ok
}
