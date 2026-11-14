package daemon

// cells.go: the DaemonHost — one `pf daemon` process, one Unix socket, one
// registry of ProfileCells. Each Cell is one AIHub identity domain: its own
// hub client and credential, machine identity and heartbeat, workspaces,
// queue, runtime, observe lane and tasks, all assembled as one *Daemon that
// runs Hosted (no face of its own). The Host owns the listener, routes every
// request to exactly one Cell (protocol.ProfileHeader; a request without it
// is served by the default Cell — a permanent protocol default), answers the
// Host-level routes itself (GET /profiles, the GET /status aggregate,
// POST /ops/config without a profile, POST /shutdown) and shuts every Cell
// down with itself.
//
// A Cell is a unit of state isolation, never a sandbox: every Cell lives in
// this one process and a process failure is common to all of them. The
// default Cell is the baseline: if it cannot load the Host refuses to start;
// a named Cell that cannot load (a bad file, a conflict, a start failure) is
// isolated, visible on GET /profiles, and every other Cell serves on.
//
// Locking: Host.mu guards only the registry map and the per-Cell bookkeeping
// fields; it is never held across I/O, a Cell call or an HTTP write.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// The execution-disabled reasons a Cell reports.
const (
	execDisabledNoWorkspace   = "no workspace configured — add a [workspace.\"<path>\"] mapping to this Cell's file and reload"
	execDisabledNoModelSource = "no model-credential source declared — set [model] source (\"shared-default\" or an absolute directory) in this Cell's file and restart the Host"
)

// cellStartBound bounds how long the Host waits for one Cell to reach its
// serving point (data-dir lock, machine key, restart reconciliation — all
// local: none of it asks aihub anything).
const cellStartBound = 60 * time.Second

// HostConfig wires the Host.
type HostConfig struct {
	// SocketPath is the one Unix socket every Cell is served behind.
	SocketPath string
	// DataDir is the Host root (cells live under cells/<profile>).
	DataDir string
	// Set is the loaded configuration: the Host file and the named Cell files.
	Set *cli.HostSet
	// Build assembles one Cell's daemon Config from its file config (main.go
	// cellConfig). An error isolates a named Cell; for the default Cell it
	// refuses the Host start.
	Build func(profile string, c cli.Config) (Config, error)
	// MaxConcurrent is the Host ceiling over every Cell (0 = the default).
	MaxConcurrent int
}

// cell is one registry entry.
type cell struct {
	profile string
	file    string
	// d is the Cell's runtime (nil while the Cell is invalid).
	d *Daemon
	// invalid names why the Cell has no runtime ("" = it has one).
	invalid string
	// removed marks a Cell removed at runtime (its state dir is kept).
	removed bool
	// stopped is closed once the Cell's Run returned; runErr is its result
	// (written before the close).
	stopped chan struct{}
	runErr  error
}

// Host is the assembled DaemonHost.
type Host struct {
	cfg HostConfig

	mu    sync.Mutex
	cells map[string]*cell

	// feed is the Host-global decision feed (<data-dir>/decisions.json,
	// decisions.go): every Cell records into it, each entry naming its
	// Cell.
	feed *decisionFeed

	lst net.Listener
	srv *http.Server

	shutOnce sync.Once
	shutDone chan struct{}

	// lifeMu serializes the Cell lifecycle operations (lifecycle.go); no
	// reader takes it.
	lifeMu sync.Mutex

	// slots is the Host ceiling over every Cell (hostslots.go).
	slots *hostSlots
	// admission is the Host's heavy-run admission every Cell's daemon-run
	// verification shares (verifyqueue.go).
	admission *verifyQueue
}

// NewHost assembles every Cell of the set. A default Cell that cannot be
// assembled refuses the Host (the error names it); a named Cell that cannot
// is recorded invalid.
func NewHost(hc HostConfig) (*Host, error) {
	h := &Host{cfg: hc, cells: map[string]*cell{}, shutDone: make(chan struct{})}
	h.slots = &hostSlots{max: hc.MaxConcurrent, tasks: &task.Slots{}, cells: h.servingDaemons}
	if h.slots.max <= 0 {
		h.slots.max = cli.DefaultMaxConcurrentJobs
	}
	h.admission = newVerifyQueue(0, 0)
	if hc.Set != nil {
		h.admission = newVerifyQueue(hc.Set.Host.Daemon.MaxConcurrentBars, hc.Set.Host.Daemon.BarLoadThreshold)
	}
	if err := os.MkdirAll(hc.DataDir, 0o755); err != nil {
		return nil, err
	}
	feed, err := newDecisionFeed(filepath.Join(hc.DataDir, decisionsFile))
	if err != nil {
		return nil, fmt.Errorf("Host decision feed: %w", err)
	}
	h.feed = feed
	defCfg, err := h.build(cellcfg.DefaultProfile, hc.Set.Host)
	if err != nil {
		return nil, fmt.Errorf("default Cell (%s): %w", hc.Set.HostPath, err)
	}
	d, err := New(defCfg)
	if err != nil {
		return nil, fmt.Errorf("default Cell (%s): %w", hc.Set.HostPath, err)
	}
	h.cells[cellcfg.DefaultProfile] = &cell{profile: cellcfg.DefaultProfile, file: hc.Set.HostPath, d: d}
	for _, c := range hc.Set.Cells {
		h.cells[c.Profile] = h.assembleNamed(c)
	}
	// A machine id names one Cell (the agent process markers carry it):
	// a named Cell sharing one with the default Cell or with another named
	// Cell is invalid — every named party, whatever the file order.
	byID := map[string][]string{}
	for p, e := range h.cells {
		if e.d != nil {
			byID[e.d.cfg.MachineID] = append(byID[e.d.cfg.MachineID], p)
		}
	}
	for id, ps := range byID {
		if len(ps) < 2 {
			continue
		}
		sort.Strings(ps)
		for _, p := range ps {
			if p != cellcfg.DefaultProfile {
				h.cells[p].d, h.cells[p].invalid = nil, machineIDConflict(p, id, ps)
			}
		}
	}
	return h, nil
}

// machineIDConflict answers why a Cell sharing its machine id is invalid.
func machineIDConflict(profile, id string, holders []string) string {
	return fmt.Sprintf("machine id %q is shared by the Cells %s — every Cell needs its own (its step and task processes are told apart by it): set a distinct [daemon] machine_id in Cell %q's file and delete that Cell's daemon-id file (%s; it already holds the shared id), then repair the Cell", id, strings.Join(holders, ", "), profile, filepath.Join(cellcfg.CellsDirName, profile, "daemon-id"))
}

// assembleNamed builds one named Cell's registry entry (invalid on any
// refusal: the file, a conflict, or the assembly).
func (h *Host) assembleNamed(c cli.CellConfig) *cell {
	e := &cell{profile: c.Profile, file: c.Path}
	if c.Invalid != "" {
		e.invalid = c.Invalid
		return e
	}
	cfg, err := h.build(c.Profile, c.Cfg)
	if err == nil {
		e.d, err = New(cfg)
	}
	if err != nil {
		e.d, e.invalid = nil, fmt.Sprintf("load failure: %v", err)
	}
	return e
}

// build assembles one Cell's Config and binds it to the Host: its profile
// and the Host-global decision feed.
func (h *Host) build(profile string, c cli.Config) (Config, error) {
	cfg, err := h.cfg.Build(profile, c)
	if err != nil {
		return cfg, err
	}
	cfg.Profile, cfg.SharedFeed = profile, h.feed
	cfg.HostSlots = h.slots
	cfg.SharedAdmission = h.admission
	hostPath := h.cfg.Set.HostPath
	if profile == cellcfg.DefaultProfile {
		cfg.LoadFile = func() (cli.Config, error) { return cli.LoadFileConfig(hostPath) }
	} else {
		cellPath := h.cellPath(profile)
		cfg.LoadFile = func() (cli.Config, error) {
			host, err := cli.LoadFileConfig(hostPath)
			if err != nil {
				return cli.Config{}, err
			}
			return cli.LoadCellFile(host, cellPath)
		}
	}
	return cfg, nil
}

// Run starts every Cell, serves the one socket, and returns when the Host
// shut down (or the default Cell failed to start).
func (h *Host) Run(ctx context.Context) error {
	// The Host's single-instance guard on the data-dir ROOT (daemon.lock
	// there is what `pf migrate cell-layout`, install.sh and `pf doctor`'s
	// restart command test); each Cell holds its own in its Cell dir.
	lock, err := acquireDataDirLock(h.cfg.DataDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	// Close the cell-layout rollback window before any Cell restores a job,
	// takes a submit or writes a subscription on the new layout.
	if err := stampServedLayout(h.cfg.DataDir); err != nil {
		return err
	}
	// Start the Cells; the default first: its failure refuses the Host.
	for _, e := range h.sortedCells() {
		if e.d == nil {
			continue
		}
		if err := h.startCell(e); err != nil {
			if e.profile == cellcfg.DefaultProfile {
				h.shutdownCells("default Cell start failure")
				return err
			}
			fmt.Fprintf(os.Stderr, "daemon: Cell %s isolated: %v\n", e.profile, err)
			continue
		}
		e.d.recordIdentity("")
		go e.d.adoptFileKey()
	}
	lst, err := listenHostUDS(h.cfg.SocketPath)
	if err != nil {
		h.shutdownCells("socket refused")
		return err
	}
	h.mu.Lock()
	h.lst = lst
	h.srv = &http.Server{Handler: h.handler(), ConnContext: credConnContext}
	srv := h.srv
	h.mu.Unlock()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lst) }()
	fmt.Fprintf(os.Stderr, "daemon: Host serving %d Cell(s) on %s\n", h.servingCount(), h.cfg.SocketPath)

	defCell := h.cell(cellcfg.DefaultProfile)
	for {
		select {
		case <-ctx.Done():
			return h.Shutdown(context.Background(), "caller-context: the Run caller's context is done")
		case <-h.shutDone:
			return nil
		case err := <-serveErr:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				_ = h.Shutdown(context.Background(), fmt.Sprintf("serve-error: %v", err))
				return err
			}
			<-h.shutDone
			return nil
		case <-defCell.stopped:
			// The default Cell ended on its own (its shutdown ran): the Host
			// goes with it.
			_ = h.Shutdown(context.Background(), "default Cell stopped")
			return defCell.runErr
		}
	}
}

// stampServedLayout closes the cell-layout rollback window (spec: after new
// business state exists, the pre-migration copy must not be restored): on
// a migrated data dir the Host stamps cells/default/migration-accepted-work
// when it first serves the new layout, before any Cell writes state there.
// A data dir no migration ran on has no window to close. A failed stamp
// refuses the start (the window must never stay open under new state).
func stampServedLayout(dataDir string) error {
	if _, err := os.Stat(filepath.Join(dataDir, cellcfg.MigrationDirName)); err != nil {
		return nil
	}
	path := filepath.Join(cellcfg.CellDir(dataDir, cellcfg.DefaultProfile), cellcfg.AcceptedWorkMarker)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("close the cell-layout rollback window: %w", err)
	}
	if err := atomicfile.Write(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return fmt.Errorf("close the cell-layout rollback window (%s): %w", path, err)
	}
	return nil
}

// startCell runs one Cell and waits for it to reach its serving point. A
// Cell that ends first is isolated (the default Cell's failure is the
// caller's to refuse).
func (h *Host) startCell(e *cell) error {
	d := e.d
	e.stopped = make(chan struct{})
	go func() {
		e.runErr = d.Run(context.Background())
		close(e.stopped)
	}()
	select {
	case <-d.ready:
		return nil
	case <-e.stopped:
		err := e.runErr
		if err == nil {
			err = errors.New("the Cell stopped during startup")
		}
		h.mu.Lock()
		e.d, e.invalid = nil, fmt.Sprintf("load failure: %v", err)
		h.mu.Unlock()
		return err
	case <-time.After(cellStartBound):
		_ = d.Shutdown(context.Background(), "Cell start exceeded its bound")
		h.mu.Lock()
		e.d, e.invalid = nil, fmt.Sprintf("load failure: the Cell did not reach its serving point within %v", cellStartBound)
		h.mu.Unlock()
		return fmt.Errorf("Cell %s did not start within %v", e.profile, cellStartBound)
	}
}

// Shutdown stops every Cell (in parallel) and then the face. Idempotent;
// concurrent callers wait for the first run.
func (h *Host) Shutdown(ctx context.Context, cause string) error {
	first := false
	h.shutOnce.Do(func() { first = true })
	if !first {
		select {
		case <-h.shutDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	fmt.Fprintf(os.Stderr, "daemon: Host shutdown: cause=%s\n", cause)
	h.shutdownCells(cause)
	h.mu.Lock()
	srv, lst := h.srv, h.lst
	h.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	} else if lst != nil {
		_ = lst.Close()
	}
	close(h.shutDone)
	return nil
}

// shutdownCells runs every live Cell's shutdown in parallel and waits for
// each Run to return.
func (h *Host) shutdownCells(cause string) {
	var wg sync.WaitGroup
	for _, e := range h.sortedCells() {
		h.mu.Lock()
		d, done := e.d, e.stopped
		h.mu.Unlock()
		if d == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.Shutdown(context.Background(), cause)
			if done != nil {
				select {
				case <-done:
				case <-time.After(wipLimit + 10*time.Second):
				}
			}
		}()
	}
	wg.Wait()
}

// sortedCells answers the registry entries, default first, then by profile.
// servingDaemons answers the serving Cells' runtimes in profile order.
func (h *Host) servingDaemons() []*Daemon {
	var out []*Daemon
	for _, e := range h.sortedCells() {
		h.mu.Lock()
		d := e.d
		h.mu.Unlock()
		if d != nil {
			out = append(out, d)
		}
	}
	return out
}

func (h *Host) sortedCells() []*cell {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*cell, 0, len(h.cells))
	for _, e := range h.cells {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].profile == cellcfg.DefaultProfile) != (out[j].profile == cellcfg.DefaultProfile) {
			return out[i].profile == cellcfg.DefaultProfile
		}
		return out[i].profile < out[j].profile
	})
	return out
}

func (h *Host) cell(profile string) *cell {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cells[profile]
}

func (h *Host) servingCount() int {
	n := 0
	for _, e := range h.sortedCells() {
		if e.d != nil {
			n++
		}
	}
	return n
}

// route resolves a request's Cell: the profile header names it, a request
// without one is served by the default Cell. An unknown profile, or a Cell
// with no live runtime, is an explicit DAEMON-404 — never another Cell.
func (h *Host) route(r *http.Request) (*Daemon, *api.Error) {
	profile := r.Header.Get(protocol.ProfileHeader)
	if profile == "" {
		profile = cellcfg.DefaultProfile
	}
	return h.routeTo(profile)
}

func (h *Host) routeTo(profile string) (*Daemon, *api.Error) {
	h.mu.Lock()
	e := h.cells[profile]
	var d *Daemon
	invalid := ""
	if e != nil {
		d, invalid = e.d, e.invalid
	}
	h.mu.Unlock()
	switch {
	case e == nil:
		return nil, &api.Error{Code: api.ErrDaemonNotFound, Detail: cellcfg.UnknownProfileDetail(profile, h.cfg.Set.Dir)}
	case d == nil:
		return nil, &api.Error{Code: api.ErrDaemonNotFound, Detail: fmt.Sprintf(
			"Cell %q has no live runtime: %s — nothing was sent to any other Cell", profile, invalid)}
	}
	return d, nil
}

// cellPath answers the file a profile's keys live in.
func (h *Host) cellPath(profile string) string {
	return filepath.Join(h.cfg.Set.Dir, cellcfg.CellFileName(profile))
}

// handler is the Host's route table: the Host-level routes, everything
// else forwarded to the routed Cell's own route table.
func (h *Host) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /profiles", h.profilesRoute)
	mux.HandleFunc("GET /status", h.statusRoute)
	mux.HandleFunc("POST /ops/config", h.opsConfigRoute)
	mux.HandleFunc("POST /shutdown", h.shutdownRoute)
	mux.HandleFunc("/aihub/{path...}", h.proxyRoute)
	h.lifecycleRoutes(mux)
	// The Host-global decision feed: one store for every
	// Cell, so the default Cell serves it (no profile); the dismiss POST
	// passes that Cell's write guard (step/task DAEMON-403).
	mux.HandleFunc("GET /decisions", h.defaultCellRoute)
	mux.HandleFunc("POST /decisions/dismiss", h.defaultCellRoute)
	mux.HandleFunc("/", h.forward)
	return h.guardHostWrites(mux)
}

// guardHostWrites refuses a mutating Host-level request (POST /shutdown,
// a profile-less POST /ops/config) from a step or task agent of any Cell, or
// from an unidentifiable peer — the same fail-closed accident guard every
// Cell's own routes keep (refuseStepOrigin). Cell-scoped requests pass
// through to forward, where the owning Cell's guard applies.
func (h *Host) guardHostWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostWrite := r.Method == http.MethodPost && (r.URL.Path == "/shutdown" ||
			(r.URL.Path == "/ops/config" && r.Header.Get(protocol.ProfileHeader) == ""))
		if hostWrite {
			if _, ok := requestPeerCred(r.Context()); !ok {
				writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("%s %s refused: the caller's identity (UDS peer credential) is unavailable — only a human session may drive the daemon", r.Method, r.URL.Path))
				return
			}
			if reason := agentRefusal(r, h.machineIDs()...); reason != "" {
				writeErr(w, api.ErrDaemonForbidden, reason)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// forward serves a Cell-scoped request with the routed Cell's handler. A
// request from a step or task agent's process tree is routed by the Host's
// own ownership record (agentOwner), never by its header: a header naming
// another Cell is refused DAEMON-403 (the header only routes, it never
// authorizes).
func (h *Host) forward(w http.ResponseWriter, r *http.Request) {
	if owner, who, ok := h.agentOwner(r); ok {
		if p := r.Header.Get(protocol.ProfileHeader); p != "" && p != owner {
			writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("%s %s refused: the caller is %s of Cell %q and the request names Cell %q — an agent acts only within its owning Cell", r.Method, r.URL.Path, who, owner, p))
			return
		}
		d, ae := h.routeTo(owner)
		if ae != nil {
			writeJSON(w, ae.HTTPStatus(), ae)
			return
		}
		d.handler().ServeHTTP(w, r)
		return
	}
	d, ae := h.route(r)
	if ae != nil {
		writeJSON(w, ae.HTTPStatus(), ae)
		return
	}
	d.handler().ServeHTTP(w, r)
}

// agentOwner resolves the Cell owning a step or task agent caller from the
// Host's own records: the peer's process tree carries a Cell's machine id
// (PF_DAEMON_ID) and the work item (PF_WI_ID) or task (PF_TASK_ID) it runs.
// Every Cell's machine id is its own (a shared one invalidates the named
// Cell at assembly), so the marker names exactly one Cell — never the
// first Cell that happens to hold an equal work item literal.
// ok=false: the caller is not an agent of any Cell (or the peer is
// unidentifiable — the Cell's own step-origin guard then applies).
func (h *Host) agentOwner(r *http.Request) (profile, who string, ok bool) {
	cred, credOK := requestPeerCred(r.Context())
	if !credOK {
		return "", "", false
	}
	for _, e := range h.sortedCells() {
		h.mu.Lock()
		d := e.d
		h.mu.Unlock()
		if d == nil {
			continue
		}
		if wi, isStep := runtimeStepOrigin(cred.pid, d.cfg.MachineID); isStep {
			return e.profile, "a step agent of work item " + wi, true
		}
		if id, isTask := runtimeTaskOrigin(cred.pid, d.cfg.MachineID); isTask {
			return e.profile, "the task agent " + id, true
		}
	}
	return "", "", false
}

// machineIDs answers every live Cell's machine id (the agent-origin checks
// look a caller up against all of them).
func (h *Host) machineIDs() []string {
	var out []string
	for _, e := range h.sortedCells() {
		if e.d != nil {
			out = append(out, e.d.cfg.MachineID)
		}
	}
	return out
}

// profilesRoute serves GET /profiles: every Cell, valid or not, and the
// effective workspace index of the Cells that serve.
func (h *Host) profilesRoute(w http.ResponseWriter, r *http.Request) {
	if reason := agentRefusal(r, h.machineIDs()...); reason != "" {
		writeErr(w, api.ErrDaemonForbidden, reason)
		return
	}
	resp := cli.ProfilesResponse{Dir: h.cfg.Set.Dir, Profiles: []cli.ProfileInfo{}, Index: []cellcfg.IndexEntry{}}
	for _, e := range h.sortedCells() {
		info := h.profileRow(e)
		resp.Profiles = append(resp.Profiles, info)
		if info.Valid {
			for _, ws := range info.Workspaces {
				resp.Index = append(resp.Index, cellcfg.IndexEntry{Profile: e.profile, Workspace: ws})
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// profileRow answers one Cell's registry row.
func (h *Host) profileRow(e *cell) cli.ProfileInfo {
	h.mu.Lock()
	d, invalid := e.d, e.invalid
	h.mu.Unlock()
	if d == nil {
		state := "invalid"
		if e.removed {
			state = "removed"
		}
		return cli.ProfileInfo{Profile: e.profile, File: e.file, State: state, Valid: false, Reason: invalid,
			Workspaces: []string{}, DataDir: h.cellDataDir(e.profile)}
	}
	return d.profileInfo(e.profile, e.file)
}

// cellDataDir answers a Cell's state directory.
func (h *Host) cellDataDir(profile string) string {
	return cellcfg.CellDir(h.cfg.DataDir, profile)
}

// statusRoute serves GET /status: the named Cell's own status when the
// request carries a profile, otherwise the Host aggregate — the default
// Cell's machine facts, every Cell's jobs (each row naming its profile),
// the Host queue counts and one block per Cell.
func (h *Host) statusRoute(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(protocol.ProfileHeader) != "" {
		h.forward(w, r)
		return
	}
	def, ae := h.routeTo(cellcfg.DefaultProfile)
	if ae != nil {
		writeJSON(w, ae.HTTPStatus(), ae)
		return
	}
	agg := def.statusSnapshot(r.URL.Query().Get("harnesses") == "1")
	agg.Jobs = []statusJob{}
	agg.Queue.Pending, agg.Queue.Running = 0, 0
	agg.Queue.MaxConcurrent = h.slots.Max()
	agg.BackgroundModelCalls = &cli.BackgroundCalls{Bound: task.Limit, Running: h.slots.tasks.Running()}
	for _, e := range h.sortedCells() {
		row := h.profileRow(e)
		agg.Cells = append(agg.Cells, row)
		h.mu.Lock()
		d := e.d
		h.mu.Unlock()
		if d == nil {
			continue
		}
		jobs, running := d.statusJobs()
		for i := range jobs {
			jobs[i].Profile = e.profile
		}
		agg.Jobs = append(agg.Jobs, jobs...)
		agg.Queue.Running += running
		agg.Queue.Pending += d.q.LenPending()
	}
	writeJSON(w, http.StatusOK, agg)
}

// opsConfigRoute serves POST /ops/config: with a profile, that Cell's
// switch and ceiling; without one, the Host — the auto switch of every
// serving Cell and the Host ceiling.
func (h *Host) opsConfigRoute(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(protocol.ProfileHeader) != "" {
		h.forward(w, r)
		return
	}
	var req struct {
		Auto *bool `json:"auto"`
		Max  *int  `json:"max"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Auto == nil && req.Max == nil {
		writeErr(w, api.ErrDaemonBadInput, "ops config requires auto and/or max")
		return
	}
	if req.Max != nil && *req.Max < 1 {
		writeErr(w, api.ErrDaemonBadInput, "max must be >= 1 (DAEMON-400)")
		return
	}
	if req.Max != nil {
		h.slots.SetMax(*req.Max)
	}
	var last opsConfigResponse
	for _, e := range h.sortedCells() {
		h.mu.Lock()
		d := e.d
		h.mu.Unlock()
		if d == nil {
			continue
		}
		cellReq := req
		if c := d.cellMaxConcurrentJobs(); req.Max != nil && c > 0 && c < *req.Max {
			// a Cell's own optional ceiling (as last applied) stays below the Host's
			m := c
			cellReq.Max = &m
		}
		body, _ := json.Marshal(cellReq)
		resp, err := d.applyOpsConfig(body)
		if err != nil {
			writeErrOf(w, err)
			return
		}
		if e.profile == cellcfg.DefaultProfile {
			last = resp
		}
	}
	if req.Max != nil {
		last.Max = *req.Max // the Host ceiling
	}
	writeJSON(w, http.StatusOK, last)
}

// shutdownRoute serves POST /shutdown: always the whole Host. A request
// naming a Cell is refused — a Cell has no shutdown of its own.
func (h *Host) shutdownRoute(w http.ResponseWriter, r *http.Request) {
	if p := r.Header.Get(protocol.ProfileHeader); p != "" {
		writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("shutdown is Host-level: it stops every Cell of this Host; it takes no profile (got %q) — run `pf ops shutdown` without --profile", p))
		return
	}
	cause := "route: POST /shutdown uds peer credential unavailable"
	if cred, ok := requestPeerCred(r.Context()); ok {
		cause = fmt.Sprintf("route: POST /shutdown uds peer pid=%d uid=%d", cred.pid, cred.uid)
	}
	writeJSON(w, http.StatusOK, map[string]any{"shutting_down": true})
	go func() { _ = h.Shutdown(context.Background(), cause) }()
}

// listenHostUDS binds the Host socket: a live listener there is another
// Host; a dead one is a stale socket file to remove.
func listenHostUDS(path string) (net.Listener, error) {
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		conn.Close()
		return nil, fmt.Errorf("daemon: another daemon is listening on %s", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	lst, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o660)
	return &credListener{Listener: lst}, nil
}

// The agent-origin lookups (seams over the runtime's process-tree walk).
var (
	runtimeStepOrigin = runtime.StepProcessOrigin
	runtimeTaskOrigin = runtime.TaskProcessOrigin
)

// defaultCellRoute serves a Host-global route (the decision feed) through
// the default Cell's handler (the store is shared; the header plays no
// part).
func (h *Host) defaultCellRoute(w http.ResponseWriter, r *http.Request) {
	r.Header.Del(protocol.ProfileHeader)
	h.forward(w, r)
}
