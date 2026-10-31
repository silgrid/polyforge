package test

// w186_waitup_readiness_test.go: harness readiness is an ANSWERED face
// request, not a bare UDS dial. The daemon binds its
// socket before startup recovery runs, so a test that returns from
// start() and then seeds a running task into the data dir can race
// Recover: the seed is rewritten interrupted and its report tail appears.
// This fake face pins waitUp's contract without a real daemon: it takes
// the dial but answers only when the test releases it.

import (
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestW186WaitUpRequiresServedFace(t *testing.T) {
	sock := filepath.Join(shortTmp(t, "w186sock"), "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	requested := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseFace := func() { releaseOnce.Do(func() { close(release) }) }
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requested <- struct{}{}:
		default:
		}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"machine_id":"m-e2e"}`))
	})}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() {
		releaseFace()
		_ = srv.Close()
		<-served
	})

	d := &v1Daemon{
		t:          t,
		sock:       sock,
		waitedExit: make(chan error, 1),
		logPath:    filepath.Join(t.TempDir(), "daemon.log"),
	}
	done := make(chan struct{})
	go func() {
		d.waitUp()
		close(done)
	}()

	select {
	case <-requested:
	case <-done:
		t.Fatal("waitUp returned on a bare UDS dial; readiness must be an answered face request")
	case <-time.After(5 * time.Second):
		t.Fatal("waitUp neither returned nor reached the fake face")
	}
	select {
	case <-done:
		t.Fatal("waitUp returned before the face answered")
	default:
	}

	releaseFace()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waitUp did not return after the face answered")
	}
	if d.client == nil {
		t.Fatal("waitUp returned without a daemon client")
	}
}
