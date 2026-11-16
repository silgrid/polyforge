package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/task"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// w74_task_bound_test.go (polyforge#74): the task lane's concurrency bound
// is the Host's [daemon] max_concurrent_tasks — every Cell's spawns share
// it, a reload moves it live (a lowered bound refuses only new spawns) and
// `GET /status` reports the configured value.

// w74Do issues one request over the Host socket, routed to one Cell by
// the profile header ("" = the Host aggregate / the default Cell).
func w74Do(t *testing.T, sock, profile, method, path, body string) (int, string) {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}}
	req, err := http.NewRequest(method, "http://daemon"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if profile != "" {
		req.Header.Set(protocol.ProfileHeader, profile)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// w74Spawn spawns one blocked task on one Cell.
func w74Spawn(t *testing.T, sock, profile, prompt string) (int, string) {
	t.Helper()
	body := `{"project":"proj","prompt":"` + prompt + `"}`
	return w74Do(t, sock, profile, "POST", "/task/spawn", body)
}

// newW74Host assembles and runs a Host of two Cells whose task lane's
// bound is maxTasks (the Host file's [daemon] max_concurrent_tasks), every
// Cell mapping a workspace whose yaml declares the task models.
func newW74Host(t *testing.T, maxTasks int) *testHost {
	t.Helper()
	setGitIdentity(t)
	root := t.TempDir()
	cfgDir := filepath.Join(root, "cfg")
	data := filepath.Join(root, "data")
	for _, d := range []string{cfgDir, data} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	th := &testHost{sock: filepath.Join(root, "h.sock"), hubs: map[string]*fakeHub{}, done: make(chan error, 1)}
	set := &cli.HostSet{HostPath: filepath.Join(cfgDir, "cli.toml"), Dir: cfgDir,
		Host: cli.Config{Mapping: &mapping.Plan{}, Daemon: cli.DaemonSettings{MaxConcurrentTasks: maxTasks}}}
	for _, p := range []string{"default", "alpha"} {
		if p != cellcfg.DefaultProfile {
			set.Cells = append(set.Cells, cli.CellConfig{Profile: p, Path: filepath.Join(cfgDir, "cli-"+p+".toml"), Cfg: cli.Config{Mapping: &mapping.Plan{}}})
		}
	}
	build := func(profile string, _ cli.Config) (Config, error) {
		ws := filepath.Join(root, "ws-"+profile)
		if err := os.MkdirAll(ws, 0o755); err != nil {
			return Config{}, err
		}
		if err := os.WriteFile(filepath.Join(ws, workspace.FileName), []byte(taskYaml), 0o600); err != nil {
			return Config{}, err
		}
		hub := newFakeHub()
		th.hubs[profile] = hub
		return Config{
			MachineID: "m-" + profile, SocketPath: th.sock, Mapping: mappingFor(ws), DataDir: filepath.Join(data, "cells", profile),
			GitRemoteBase: filepath.Join(root, "hub"), MaxConcurrent: 4, Hub: hub, Spawner: &fakeSpawner{},
			TaskRunner: blockRunner{}, StartupFile: cli.Config{}, AgentStarter: fakeEngineStarter(),
			ObservePoll: 10 * time.Millisecond, DrainInterval: 10 * time.Millisecond, DispatchTick: 10 * time.Millisecond,
			Hosted: true, Server: "http://hub-" + profile + ":1", APIKey: "k-" + profile,
		}, nil
	}
	h, err := NewHost(HostConfig{SocketPath: th.sock, DataDir: data, Set: set, Build: build})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	th.Host = h
	go func() { th.done <- h.Run(context.Background()) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(th.sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the Host socket never appeared")
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

// TestW74HostTaskBoundFromConfigSharedByCells: the Host file's
// max_concurrent_tasks bounds every Cell's one-off tasks together — at 3
// the third spawn passes (whichever Cell), the fourth is refused 409
// naming the key, the value and `nothing is queued` — and the Host
// aggregate reports the configured value in both `tasks.limit` and
// `background_model_calls.bound`.
func TestW74HostTaskBoundFromConfigSharedByCells(t *testing.T) {
	th := newW74Host(t, 3)
	for i, p := range []string{"default", "default", "alpha"} {
		if code, body := w74Spawn(t, th.sock, p, fmt.Sprint(i)); code != 200 {
			t.Fatalf("spawn %d on %s = %d %s, want 200", i, p, code, body)
		}
	}
	code, body := w74Spawn(t, th.sock, "alpha", "4")
	if code != 409 {
		t.Fatalf("the fourth spawn at the bound of 3 = %d %s, want 409", code, body)
	}
	for _, want := range []string{"3 tasks already running", "the limit is 3", "max_concurrent_tasks", "nothing is queued", "DAEMON-409"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal must name %q: %q", want, body)
		}
	}
	raw, err := th.client("").Status()
	if err != nil {
		t.Fatal(err)
	}
	var s cli.StatusResponse
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Tasks == nil || s.Tasks.Limit != 3 || s.Tasks.Running != 3 {
		t.Fatalf("the aggregate's tasks block = %+v, want limit 3 running 3", s.Tasks)
	}
	if s.BackgroundModelCalls == nil || s.BackgroundModelCalls.Bound != 3 || s.BackgroundModelCalls.Running != 3 {
		t.Fatalf("the aggregate's background_model_calls = %+v, want bound 3 running 3", s.BackgroundModelCalls)
	}
}

// TestW74TaskBoundReloadLive (AC2's scenario): with 3 slots taken the
// bound reloads to 2 — the reload outcome is applied, the 3 running tasks
// keep running, the fourth spawn is refused naming the new value, and
// after a release to 1 running a new spawn is admitted again.
func TestW74TaskBoundReloadLive(t *testing.T) {
	slots := &task.Slots{}
	slots.SetMax(3)
	td := newTestDaemon(t, func(c *Config) {
		c.TaskRunner = blockRunner{}
		c.HostSlots = &hostSlots{max: 8, tasks: slots, cells: func() []*Daemon { return nil }}
		c.Profile = cellcfg.DefaultProfile
	})
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName), []byte(taskYaml), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cli.toml")
	daemon := func(tasks int) string {
		return "[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n\n[daemon]\nmax_concurrent_tasks = " + fmt.Sprint(tasks) + "\n"
	}
	if err := os.WriteFile(path, []byte(daemon(3)), 0o600); err != nil {
		t.Fatal(err)
	}
	td.cfg.ConfigPath = path
	td.cfg.StartupFile.Daemon.MaxConcurrentTasks = 3
	for i := 0; i < 3; i++ {
		if code, body := w74Spawn(t, td.sock, "", fmt.Sprint(i)); code != 200 {
			t.Fatalf("spawn %d = %d %s, want 200 at the bound of 3", i, code, body)
		}
	}
	if err := os.WriteFile(path, []byte(daemon(2)), 0o600); err != nil {
		t.Fatal(err)
	}
	resp, err := td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	applied := false
	for _, o := range resp.Outcomes {
		if o.Key == "max_concurrent_tasks" && o.Status == "applied" {
			applied = true
		}
	}
	if !applied {
		t.Fatalf("the reload must apply max_concurrent_tasks: %+v", resp.Outcomes)
	}
	if got := slots.Running(); got != 3 {
		t.Fatalf("a lowered bound must not stop running tasks, running %d", got)
	}
	if code, body := w74Spawn(t, td.sock, "", "4"); code != 409 || !strings.Contains(body, "the limit is 2") || !strings.Contains(body, "3 tasks already running") {
		t.Fatalf("the fourth spawn at the lowered bound = %d %q", code, body)
	}
	// release to 1 running: two cancels, then one new spawn is admitted
	for _, prompt := range []string{"0", "1"} {
		id := w74TaskID(t, td.sock, prompt)
		if code, body := w74Do(t, td.sock, "", "POST", "/task/cancel/"+id, ""); code != 200 {
			t.Fatalf("cancel %s = %d %s", id, code, body)
		}
	}
	w74WaitRunning(t, td.sock, 1)
	if code, body := w74Spawn(t, td.sock, "", "5"); code != 200 {
		t.Fatalf("a release to 1 running must admit one new spawn: %d %s", code, body)
	}
}

// TestW74UnhostedTaskBoundReloadLive: without a Host an unhosted daemon
// owns the bound itself — the file's key applies live there too.
func TestW74UnhostedTaskBoundReloadLive(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.TaskRunner = blockRunner{}
	})
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName), []byte(taskYaml), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cli.toml")
	w74WriteConfig(t, path, ws, 3)
	td.cfg.ConfigPath = path
	// the default bound holds until the reload applies the file's value
	for i := 0; i < 2; i++ {
		if code, body := w74Spawn(t, td.sock, "", fmt.Sprint(i)); code != 200 {
			t.Fatalf("spawn %d = %d %s", i, code, body)
		}
	}
	if code, body := w74Spawn(t, td.sock, "", "2"); code != 409 || !strings.Contains(body, "the limit is 2") {
		t.Fatalf("the third spawn before the reload = %d %q, want 409 at the default 2", code, body)
	}
	resp, err := td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	applied := false
	for _, o := range resp.Outcomes {
		if o.Key == "max_concurrent_tasks" && o.Status == "applied" {
			applied = true
		}
	}
	if !applied {
		t.Fatalf("the unhosted reload must apply max_concurrent_tasks: %+v", resp.Outcomes)
	}
	if code, body := w74Spawn(t, td.sock, "", "2"); code != 200 {
		t.Fatalf("the third spawn after the reload = %d %s, want 200 at the raised bound", code, body)
	}
	// a lowered bound refuses only new spawns
	w74WriteConfig(t, path, ws, 1)
	if _, err := td.reloadConfig(); err != nil {
		t.Fatal(err)
	}
	if code, body := w74Spawn(t, td.sock, "", "3"); code != 409 || !strings.Contains(body, "the limit is 1") || !strings.Contains(body, "3 tasks already running") {
		t.Fatalf("the fourth spawn at the lowered bound = %d %q", code, body)
	}
	if got := w74Running(t, td.sock); got != 3 {
		t.Fatalf("a lowered bound must not stop running tasks, running %d", got)
	}
}

// w74WriteConfig writes one reload file: the mapping plus the task bound.
func w74WriteConfig(t *testing.T, path, ws string, tasks int) {
	t.Helper()
	toml := "[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n\n[daemon]\nmax_concurrent_tasks = " + fmt.Sprint(tasks) + "\n"
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
}

// w74TaskID answers the id of the running task whose prompt is prompt.
func w74TaskID(t *testing.T, sock, prompt string) string {
	t.Helper()
	code, body := w74Do(t, sock, "", "GET", "/task/list", "")
	if code != 200 {
		t.Fatalf("task list = %d %s", code, body)
	}
	type row struct {
		ID    string `json:"id"`
		State string `json:"state"`
		Spec  struct {
			Prompt string `json:"prompt"`
		} `json:"spec"`
	}
	var rows []row
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("task list: %v", err)
	}
	for _, r := range rows {
		if r.Spec.Prompt == prompt && r.State == string(task.StateRunning) {
			return r.ID
		}
	}
	t.Fatalf("no running task with prompt %q: %s", prompt, body)
	return ""
}

// w74Running answers the running count of `GET /status`'s tasks block.
func w74Running(t *testing.T, sock string) int {
	t.Helper()
	code, body := w74Do(t, sock, "", "GET", "/status", "")
	if code != 200 {
		t.Fatalf("status = %d %s", code, body)
	}
	var s struct {
		Tasks struct {
			Running int `json:"running"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	return s.Tasks.Running
}

// w74WaitRunning polls `GET /status` until the running count settles at
// want (a terminal transition is asynchronous).
func w74WaitRunning(t *testing.T, sock string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := w74Running(t, sock); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("running tasks never settled at %d (now %d)", want, w74Running(t, sock))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
