package daemon

// w359_proxy_slug_repro_test.go: repro — since v0.2.0
// every AIHub data-plane call goes through the Host's unified business
// proxy (`ANY /aihub/{path...}`), and a slug-addressed
// command loses the "#…" part of the slug:
//
//	pf file show        -> CORE-404: work item workbench
//	pf file comment ... -> HTTP-405: Method Not Allowed
//
// while the id spelling and the pre-encoded "workbench%231" spelling work
// (both stay as controls on the very same rig). Nothing was observed
// writing to a wrong work item: the failure is a refusal (404/405).
//
// The repro drives the reported path end to end — the real `cli.Run("file",
// …)` over the real Host socket (an in-process DaemonHost serving the
// production `/aihub/` route) to a REAL aihub server (internal/aihub/server
// over a real sqlite store) through the Cell's production
// `*aihubclient.Client` — and records the request lines the Hub actually
// received, logged at the end of every run.
//
// Run (targeted):
//
//	GOWORK=off go test ./internal/polyforge/daemon -run '^TestW359' -count=1 -v

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/aihub/git"
	"github.com/silgrid/polyforge/internal/aihub/server"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// w359Goal is the work item's goal: the read verb's answer must carry it,
// so a 2xx from a wrong record cannot pass the read assertion.
const w359Goal = "w359 repro: address the work item by its slug"

// w359HubSees records the request lines the real AIHub server received, so
// a failing run prints what reached the Hub (never only what the CLI sent).
type w359HubSees struct {
	next  http.Handler
	mu    sync.Mutex
	lines []string
}

func (h *w359HubSees) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.lines = append(h.lines, r.Method+" "+r.RequestURI)
	h.mu.Unlock()
	h.next.ServeHTTP(w, r)
}

func (h *w359HubSees) snapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.lines...)
}

// newW359AIHub runs a REAL aihub server on a real sqlite store with one
// admin user, one project ("workbench") and its key, and answers the
// server (behind the request recorder), the store the repro reads the
// write's effect back from, and the admin key.
func newW359AIHub(t *testing.T) (hub *httptest.Server, sees *w359HubSees, db *core.DB, key string) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := core.Open(dataDir)
	if err != nil {
		t.Fatalf("aihub: open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	admin := core.NewAdminManager(db)
	u, err := admin.CreateUser("root", "root@example.com", "admin", "human")
	if err != nil {
		t.Fatalf("aihub: admin user: %v", err)
	}
	issued, err := admin.CreateAPIKey(u.ID, "w359-repro", "")
	if err != nil {
		t.Fatalf("aihub: admin key: %v", err)
	}
	if _, err := admin.CreateProject(core.CreateProjectRequest{Name: "workbench"}); err != nil {
		t.Fatalf("aihub: project: %v", err)
	}
	mgr, err := git.NewManager(dataDir, core.NewOwnerManager(db))
	if err != nil {
		t.Fatalf("aihub: git manager: %v", err)
	}
	sees = &w359HubSees{next: server.New(db, dataDir, mgr)}
	hub = httptest.NewServer(sees)
	t.Cleanup(hub.Close)
	return hub, sees, db, issued.Plain
}

// w359Host builds and runs one in-process DaemonHost whose single default
// Cell talks to the real aihub server at hubURL with key. It is
// newTestHost with the Cell's hub set to the production `*aihubclient.Client`
// before Run (no post-start swap, so no loop can race the field), and with
// the drain/dispatch cadences parked — this repro drives the data plane only.
func w359Host(t *testing.T, hubURL, key string) *testHost {
	t.Helper()
	setGitIdentity(t)
	root := t.TempDir()
	cfgDir := filepath.Join(root, "cfg")
	data := filepath.Join(root, "data")
	ws := filepath.Join(root, "ws")
	for _, d := range []string{cfgDir, data, ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	th := &testHost{sock: filepath.Join(root, "h.sock"), done: make(chan error, 1)}
	set := &cli.HostSet{HostPath: filepath.Join(cfgDir, "cli.toml"), Dir: cfgDir, Host: cli.Config{Mapping: &mapping.Plan{}}}
	build := func(profile string, _ cli.Config) (Config, error) {
		return Config{
			MachineID:     "m-w359",
			SocketPath:    th.sock,
			Mapping:       mappingFor(ws),
			DataDir:       filepath.Join(data, "cells", profile),
			GitRemoteBase: filepath.Join(root, "hub"),
			MaxConcurrent: 4,
			Hub:           aihubclient.New(hubURL, key),
			Spawner:       &fakeSpawner{},
			AgentStarter:  fakeEngineStarter(),
			ObservePoll:   50 * time.Millisecond,
			DrainInterval: time.Hour,
			DispatchTick:  time.Hour,
			Hosted:        true,
			Server:        hubURL,
			APIKey:        key,
		}, nil
	}
	h, err := NewHost(HostConfig{SocketPath: th.sock, DataDir: data, Set: set, Build: build})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	th.Host = h
	go func() { th.done <- h.Run(context.Background()) }()
	probe := th.client("")
	probe.HTTP.Timeout = 200 * time.Millisecond
	defer probe.HTTP.CloseIdleConnections()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := probe.Status(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the Host socket never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(func() {
		_ = h.Shutdown(context.Background(), DirectShutdownCause())
		select {
		case <-th.done:
		case <-time.After(10 * time.Second):
		}
		for _, e := range h.sortedCells() {
			if e.d != nil {
				settleAfterShutdown(t, e.d)
			}
		}
	})
	return th
}

// runW359CLI runs the real `pf` entry point as a HUMAN session (the
// data-plane proxy serves a human only, so every step/task marker is
// cleared) addressed at the Host socket under test.
func runW359CLI(t *testing.T, sock string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	for _, k := range []string{
		"PF_SERVER", "PF_API_KEY", "PF_USER_ID", "PF_CHAT_ID",
		"PF_WI_ID", "PF_DAEMON_ID", "PF_TASK_ID", "PF_PROFILE",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("PF_DAEMON_SOCKET", sock)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	code = cli.Run(args, bytes.NewReader(nil), &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestW359ProxySlugKeepsTheHash is the w359 repro: a work item addressed by
// its "<project>#<seq>" slug must reach the same record its id does through
// the Host's data-plane proxy, for a read (`file show`) and a write
// (`file comment`). Observed before the fix: the slug spelling truncates at
// the "#" on the way to the Hub — CORE-404 "work item workbench" for the
// read, HTTP-405 for the write — while the id and "%23" spellings work.
func TestW359ProxySlugKeepsTheHash(t *testing.T) {
	hub, sees, db, key := newW359AIHub(t)
	// the work item the operator addresses by slug: project "workbench",
	// first ticket — slug "task#1" (the number is irrelevant to the
	// defect; it is the "#" the proxy drops).
	created, err := aihubclient.New(hub.URL, key).CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project: "workbench",
		Goal:    w359Goal,
	})
	if err != nil {
		t.Fatalf("create the repro work item: %v", err)
	}
	var wi api.WorkItem
	if err := json.Unmarshal(created, &wi); err != nil {
		t.Fatalf("create answer undecodable: %v", err)
	}
	if !strings.Contains(wi.Slug, "#") {
		t.Fatalf("the repro needs a #-carrying slug, got %q (id %s)", wi.Slug, wi.ID)
	}
	slug, id := wi.Slug, wi.ID
	pct := strings.Replace(slug, "#", "%23", 1)

	th := w359Host(t, hub.URL, key)

	// Controls: the id and the percent-encoded spellings already reach the
	// record through this exact rig — the failure below is the raw slug.
	if code, out, errOut := runW359CLI(t, th.sock, "file", "show", id); code != 0 || !strings.Contains(out, w359Goal) {
		t.Fatalf("control: `pf file show %s` exited %d (stderr %q) — the rig must serve the id spelling", id, code, strings.TrimSpace(errOut))
	}
	if code, out, errOut := runW359CLI(t, th.sock, "file", "show", pct); code != 0 || !strings.Contains(out, w359Goal) {
		t.Fatalf("control: `pf file show %s` exited %d (stderr %q) — the pre-encoded spelling must resolve to the same record", pct, code, strings.TrimSpace(errOut))
	}

	// Reported failure, verb 1 (read): the slug spelling through the proxy.
	if code, out, errOut := runW359CLI(t, th.sock, "file", "show", slug); code != 0 {
		t.Errorf("REPORTED FAILURE (w359, read): `pf file show %s` exited %d: %s — expected the same detail record the id spelling returned on this rig",
			slug, code, strings.TrimSpace(errOut))
	} else if !strings.Contains(out, w359Goal) {
		t.Errorf("REPORTED FAILURE (w359, read): `pf file show %s` exited 0 but answered no detail for %q: %s",
			slug, w359Goal, strings.TrimSpace(out))
	}

	// Reported failure, verb 2 (write): the slug spelling through the proxy.
	// On success the comment must land on THIS work item — the reported
	// failure was a refusal, but a 2xx on a wrong record would be worse.
	const body = "w359 repro: comment written through the slug"
	if code, out, errOut := runW359CLI(t, th.sock, "file", "comment", slug, "--body", body); code != 0 {
		t.Errorf("REPORTED FAILURE (w359, write): `pf file comment %s --body …` exited %d: %s — expected the append to reach the same work item the id spelling writes to",
			slug, code, strings.TrimSpace(errOut))
	} else {
		if !strings.Contains(out, body) {
			t.Errorf("REPORTED FAILURE (w359, write): `pf file comment %s` exited 0 but answered no rendered comment: %s",
				slug, strings.TrimSpace(out))
		}
		comments, err := core.NewWIManager(db).Comments(id)
		if err != nil {
			t.Fatalf("read the write's effect back: %v", err)
		}
		if len(comments) != 1 || comments[0].Body != body {
			t.Errorf("REPORTED FAILURE (w359, write): the slug-addressed comment is not on %s: %d comment(s) %+v",
				id, len(comments), comments)
		}
	}

	// The observed half of the record: exactly what the Hub received on the
	// requests above (the slug spelling's own lines are the evidence).
	t.Logf("the real AIHub server received:\n  %s", strings.Join(sees.snapshot(), "\n  "))
}
