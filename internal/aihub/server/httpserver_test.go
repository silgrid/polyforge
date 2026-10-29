package server

// httpserver_test.go: the production HTTP server's
// pressure bounds — the three constants with no knob, no global
// ReadTimeout/WriteTimeout, and the four behaviours over a real
// http.Server on a loopback listener (httptest's config cannot inject
// timeouts): a slow-header connection is closed without reaching a
// handler, an oversized header answers 4xx, a slow body is not cut,
// and a real inbox long-poll holds its full ?wait= window.

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// serveBounded starts the given server on a loopback listener and
// answers its base URL. The readHeader/idle/maxHeader bounds come from
// the test's constructor values.
func serveBounded(t *testing.T, srv *http.Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}

// TestNewHTTPServerBounds: the production constructor pins the three
// constant bounds and leaves the global read/write deadlines at zero
// (a global deadline would cut long git transfers and the 25s
// long-poll); the injected constructor is the test seam for the same
// fields, nothing else.
func TestNewHTTPServerBounds(t *testing.T) {
	srv := NewHTTPServer(":8420", http.NotFoundHandler())
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v, want the 10s constant", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout != 25*time.Second {
		t.Fatalf("IdleTimeout = %v, want the 25s constant", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes != 64<<10 {
		t.Fatalf("MaxHeaderBytes = %d, want the 64KiB constant", srv.MaxHeaderBytes)
	}
	if srv.ReadTimeout != 0 || srv.WriteTimeout != 0 {
		t.Fatalf("global ReadTimeout/WriteTimeout = %v/%v, want both zero", srv.ReadTimeout, srv.WriteTimeout)
	}
	inj := newHTTPServer(":0", http.NotFoundHandler(), 5*time.Millisecond, 7*time.Millisecond, 1024)
	if inj.ReadHeaderTimeout != 5*time.Millisecond || inj.IdleTimeout != 7*time.Millisecond || inj.MaxHeaderBytes != 1024 {
		t.Fatalf("injected constructor = %v/%v/%d, want the given values", inj.ReadHeaderTimeout, inj.IdleTimeout, inj.MaxHeaderBytes)
	}
}

// TestSlowHeaderConnectionClosed: a connection that never finishes its
// header block is closed by the header deadline and never reaches a
// handler. (Red on the pre- tree: without ReadHeaderTimeout
// the read blocks indefinitely and this test times out instead.)
func TestSlowHeaderConnectionClosed(t *testing.T) {
	var handlerCalls atomic.Int32
	srv := newHTTPServer(":0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}), 300*time.Millisecond, 25*time.Second, 64<<10)
	base := serveBounded(t, srv)
	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	// A partial header that is never completed.
	if _, err := io.WriteString(conn, "GET /api/v1/projects HTTP/1.1\r\nHost: 127.0.0.1\r\nX-Slow: "); err != nil {
		t.Fatalf("write partial header: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1)
	_, rerr := conn.Read(buf)
	if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
		t.Fatalf("the slow-header connection was still open after 3s (want it closed at the 300ms header bound): %v", rerr)
	}
	if rerr == nil {
		t.Fatalf("the slow-header connection was answered: a handler ran %d times, want none", handlerCalls.Load())
	}
	if !strings.Contains(rerr.Error(), "EOF") && !strings.Contains(rerr.Error(), "reset") && !strings.Contains(rerr.Error(), "closed") {
		t.Fatalf("slow-header connection ended unexpectedly: %v", rerr)
	}
	if n := handlerCalls.Load(); n != 0 {
		t.Fatalf("the slow-header request reached a handler %d times, want none", n)
	}
}

// TestOversizedHeaderRejected: a header block over the 64KiB constant is
// answered 4xx without reaching a handler. (Red on the pre-
// tree: without MaxHeaderBytes the server serves the request.)
func TestOversizedHeaderRejected(t *testing.T) {
	var handlerCalls atomic.Int32
	srv := NewHTTPServer(":0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	base := serveBounded(t, srv)
	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	req := "GET /api/v1/projects HTTP/1.1\r\nHost: 127.0.0.1\r\nAuthorization: Bearer x\r\nX-Pad: " +
		strings.Repeat("a", 70*1024) + "\r\nConnection: close\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	raw, rerr := io.ReadAll(io.LimitReader(conn, 4096))
	if rerr != nil {
		t.Fatalf("read answer: %v", rerr)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "HTTP/") {
		t.Fatalf("no HTTP answer to a 70KiB header, want 4xx: %q", text)
	}
	fields := strings.Fields(strings.SplitN(text, "\r\n", 2)[0])
	code := 0
	if len(fields) > 1 {
		_, _ = fmt.Sscanf(fields[1], "%d", &code)
	}
	if code < 400 || code > 499 {
		t.Fatalf("oversized header answered %d, want 4xx: %q", code, text)
	}
	if n := handlerCalls.Load(); n != 0 {
		t.Fatalf("the oversized-header request reached a handler %d times, want none", n)
	}
}

// TestSlowBodyNotCut: a request whose body trickles in over several
// header-deadline windows is served in full — the bounds impose no
// global read deadline on bodies (long git transfers rely on this).
func TestSlowBodyNotCut(t *testing.T) {
	const chunks = 6
	srv := newHTTPServer(":0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("handler read: %v", err)
			http.Error(w, "read failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "%d", len(data))
	}), 250*time.Millisecond, 25*time.Second, 64<<10)
	base := serveBounded(t, srv)
	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	const chunk = 1024
	total := chunks * chunk
	fmt.Fprintf(conn, "POST /api/v1/anything HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", total)
	for i := 0; i < chunks; i++ {
		if _, err := conn.Write(make([]byte, chunk)); err != nil {
			t.Fatalf("write chunk %d: %v", i, err)
		}
		time.Sleep(150 * time.Millisecond) // 6×150ms = 900ms, well past the 250ms header bound
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	raw, rerr := io.ReadAll(io.LimitReader(conn, 4096))
	if rerr != nil {
		t.Fatalf("read answer: %v", rerr)
	}
	if !strings.Contains(string(raw), fmt.Sprintf("\r\n\r\n%d", total)) {
		t.Fatalf("slow body answered %q, want the handler to see all %d bytes", raw, total)
	}
}

// TestInboxLongPollUnderBounds: a real signed inbox long-poll holds its
// full ?wait= window and answers 200 through a listener bound by the
// production constructor's header deadline — the proof that the bounds
// do not cut the long-poll leg.
func TestInboxLongPollUnderBounds(t *testing.T) {
	e := newSrvEnv(t)
	pub, priv := machineKey(t)
	e.registerSigned(t, e.key, "m-bounded", pub, priv)
	// The production constructor (10s header deadline, 25s idle bound);
	// the poll's wait window is short (400ms) so the test stays fast.
	srv := NewHTTPServer(":0", e.srv)
	base := serveBounded(t, srv)
	start := time.Now()
	code, body := e.doSignedTo(t, base, "GET", "/api/v1/machines/inbox?faces=queries&wait=400ms", e.key, "m-bounded", priv, 0, nil)
	if code != http.StatusOK {
		t.Fatalf("long-poll through the bounded listener: %d %s", code, body)
	}
	if held := time.Since(start); held < 300*time.Millisecond {
		t.Fatalf("the long-poll answered after %v, want it to hold its full 400ms window", held)
	}
}
