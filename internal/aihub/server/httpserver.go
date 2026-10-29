package server

// httpserver.go: the aihub production HTTP server's pressure bounds
//. An unbounded http.Server lets an unauthenticated
// remote hold a connection open indefinitely (a slow or partial request
// header never releases it) and feed it arbitrarily large headers; the
// three constants below bound that face WITHOUT imposing a global read
// or write deadline, which would cut a large git transfer mid-body and
// the 25s inbox long-poll (machineinbox.go). They are constants: no
// flag, config key or environment variable can change them
// (cmd/aihub/main.go registers no knob for them).

import (
	"net/http"
	"time"
)

// HTTPReadHeaderTimeout bounds how long a connection may take to send
// its request headers before the server closes it (slowloris bound).
const HTTPReadHeaderTimeout = 10 * time.Second

// HTTPIdleTimeout bounds how long a keep-alive connection may stay idle
// between requests. It only governs that keep-alive idleness — never an
// in-flight handler — so the 25s inbox long-poll (inboxLongPoll, the
// same value) is untouched by it: a poll's request is being served, not
// idle, for its whole window.
const HTTPIdleTimeout = 25 * time.Second

// HTTPMaxHeaderBytes caps the request header block the server accepts;
// a bigger header is answered 4xx without reaching any handler.
const HTTPMaxHeaderBytes = 64 << 10

// NewHTTPServer answers the aihub production HTTP server for addr and
// handler with the three constant pressure bounds applied.
func NewHTTPServer(addr string, h http.Handler) *http.Server {
	return newHTTPServer(addr, h, HTTPReadHeaderTimeout, HTTPIdleTimeout, HTTPMaxHeaderBytes)
}

// newHTTPServer answers an http.Server with explicit bounds. It is the
// test seam for millisecond-scale values; the production bounds come
// from the constants above and are set by NewHTTPServer alone.
func newHTTPServer(addr string, h http.Handler, readHeader, idle time.Duration, maxHeader int) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeader,
		IdleTimeout:       idle,
		MaxHeaderBytes:    maxHeader,
		// ReadTimeout/WriteTimeout stay ZERO: a global read or write
		// deadline would cut a long git transfer mid-body and the 25s
		// inbox long-poll. The pressure bounds are the three fields
		// above only.
	}
}
