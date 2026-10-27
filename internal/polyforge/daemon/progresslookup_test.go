package daemon

// progresslookup_test.go: the fixed remote decision order and the
// artifact route. Local hits never depend on aihub, queued/terminal WIs
// answer only the pointer, a remote holder's snapshot carries
// holder_machine, and every typed branch surfaces instead of fabricating
// stale data.

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

type fakeFace struct {
	mu      sync.Mutex
	calls   int
	lastWI  string
	lastRel string
	ans     *api.QueryAnswerBody
	err     error
	inbox   *api.MachineInbox
	acks    []string
}

func (f *fakeFace) Inbox(faces string, wait time.Duration) (*api.MachineInbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inbox == nil {
		f.mu.Unlock()
		time.Sleep(5 * time.Millisecond) // an idle long-poll hold
		f.mu.Lock()
		return &api.MachineInbox{}, nil
	}
	out := *f.inbox
	f.inbox = nil
	return &out, nil
}
func (f *fakeFace) AnswerQuery(qid string, body api.QueryAnswerBody) error { return nil }
func (f *fakeFace) ProgressQuery(wi, artifactPath string) (*api.QueryAnswerBody, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastWI, f.lastRel = wi, artifactPath
	return f.ans, f.err
}
func (f *fakeFace) AckMessage(conversationID string, seq int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acks = append(f.acks, fmt.Sprintf("%s/%d", conversationID, seq))
	return nil
}
func (f *fakeFace) SetMachineIdentity(machineID string, key ed25519.PrivateKey) {}

func (f *fakeFace) called() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func enableFace(td *testDaemon) {
	td.mfaceMu.Lock()
	td.face.available = true
	td.face.role = api.MachineRoleInteractive
	td.face.multiMachine = true
	td.mfaceMu.Unlock()
}

func testSnapshot(wi string) *protocol.WatchSnapshot {
	return &protocol.WatchSnapshot{WorkItem: wi, Slug: "proj#1", Known: true, HubStatus: "running"}
}

func TestWatchRouteRemoteSnapshotCarriesHolder(t *testing.T) {
	face := &fakeFace{}
	td := newTestDaemon(t, func(c *Config) { c.MachineFace = face })
	enableFace(td)
	raw, _ := json.Marshal(testSnapshot("wi_remote"))
	face.ans = &api.QueryAnswerBody{HolderMachine: "m-holder", Snapshot: raw}
	td.hub.mu.Lock()
	td.hub.status["wi_remote"] = api.StatusRunning
	td.hub.mu.Unlock()

	snap, err := td.client.WatchSnapshot("wi_remote", 0, false)
	if err != nil {
		t.Fatalf("remote watch: %v", err)
	}
	if snap.HolderMachine != "m-holder" || snap.WorkItem != "wi_remote" {
		t.Fatalf("relayed snapshot: %+v", snap)
	}
}

func TestWatchRouteUnknownWIAnswers404(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.hub.mu.Lock()
	if td.hub.unknown == nil {
		td.hub.unknown = map[string]bool{}
	}
	td.hub.unknown["wi_missing"] = true
	td.hub.mu.Unlock()
	_, err := td.client.WatchSnapshot("wi_missing", 0, false)
	if err == nil {
		t.Fatal("an unknown WI must be refused, not answered with an empty snapshot")
	}
	var ae *api.Error
	if !asCode(err, &ae, api.ErrCoreNotFound) {
		t.Fatalf("unknown WI code = %v, want %s", err, api.ErrCoreNotFound)
	}
}

func asCode(err error, target **api.Error, code string) bool {
	ae, ok := err.(*api.Error)
	if !ok {
		return false
	}
	*target = ae
	return ae.Code == code
}

func TestMaybeRemoteTypedBranches(t *testing.T) {
	cases := []string{api.ErrCoreStaleHolder, api.ErrCoreGone, api.ErrCoreUnsupported, api.ErrCoreUpstream}
	for _, code := range cases {
		face := &fakeFace{err: &api.Error{Code: code, Detail: "typed"}}
		td := newTestDaemon(t, func(c *Config) { c.MachineFace = face })
		enableFace(td)
		td.hub.mu.Lock()
		td.hub.status["wi_b"] = api.StatusRunning
		td.hub.mu.Unlock()
		item, _ := td.hub.GetWorkItem("wi_b")
		local := protocol.WatchSnapshot{WorkItem: "wi_b", Events: []protocol.EventEntry{}}
		_, err := td.Daemon.maybeRemoteSnapshot(item, local)
		if err == nil {
			t.Fatalf("%s: want the typed error, got nil", code)
		}
		var ae *api.Error
		if !asCode(err, &ae, code) {
			t.Fatalf("%s: got %v", code, err)
		}
	}
}

func TestMaybeRemoteLocalAnswerKeepsLocalSnapshot(t *testing.T) {
	face := &fakeFace{ans: &api.QueryAnswerBody{Local: true, HolderMachine: "m-test"}}
	td := newTestDaemon(t, func(c *Config) { c.MachineFace = face })
	enableFace(td)
	td.hub.mu.Lock()
	td.hub.status["wi_local"] = api.StatusRunning
	td.hub.mu.Unlock()
	item, _ := td.hub.GetWorkItem("wi_local")
	local := protocol.WatchSnapshot{WorkItem: "wi_local", Events: []protocol.EventEntry{}}
	got, err := td.Daemon.maybeRemoteSnapshot(item, local)
	if err != nil {
		t.Fatalf("local answer: %v", err)
	}
	if got.WorkItem != "wi_local" || got.HolderMachine != "" || got.Known {
		t.Fatalf("the local registered-window snapshot changed: %+v", got)
	}
}

func TestMaybeRemoteQueuedTerminalNeverRelays(t *testing.T) {
	for _, st := range []string{api.StatusWrapped, api.StatusFailed, api.StatusCanceled, api.StatusQueued, api.StatusPaused} {
		face := &fakeFace{}
		td := newTestDaemon(t, func(c *Config) { c.MachineFace = face })
		enableFace(td)
		td.hub.mu.Lock()
		td.hub.status["wi_closed"] = st
		td.hub.mu.Unlock()
		item, _ := td.hub.GetWorkItem("wi_closed")
		local := protocol.WatchSnapshot{WorkItem: "wi_closed", Events: []protocol.EventEntry{}}
		got, err := td.Daemon.maybeRemoteSnapshot(item, local)
		if err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		if got.HolderMachine != "" || face.called() != 0 {
			t.Fatalf("%s must answer from hub data, not the relay: %+v calls=%d", st, got, face.called())
		}
	}
}

// --- artifact route over the real UDS face -----------------------------

func mkdirWrite(dir, name, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

func udsGet(t *testing.T, sock, path string) (int, []byte) {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}, Timeout: 10 * time.Second}
	resp, err := c.Get("http://pf" + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func TestArtifactRouteLocalHitDoesNotNeedAihub(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_localart")
	ji, _ := td.rt.Info("wi_localart")
	art := ji.CapsuleDir + "/artifacts"
	if err := mkdirWrite(art, "note.txt", "local content"); err != nil {
		t.Fatal(err)
	}
	// aihub is down: the local hit must still answer.
	td.hub.mu.Lock()
	td.hub.probeFails = 100
	td.hub.mu.Unlock()
	code, body := udsGet(t, td.sock, "/jobs/wi_localart/artifact?path="+url.QueryEscape("note.txt"))
	if code != http.StatusOK {
		t.Fatalf("local artifact read: %d %s", code, body)
	}
	var ans api.ArtifactAnswer
	if err := json.Unmarshal(body, &ans); err != nil || string(ans.Content) != "local content" {
		t.Fatalf("local artifact answer: %s %v", body, err)
	}
}

func TestArtifactRouteQueuedTerminalAnswersPointerOnly(t *testing.T) {
	face := &fakeFace{}
	td := newTestDaemon(t, func(c *Config) { c.MachineFace = face })
	enableFace(td)
	td.hub.mu.Lock()
	if td.hub.slugs == nil {
		td.hub.slugs = map[string]string{}
	}
	td.hub.slugs["wi_done"] = "proj#2"
	td.hub.status["wi_done"] = api.StatusWrapped
	td.hub.mu.Unlock()
	code, body := udsGet(t, td.sock, "/jobs/wi_done/artifact?path="+url.QueryEscape("spec.md"))
	if code != http.StatusOK {
		t.Fatalf("queued/terminal pointer: %d %s", code, body)
	}
	var ans api.ArtifactAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if !ans.Pointer || len(ans.Content) != 0 {
		t.Fatalf("queued/terminal must answer the pointer only: %+v", ans)
	}
	if face.called() != 0 {
		t.Fatalf("a queued/terminal artifact went through the relay")
	}
}

func TestArtifactRouteRemoteRelay(t *testing.T) {
	face := &fakeFace{ans: &api.QueryAnswerBody{
		HolderMachine: "m-holder",
		Artifact:      &api.ArtifactAnswer{WorkItem: "wi_remote", Path: "spec.md", Content: []byte("remote content")},
	}}
	td := newTestDaemon(t, func(c *Config) { c.MachineFace = face })
	enableFace(td)
	td.hub.mu.Lock()
	td.hub.status["wi_remote"] = api.StatusRunning
	td.hub.mu.Unlock()
	code, body := udsGet(t, td.sock, "/jobs/wi_remote/artifact?path="+url.QueryEscape("spec.md"))
	if code != http.StatusOK {
		t.Fatalf("remote artifact: %d %s", code, body)
	}
	var ans api.ArtifactAnswer
	if err := json.Unmarshal(body, &ans); err != nil || string(ans.Content) != "remote content" {
		t.Fatalf("remote artifact answer: %s %v", body, err)
	}
	if face.called() != 1 || face.lastRel != "spec.md" {
		t.Fatalf("relay call: calls=%d rel=%q", face.called(), face.lastRel)
	}
}

// TestRemoteFacesRefuseStepProcess: a step process is refused BEFORE any
// remote query leaves the daemon, on watch, artifact and analyze.
func TestRemoteFacesRefuseStepProcess(t *testing.T) {
	face := &fakeFace{ans: &api.QueryAnswerBody{HolderMachine: "m-holder", Snapshot: []byte(`{}`)}}
	td := newTestDaemon(t, func(c *Config) { c.MachineFace = face })
	enableFace(td)
	td.hub.mu.Lock()
	td.hub.status["wi_remote"] = api.StatusRunning
	td.hub.mu.Unlock()
	marks := []string{"PF_WI_ID=wi_st", "PF_DAEMON_ID=" + td.cfg.MachineID}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/jobs/wi_remote/watch"},
		{"GET", "/jobs/wi_remote/artifact?path=spec.md"},
		{"POST", "/messages/msg_1/analyze"},
	} {
		out := stepOriginRequest(t, td, tc.method, tc.path, marks...)
		if !strings.Contains(out, "PFSO-STATUS 403") || !strings.Contains(out, "DAEMON-403") {
			t.Errorf("%s %s from a step process was not refused: %s", tc.method, tc.path, out)
		}
	}
	if face.called() != 0 {
		t.Fatalf("a step process routed %d remote queries", face.called())
	}
}
