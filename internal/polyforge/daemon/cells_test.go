package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// testHost is one in-process DaemonHost over fake hubs.
type testHost struct {
	*Host
	sock string
	hubs map[string]*fakeHub
	done chan error
}

// hostCell is one Cell of a test Host: its profile, an invalid reason ("" =
// valid) and its machine id.
type hostCell struct {
	profile, invalid, machineID, agentLanguage string
}

// newTestHost assembles and runs a Host over the given Cells (the first is
// default) with fake hubs and engines.
func newTestHost(t *testing.T, cells ...hostCell) *testHost {
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
	set := &cli.HostSet{HostPath: filepath.Join(cfgDir, "cli.toml"), Dir: cfgDir, Host: cli.Config{Mapping: &mapping.Plan{}}}
	ids := map[string]string{}
	for i, c := range cells {
		ids[c.profile] = c.machineID
		if i == 0 {
			continue
		}
		set.Cells = append(set.Cells, cli.CellConfig{Profile: c.profile, Path: filepath.Join(cfgDir, "cli-"+c.profile+".toml"), Invalid: c.invalid, Cfg: cli.Config{Mapping: &mapping.Plan{}}})
	}
	build := func(profile string, _ cli.Config) (Config, error) {
		ws := filepath.Join(root, "ws-"+profile)
		if err := os.MkdirAll(ws, 0o755); err != nil {
			return Config{}, err
		}
		hub := newFakeHub()
		th.hubs[profile] = hub
		dataDir := filepath.Join(data, "cells", profile) // every Cell, default included
		return Config{
			MachineID: ids[profile], SocketPath: th.sock, Mapping: mappingFor(ws), DataDir: dataDir,
			GitRemoteBase: filepath.Join(root, "hub"), MaxConcurrent: 4, Hub: hub, Spawner: &fakeSpawner{},
			StartupFile: cli.Config{Daemon: cli.DaemonSettings{AgentLanguage: func() string {
				for _, c := range cells {
					if c.profile == profile {
						return c.agentLanguage
					}
				}
				return ""
			}()}},
			AgentStarter: fakeEngineStarter(), ObservePoll: 10 * time.Millisecond, DrainInterval: 10 * time.Millisecond,
			DispatchTick: 10 * time.Millisecond, Hosted: true, Server: "http://hub-" + profile + ":1", APIKey: "k-" + profile,
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

func (th *testHost) client(profile string) *cli.DaemonClient {
	c := cli.NewDaemonClient(th.sock)
	c.Profile = profile
	return c
}

// TestHostServesTwoCellsBehindOneSocket: one socket, two Cells, each
// request answered by exactly the Cell its header names; headerless is the
// default Cell; an unknown profile is DAEMON-404 naming its expected file.
func TestHostServesTwoCellsBehindOneSocket(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	statusOf := func(profile string) cli.StatusResponse {
		t.Helper()
		raw, err := th.client(profile).Status()
		if err != nil {
			t.Fatalf("status %q: %v", profile, err)
		}
		var s cli.StatusResponse
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := statusOf("alpha"); s.MachineID != "m-alpha" || len(s.Cells) != 0 {
		t.Fatalf("alpha's own status: machine %q cells %d", s.MachineID, len(s.Cells))
	}
	agg := statusOf("")
	if agg.MachineID != "m-def" || len(agg.Cells) != 2 || agg.Cells[0].Profile != "default" || agg.Cells[1].Profile != "alpha" {
		t.Fatalf("the Host aggregate: machine %q cells %+v", agg.MachineID, agg.Cells)
	}
	// a Cell-scoped route without a header lands on default
	if _, err := th.client("").WatchSnapshot("wi_none", 0, false); err == nil {
		t.Log("headerless watch answered (default Cell)")
	}
	_, err := th.client("beta").WatchSnapshot("wi_none", 0, false)
	if err == nil || !strings.Contains(err.Error(), "DAEMON-404") || !strings.Contains(err.Error(), "cli-beta.toml") {
		t.Fatalf("an unknown profile must be DAEMON-404 naming cli-beta.toml: %v", err)
	}
	resp, err := th.client("").Profiles()
	if err != nil || len(resp.Profiles) != 2 || len(resp.Index) != 2 {
		t.Fatalf("GET /profiles: %+v %v", resp, err)
	}
}

// TestHostIsolatesAnInvalidNamedCell: an invalid named Cell is visible and
// refuses its requests explicitly; the default Cell serves.
func TestHostIsolatesAnInvalidNamedCell(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", invalid: "config cli-alpha.toml: line 1: value must be a quoted string"})
	resp, err := th.client("").Profiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Profiles) != 2 || resp.Profiles[1].Valid || resp.Profiles[1].State != "invalid" || !strings.Contains(resp.Profiles[1].Reason, "quoted") {
		t.Fatalf("the invalid Cell must be visible: %+v", resp.Profiles)
	}
	if len(resp.Index) != 1 || resp.Index[0].Profile != "default" {
		t.Fatalf("an invalid Cell must not be in the effective index: %+v", resp.Index)
	}
	if _, err := th.client("alpha").Status(); err == nil || !strings.Contains(err.Error(), "no live runtime") {
		t.Fatalf("a request to the invalid Cell must fail explicitly: %v", err)
	}
	if _, err := th.client("").Status(); err != nil {
		t.Fatalf("the default Cell must serve: %v", err)
	}
}

// TestHostShutdownIsHostLevel: a shutdown naming a Cell is refused and
// stops nothing; a plain one stops the whole Host.
func TestHostShutdownIsHostLevel(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	if _, err := th.client("alpha").Shutdown(); err == nil || !strings.Contains(err.Error(), "Host-level") {
		t.Fatalf("a Cell-scoped shutdown must be refused: %v", err)
	}
	if _, err := th.client("").Status(); err != nil {
		t.Fatalf("the refused shutdown stopped the Host: %v", err)
	}
	req, _ := http.NewRequest("POST", "http://daemon/shutdown", strings.NewReader("{}"))
	if resp, err := th.client("").HTTP.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("plain shutdown: %v %v", resp, err)
	}
	select {
	case <-th.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the Host did not stop")
	}
	var _ = protocol.ProfileHeader
}

// TestHostInjectsEveryCellsTargetFacts (polyforge#76): every serving
// Cell's daemon injects the target facts of ALL this Host's serving Cells
// - profile, server as configured and the deploy configuration file that
// Cell's deploys read (beside its Cell file, the Cell file naming) - so a
// step's deploy procedure can derive the machine's deploy targets from
// one list. A Cell that cannot load is no target: it serves no work
// items and its configuration is not trusted.
func TestHostInjectsEveryCellsTargetFacts(t *testing.T) {
	th := newTestHost(t,
		hostCell{profile: "default", machineID: "m-def"},
		hostCell{profile: "public", machineID: "m-pub"},
		hostCell{profile: "broken", invalid: "load failure (test)", machineID: "m-bad"},
	)
	dir := th.Host.cfg.Set.Dir
	want := []runtime.CellTarget{
		{Profile: "default", Server: "http://hub-default:1", DeployEnv: filepath.Join(dir, cellcfg.DeployEnvFileName("default"))},
		{Profile: "public", Server: "http://hub-public:1", DeployEnv: filepath.Join(dir, cellcfg.DeployEnvFileName("public"))},
	}
	for _, p := range []string{"default", "public"} {
		e := th.Host.cell(p).d.stepEnv()
		if !slices.Equal(e.CellTargets, want) {
			t.Fatalf("%s: the step env carries every serving Cell's target facts %+v, want %+v", p, e.CellTargets, want)
		}
		for _, row := range e.CellTargets {
			if row.Profile == "broken" {
				t.Fatalf("a Cell that cannot load is no deploy target: %+v", e.CellTargets)
			}
		}
	}
}
