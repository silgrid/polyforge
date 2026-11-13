package test

// v1_inproc_harness_test.go: the in-process runtime+observe rig for
// scenarios that need an injectable observe config (the push backoff
// ladder, the long-run leak probes). It assembles the SAME production
// components — runtime.New (lifecycle + recovery engine), observe.New
// (the per-WI supervisor), runtime.DefaultSpawner (the real pi-spawner
// that derives the real fakepi process), the driver with its
// supervision link — with the observe config injected.
//
// There is no daemon in the rig, yet a workflow contract 4 step ends each
// dispatch with `pf step handoff`, and the driver reacts only to the
// accepted hand-off on the capsule's call ledger. So the rig serves the
// daemon's step-call face itself (POST /step/call on its own socket, handed
// to the step processes as PF_DAEMON_SOCKET): it checks a hand-off's shape
// and records it the way the daemon's stephandoff.go does — the call and
// result lines on runtime-state/step-calls.jsonl, the stamped section on
// artifacts/handoff.md. Only `pf step handoff` is served (a rig scenario
// needs no ask, memory or hub call); the lane checks and the owner-message
// statuses stay the daemon's.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
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

	// calls is the rig's step-call face (rigServeStepCalls); callMu
	// serializes its calls (the daemon's per-capsule step-call lock).
	calls  *http.Server
	callMu sync.Mutex
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
		// the step processes' pf reaches the rig's step-call face
		DaemonSocket: rig.serveStepCalls(t),
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
		if t.Failed() {
			t.Logf("rig events tail (%s):\n%s", rig.dataDir, eventsTail(rig.dataDir))
		}
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
		_ = rig.calls.Close()
	})
	return rig
}

// serveStepCalls starts the rig's step-call face on a socket of its own
// (a short path: the unix socket bound) and answers the path.
func (r *inprocRig) serveStepCalls(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(shortTmp(t, "pfrigsock"), "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("inproc rig: step-call socket: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /step/call", r.stepCallRoute)
	r.calls = &http.Server{Handler: mux}
	go func() { _ = r.calls.Serve(ln) }()
	return sock
}

// stepCallRoute records and serves one `pf step` call as the daemon's
// stepCallRoute does: the call line BEFORE it runs, then what it came to
// (an accepted hand-off's record, or the refusal) BEFORE the answer
// leaves.
func (r *inprocRig) stepCallRoute(w http.ResponseWriter, req *http.Request) {
	capDir, step, spawn, why := r.stepCaller()
	if why != "" {
		rigStepErr(w, api.ErrDaemonForbidden, "/step/call: "+why)
		return
	}
	var call struct {
		Verb string          `json:"verb"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.NewDecoder(req.Body).Decode(&call); err != nil {
		rigStepErr(w, api.ErrDaemonBadInput, "the call is not a valid {verb, args} JSON request: "+err.Error())
		return
	}
	r.callMu.Lock()
	defer r.callMu.Unlock()
	var ex capsule.Execution
	if raw, err := os.ReadFile(filepath.Join(capDir, capsule.ExecutionJSON)); err == nil {
		_ = json.Unmarshal(raw, &ex)
	}
	var st *capsule.Step
	if ex.Steps != nil {
		st = ex.Steps[step]
	}
	var round int64
	if st != nil {
		round = st.Round
	}
	line, err := stepcall.Append(capDir, stepcall.Line{Kind: stepcall.KindCall, Step: step, Round: round, Spawn: spawn, Verb: call.Verb, Args: call.Args})
	if err != nil {
		rigStepErr(w, api.ErrDaemonInternal, "the call could not be recorded in the call ledger: "+err.Error())
		return
	}
	rec, code, detail := rigHandoff(capDir, line, st, call.Verb, call.Args)
	res := stepcall.Line{Kind: stepcall.KindResult, Step: step, Round: round, Spawn: spawn, Verb: call.Verb, Call: line.Seq}
	switch ae := (api.Error{Code: code}); {
	case code == "":
		res.Outcome, res.Handoff = stepcall.OutcomeOK, rec
	case ae.HTTPStatus() >= 500:
		res.Outcome, res.Code, res.Error = stepcall.OutcomeFailed, code, detail
	default:
		res.Outcome, res.Code, res.Error = stepcall.OutcomeRefused, code, detail
	}
	if _, err := stepcall.Append(capDir, res); err != nil {
		rigStepErr(w, api.ErrDaemonInternal, fmt.Sprintf("call %d ran, but what it came to could not be recorded: %v", line.Seq, err))
		return
	}
	if code != "" {
		rigStepErr(w, code, detail)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"call": line.Seq, "step": step, "round": round, "spawn": spawn,
		"status": rec.Status, "to": rec.To, "path": capsule.HandoffMD,
	})
}

// stepCaller resolves the calling dispatch: the newest live step process
// of the rig's jobs, by the spawn journal (pid + start time). The daemon
// walks the UDS peer's process tree to it; the rig needs no peer
// credentials because its scenarios run one work item at a time — more
// than one job with a live step process is refused, never guessed.
func (r *inprocRig) stepCaller() (capDir, step string, spawn int64, why string) {
	n := 0
	for _, ji := range r.rt.List() {
		entries, _ := capsule.ReadSpawnJournal(ji.CapsuleDir)
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			if e.PID > 1 && runtime.JournaledProcessLive(e.PID, e.StartedAt) {
				capDir, step, spawn = ji.CapsuleDir, e.StepID, int64(e.SpawnN)
				n++
				break
			}
		}
	}
	switch {
	case n == 0:
		return "", "", 0, "serves step agents only (no job of the rig has a live step process)"
	case n > 1:
		return "", "", 0, "the in-process rig resolves a caller only while one work item has a live step process"
	}
	return capDir, step, spawn, ""
}

// rigHandoff checks and records one hand-off call of the dispatch (step
// of st, the call's spawn), answering its ledger record or the refusal's
// code and detail: the daemon's shape checks (the status, the once-only
// hand-off, the body's template sections) and its stamped section on
// artifacts/handoff.md.
func rigHandoff(capDir string, call stepcall.Line, st *capsule.Step, verb string, raw json.RawMessage) (*stepcall.Handoff, string, string) {
	if verb != stepcall.VerbHandoff {
		return nil, api.ErrDaemonBadInput, fmt.Sprintf("the in-process rig serves `pf step handoff` only (asked for %q)", verb)
	}
	var a stepcall.HandoffArgs
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, api.ErrDaemonBadInput, "the call's args: " + err.Error()
	}
	lines, err := stepcall.Read(capDir)
	if err != nil {
		return nil, api.ErrDaemonInternal, "handoff: " + err.Error()
	}
	switch {
	case stepcall.HandoffOf(lines, call.Step, call.Spawn) != nil:
		return nil, api.ErrDaemonConflict, fmt.Sprintf("handoff refused: this dispatch (step %s, spawn %d) already handed off", call.Step, call.Spawn)
	case !stepcall.IsStatus(a.Status):
		return nil, api.ErrDaemonBadInput, fmt.Sprintf("handoff refused: --status %q is none of %s", a.Status, strings.Join(stepcall.Statuses, ", "))
	case stepcall.NeedsOwner(a.Status) || a.PerOwner != "":
		return nil, api.ErrDaemonBadInput, "handoff refused: the in-process rig carries no owner messages to cite (--per-owner)"
	}
	if err := stepcall.CheckBody(a.Body, len(stepcall.StepHandoffs(lines, call.Step)) > 0, capsule.DispatchLanguage(capDir, call.Step, call.Spawn)); err != nil {
		return nil, api.ErrDaemonBadInput, "handoff refused: " + err.Error()
	}
	h := stepcall.Header{
		Call: call.Seq, Step: call.Step, Round: call.Round, Spawn: call.Spawn,
		EndedAt: time.Now().UTC().Format(time.RFC3339), Status: a.Status, To: a.To, Reason: a.Reason,
	}
	if st != nil {
		h.StartedAt, h.Model = st.StepStartedAt, st.Candidate
	}
	if err := stepcall.AppendHandoff(capDir, stepcall.RenderSection(h, a.Body)); err != nil {
		return nil, api.ErrDaemonInternal, "handoff: the section could not be appended: " + err.Error()
	}
	return &stepcall.Handoff{Status: a.Status, To: a.To, Reason: a.Reason, Candidate: h.Model, StartedAt: h.StartedAt}, "", ""
}

// rigStepErr answers one typed refusal the way the daemon's writeErr does.
func rigStepErr(w http.ResponseWriter, code, detail string) {
	ae := api.Error{Code: code, Detail: detail}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ae.HTTPStatus())
	_ = json.NewEncoder(w).Encode(ae)
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
