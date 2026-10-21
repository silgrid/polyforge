package test

// v1_inproc_harness_test.go: the in-process runtime+observe rig for
// scenarios that need an injectable observe config (the push backoff
// ladder, the long-run leak probes). It assembles the SAME production
// components — runtime.New (lifecycle + recovery engine), observe.New
// (the per-WI supervisor), runtime.DefaultSpawner (the real pi-spawner
// that derives the real fakepi process), the driver with its
// supervision link — with the observe config injected.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// inprocRig is the assembled runtime+observe pair with injectable
// detection windows.
type inprocRig struct {
	t         *testing.T
	inst      *v1Install
	rt        *runtime.Runtime
	obs       *observe.Observe
	dataDir   string
	workspace string

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	sup *supervise.Registry
}

// inprocObsTune is one injected tweak of the rig's observe.Config
// (the rig's whole point: production components, injectable windows).
type inprocObsTune func(*observe.Config)

// optPushBackoff tunes the observe push retry ladder: first is the
// first retry delay (production default 10s), max the cap whose reaching
// writes push_backpressure (production default 5m).
func optPushBackoff(first, max time.Duration) inprocObsTune {
	return func(c *observe.Config) {
		c.PushRetryBackoff = first
		c.PushBackoffMax = max
	}
}

// newInprocRig assembles and runs the rig. obsTune carries observe.Config
// tweaks (e.g. the push backoff ladder), so a scenario extends the
// assembly instead of forking the helper.
func newInprocRig(t *testing.T, inst *v1Install, obsTune ...inprocObsTune) *inprocRig {
	t.Helper()
	dataDir := t.TempDir()
	workspace := t.TempDir()

	rig := &inprocRig{
		t: t, inst: inst, dataDir: dataDir, workspace: workspace,
		done: make(chan struct{}),
		sup:  supervise.NewRegistry(),
	}

	// The spawner inherits THIS process's environment: the plan dir must
	// be visible to the spawned agent (the daemon process gets it via its
	// own launch env).
	os.Setenv("FAKEPI_PLANS", inst.planDir)

	// ORPHAN PIN (pilot finding): every rig in this tree must derive the
	// FAKE harness binary, never the PiBin default ("pi" on PATH) — a
	// rig that silently dropped the field would spawn the operator's REAL
	// harness as an orphan that outlives the test binary and whose cwd
	// (the capsule temp dir) is deleted under it. The pin is structural:
	// fail the assembly, not the run.
	if inst.fakepiPath == "" {
		t.Fatalf("inproc rig: no fake harness binary built; refusing the PiBin default (a real harness would be spawned)")
	}

	plan, err := mapping.Build([]mapping.Entry{{Workspace: workspace, Projects: []string{v1Project}}})
	if err != nil {
		t.Fatal(err)
	}
	rtCfg := runtime.Config{
		MachineID:     "m-inproc",
		Mapping:       plan,
		DataDir:       dataDir,
		GitRemoteBase: inst.url + "/git",
		WorkflowsDir:  inst.workflowsPath(),
		PiBin:         inst.fakepiPath,
		ClaudeBin:     "/nonexistent/claude-test-guard", // never the host claude
	}
	sp := runtime.DefaultSpawner(&rtCfg)
	// polyforge#106: the driver IS the engine — the rig binds the real
	// driver starter (the production mapping), so the in-process
	// scenarios run the same execution engine the daemon runs.
	rtCfg.AgentStarter = func(ctx context.Context, in runtime.AgentStartInput) (runtime.AgentHandle, error) {
		h, err := driver.Start(ctx, driver.StartConfig{
			WI:         in.WI,
			CapsuleDir: in.CapsuleDir,
			BundleName: in.BundleName,
			SpawnN:     in.SpawnN,
			Spawner:    in.Spawner,
			Log:        rig.obs.Events(),
			Link:       rig.sup.Link(in.WI),
		})
		if err != nil {
			return nil, err
		}
		return h, nil
	}

	rtMsgs := supervise.NewQueue[protocol.RTMsg]()
	obMsgs := supervise.NewQueue[protocol.OBMsg]()

	rig.rt = runtime.New(rtCfg, inst.client, sp, rtMsgs, obMsgs)
	obCfg := observe.Config{
		RT: rtMsgs,
		OB: obMsgs,
		Agent: func(wi string) (observe.Handle, observe.WIInfo, bool) {
			ji, ok := rig.rt.Info(wi)
			h, live := ji.Agent.(observe.Handle)
			if !ok || !live {
				return nil, observe.WIInfo{}, false
			}
			return h, observe.WIInfo{WorkItem: ji.WorkItem, CapsuleDir: ji.CapsuleDir, Git: ji.Git}, true
		},
		DataDir:      dataDir,
		PollInterval: 50 * time.Millisecond,
		Registry:     rig.sup,
	}
	for _, tune := range obsTune {
		tune(&obCfg)
	}
	obs, err := observe.New(obCfg)
	if err != nil {
		t.Fatalf("observe.New: %v", err)
	}
	rig.obs = obs

	rig.ctx, rig.cancel = context.WithCancel(context.Background())
	go func() { _ = rig.rt.Run(rig.ctx) }()
	go func() { rig.obs.Run(rig.ctx); close(rig.done) }()

	t.Cleanup(func() {
		// Teardown order: cancel the loops, then TERMINATE every live
		// agent handle the runtime still holds — the in-process runtime
		// does not kill agents on ctx end (Run just returns; only the
		// daemon's shutdown sequence does), so without this sweep every
		// still-live fake harness child would outlive the test binary as
		// an orphan whose cwd is deleted under it (the pilot's orphan
		// finding, pinned here for the rig itself).
		rig.cancel()
		for _, ji := range rig.rt.List() {
			if ji.Agent != nil {
				_ = ji.Agent.Terminate(5 * time.Second)
			}
		}
		select {
		case <-rig.done:
		case <-time.After(5 * time.Second):
		}
	})
	return rig
}

// start claims and runs one work item through the real Runtime.
func (r *inprocRig) start(wi string) {
	r.t.Helper()
	if err := r.rt.Start(r.ctx, wi, runtime.StartOpts{}); err != nil {
		r.t.Fatalf("runtime start %s: %v", wi, err)
	}
}

// eventsOf reads the rig's durable events file for one work item.
func (r *inprocRig) eventsOf(t *testing.T, wi string) []protocol.EventEntry {
	t.Helper()
	raw := eventsRaw(t, r.dataDir)
	var out []protocol.EventEntry
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e protocol.EventEntry
		if json.Unmarshal([]byte(line), &e) == nil && e.WI == wi {
			out = append(out, e)
		}
	}
	return out
}

// waitEvent waits until one event of the given kind appears.
func (r *inprocRig) waitEvent(t *testing.T, wi, kind string, bound time.Duration) protocol.EventEntry {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		for _, e := range r.eventsOf(t, wi) {
			if e.Kind == kind {
				return e
			}
		}
		if time.Now().After(deadline) {
			var kinds []string
			for _, e := range r.eventsOf(t, wi) {
				kinds = append(kinds, e.Kind)
			}
			t.Fatalf("timeout: no %q event for %s (saw %v)", kind, wi, kinds)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// eventBody decodes one event's body.
func eventBody(t *testing.T, e protocol.EventEntry, v any) {
	t.Helper()
	if len(e.Body) == 0 {
		t.Fatalf("event %s has no body", e.Kind)
	}
	if err := json.Unmarshal(e.Body, v); err != nil {
		t.Fatalf("event %s body: %v", e.Kind, err)
	}
}
