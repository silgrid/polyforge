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
	mu         sync.Mutex
	calls      int
	lastWI     string
	lastRel    string
	ans        *api.QueryAnswerBody
	err        error
	inbox      *api.MachineInbox
	deliveries []string
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
func (f *fakeFace) DeliverMessage(conversationID string, seq int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliveries = append(f.deliveries, fmt.Sprintf("%s/%d", conversationID, seq))
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

// TestWatchRouteRelaysOncePerCacheTTL pins A3: a resident
// watcher polls every 2 s, and a WI held elsewhere used to cost one
// signed relay per poll (≈300 per replay window per watcher). N polls
// well inside the cache TTL relay exactly once, and every one of them
// answers the holder's snapshot.
func TestWatchRouteRelaysOncePerCacheTTL(t *testing.T) {
	face := &fakeFace{}
	td := newTestDaemon(t, func(c *Config) { c.MachineFace = face })
	enableFace(td)
	raw, _ := json.Marshal(testSnapshot("wi_cached"))
	face.ans = &api.QueryAnswerBody{HolderMachine: "m-holder", Snapshot: raw}
	td.hub.mu.Lock()
	td.hub.status["wi_cached"] = api.StatusRunning
	td.hub.mu.Unlock()
	for i := 0; i < 5; i++ {
		snap, err := td.client.WatchSnapshot("wi_cached", int64(i), false)
		if err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
		if snap.HolderMachine != "m-holder" || snap.WorkItem != "wi_cached" {
			t.Fatalf("poll %d answered %+v, want the holder's snapshot", i+1, snap)
		}
	}
	if got := face.called(); got != 1 {
		t.Fatalf("5 polls inside the cache TTL relayed %d times, want 1", got)
	}
}

// remoteCacheDaemon is a test daemon whose face answers wi's holder
// snapshot, whose cache runs on the returned clock, and whose hub says
// wi is running.
func remoteCacheDaemon(t *testing.T, wi string) (*testDaemon, *fakeFace, func(time.Duration)) {
	t.Helper()
	face := &fakeFace{}
	td := newTestDaemon(t, func(c *Config) { c.MachineFace = face })
	enableFace(td)
	raw, _ := json.Marshal(testSnapshot(wi))
	face.ans = &api.QueryAnswerBody{HolderMachine: "m-holder", Snapshot: raw}
	td.hub.mu.Lock()
	td.hub.status[wi] = api.StatusRunning
	td.hub.mu.Unlock()
	var mu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	td.Daemon.remoteSnaps.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	return td, face, advance
}

func setHub(td *testDaemon, wi, status, updatedAt string) {
	td.hub.mu.Lock()
	defer td.hub.mu.Unlock()
	td.hub.status[wi] = status
	if td.hub.updatedAt == nil {
		td.hub.updatedAt = map[string]string{}
	}
	td.hub.updatedAt[wi] = updatedAt
}

// pollRemote is one watcher poll of wi; it answers the snapshot.
func pollRemote(t *testing.T, td *testDaemon, wi string) *protocol.WatchSnapshot {
	t.Helper()
	snap, err := td.client.WatchSnapshot(wi, 0, false)
	if err != nil {
		t.Fatalf("poll %s: %v", wi, err)
	}
	return snap
}

// TestRemoteSnapshotCacheTTLAndKey ( A3): a poll after the
// TTL relays again, and inside the TTL any aihub-visible change of the
// WI — a status change (running -> blocked, still remote) or a new
// updated_at (a claim, a session end, a patch) — relays instead of
// serving the entry.
func TestRemoteSnapshotCacheTTLAndKey(t *testing.T) {
	td, face, advance := remoteCacheDaemon(t, "wi_ttl")
	setHub(td, "wi_ttl", api.StatusRunning, "t1")
	pollRemote(t, td, "wi_ttl")
	advance(remoteSnapTTL - time.Millisecond)
	if snap := pollRemote(t, td, "wi_ttl"); snap.HolderMachine != "m-holder" || face.called() != 1 {
		t.Fatalf("a poll inside the TTL relayed (calls=%d) or lost the holder snapshot: %+v", face.called(), snap)
	}
	advance(time.Millisecond)
	if pollRemote(t, td, "wi_ttl"); face.called() != 2 {
		t.Fatalf("a poll at the TTL served the cache: calls=%d, want 2", face.called())
	}
	setHub(td, "wi_ttl", api.StatusBlocked, "t1")
	if pollRemote(t, td, "wi_ttl"); face.called() != 3 {
		t.Fatalf("a status change inside the TTL served the cache: calls=%d, want 3", face.called())
	}
	setHub(td, "wi_ttl", api.StatusBlocked, "t2")
	if pollRemote(t, td, "wi_ttl"); face.called() != 4 {
		t.Fatalf("a new updated_at inside the TTL served the cache: calls=%d, want 4", face.called())
	}
	if pollRemote(t, td, "wi_ttl"); face.called() != 4 {
		t.Fatalf("an unchanged WI inside the TTL relayed: calls=%d, want 4", face.called())
	}
}

// TestRemoteSnapshotCacheBypassedForLocalOrClosedWI ( A3): a
// WI the local side knows is not remote — paused, queued or terminal at
// aihub, or now a local job — never reads the cache: it answers the
// local snapshot without a relay, and its entry is gone, so a later
// remote poll relays afresh.
func TestRemoteSnapshotCacheBypassedForLocalOrClosedWI(t *testing.T) {
	for _, st := range []string{api.StatusPaused, api.StatusQueued, api.StatusWrapped, api.StatusFailed, api.StatusCanceled} {
		td, face, _ := remoteCacheDaemon(t, "wi_closed")
		setHub(td, "wi_closed", api.StatusRunning, "t1")
		pollRemote(t, td, "wi_closed") // primes the cache
		setHub(td, "wi_closed", st, "t1")
		snap := pollRemote(t, td, "wi_closed")
		if snap.HolderMachine != "" || face.called() != 1 {
			t.Fatalf("%s: answered %+v with %d relays, want the local snapshot and no new relay", st, snap, face.called())
		}
		setHub(td, "wi_closed", api.StatusRunning, "t1")
		if pollRemote(t, td, "wi_closed"); face.called() != 2 {
			t.Fatalf("%s: the entry survived the bypass (calls=%d, want a fresh relay)", st, face.called())
		}
	}

	td, face, _ := remoteCacheDaemon(t, "wi_becomes_local")
	pollRemote(t, td, "wi_becomes_local") // primes the cache
	td.submitClaim(t, "wi_becomes_local")
	snap := pollRemote(t, td, "wi_becomes_local")
	if !snap.Known || snap.HolderMachine != "" || face.called() != 1 {
		t.Fatalf("a WI now held locally answered %+v with %d relays, want the local job's snapshot", snap, face.called())
	}
}

// TestRemoteSnapshotErrorsAreNotCached ( A3): a failed relay
// is never stored — the next poll asks the holder again, as before — and
// the first success after it is cached.
func TestRemoteSnapshotErrorsAreNotCached(t *testing.T) {
	td, face, _ := remoteCacheDaemon(t, "wi_err")
	face.mu.Lock()
	ans := face.ans
	face.ans, face.err = nil, &api.Error{Code: api.ErrCoreUpstream, Detail: "holder did not answer"}
	face.mu.Unlock()
	for i := 1; i <= 2; i++ {
		if _, err := td.client.WatchSnapshot("wi_err", 0, false); err == nil {
			t.Fatalf("failed relay %d answered no error", i)
		}
		if face.called() != i {
			t.Fatalf("failed relay %d: calls=%d, want %d (an error was cached)", i, face.called(), i)
		}
	}
	face.mu.Lock()
	face.ans, face.err = ans, nil
	face.mu.Unlock()
	pollRemote(t, td, "wi_err")
	pollRemote(t, td, "wi_err")
	if face.called() != 3 {
		t.Fatalf("after recovery: calls=%d, want 3 (one relay, then the cache)", face.called())
	}
}

// TestArtifactRouteIsNotCached ( A3): artifacts are fetched
// on demand, so every remote artifact read relays — the progress cache
// (primed here) never answers one.
func TestArtifactRouteIsNotCached(t *testing.T) {
	td, face, _ := remoteCacheDaemon(t, "wi_art")
	pollRemote(t, td, "wi_art")
	face.mu.Lock()
	face.ans = &api.QueryAnswerBody{HolderMachine: "m-holder",
		Artifact: &api.ArtifactAnswer{WorkItem: "wi_art", Path: "spec.md", Content: []byte("remote content")}}
	face.mu.Unlock()
	for i := 1; i <= 2; i++ {
		code, body := udsGet(t, td.sock, "/jobs/wi_art/artifact?path="+url.QueryEscape("spec.md"))
		if code != http.StatusOK {
			t.Fatalf("artifact read %d: %d %s", i, code, body)
		}
	}
	if face.called() != 3 || face.lastRel != "spec.md" {
		t.Fatalf("artifact reads: calls=%d rel=%q, want 1 progress + 2 artifact relays", face.called(), face.lastRel)
	}
}

// TestRemoteSnapCacheBoundedMemory ( A3): the cache never
// holds more than its cap — the oldest entry goes — and a store sweeps
// every expired entry.
func TestRemoteSnapCacheBoundedMemory(t *testing.T) {
	c := newRemoteSnapCache(10*time.Second, 2)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }
	item := func(id string) *api.WorkItem { return &api.WorkItem{ID: id, Status: api.StatusRunning} }
	for _, id := range []string{"a", "b", "c"} {
		c.put(item(id), protocol.WatchSnapshot{WorkItem: id})
		now = now.Add(time.Second)
	}
	if _, ok := c.get(item("a")); ok || len(c.entries) != 2 {
		t.Fatalf("the cap did not evict the oldest entry: %d entries, a cached=%v", len(c.entries), ok)
	}
	if _, ok := c.get(item("c")); !ok {
		t.Fatal("the newest entry was evicted")
	}
	now = now.Add(time.Minute)
	c.put(item("d"), protocol.WatchSnapshot{WorkItem: "d"})
	if len(c.entries) != 1 {
		t.Fatalf("a store left %d entries, want only the fresh one (expired entries swept)", len(c.entries))
	}
}
