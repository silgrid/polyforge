package test

// v1_s19: the long-run leak probe — scenario A of the dual-scenario
// split the owner confirmed (grill q_f17c2e), driver/envelope edition.
// Every existing scenario runs one or a handful of jobs and never checks
// the daemon's steady-state hygiene after MANY consecutive work items; a
// per-job fd leak (sockets or spawn handles never closed) or duplicate
// writes to the durable events log would pass today's suite unnoticed.
// This scenario closes that gap at PROCESS level, against the REAL
// `pf daemon` binary running a long driver workload: 20 consecutive
// driver-pinned autopilot work items, each completing and sealing through
// the terminal wiring.
//
//   - fd count does not grow: the number of entries in
//     /proc/<daemon-pid>/fd, measured after all 20 jobs are wrapped
//     plus a bounded settle window for async teardown, must not exceed
//     the baseline captured after daemon start-up idle-settle and
//     BEFORE job 1.
//   - the events are clean: every line of <dataDir>/events/* is one
//     valid protocol.EventEntry, and no event occurrence is written
//     twice — a re-written/duplicated event line is the failure signal.
//     The per-work-item accounting is the healthy driver-run shape:
//     exactly one job_started and one job_stopped per work item (the
//     per-job completion runs through the recovery door without a
//     needs_recovery event; the accounting tolerates either).
//
// Division of labor (the dual-scenario split): fd leaks and event
// duplication are process-level realities, so the real binary is the
// most convincing carrier and this file carries them with ZERO
// production changes — /proc is read from outside. The GOROUTINE half of
// the probe lives in the companion scenario
// v1_s20_longrun_goroutines_test.go (the in-process rig).
import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// Probe constants. The settle windows are pinned deliberately generous
// for a suite that runs on this machine: 2s of start-up idle-settle
// lets the daemon's boot goroutines and the UDS listener reach steady
// state before the baseline, and 3s of post-run settle lets the last
// job's async teardown (step-process close, monitor exit, transport
// teardown) finish before the final measurement. The fd sample takes
// the MINIMUM of 5 reads 200ms apart so a transient fd (a just-closed
// socket not yet reaped, a concurrent status connection) can only make
// a measurement MORE conservative, never mask growth: a real leak is
// present in every read, so the minimum still carries it.
const (
	s19Jobs          = 20
	s19StartupSettle = 2 * time.Second
	s19FinalSettle   = 3 * time.Second
	s19FDSamples     = 5
	s19FDGap         = 200 * time.Millisecond
)

// s19FDCountsOf splits the entries of /proc/<pid>/fd — the process's open
// file descriptors (sockets, pipes and files alike) — into the daemon's TCP
// connections to the aihub at 127.0.0.1:hubPort and everything else. The
// hub client's transport keeps up to http.DefaultMaxIdleConnsPerHost idle
// keep-alive connections, and how many it holds depends on whether two hub
// calls ever overlapped (a loaded machine makes that likelier), so those
// are bounded separately instead of pinned to the baseline.
func s19FDCountsOf(pid, hubPort int) (hub, other int, err error) {
	ents, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return 0, 0, err
	}
	peer := fmt.Sprintf("0100007F:%04X", hubPort)
	for _, e := range ents {
		tgt, _ := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, e.Name()))
		if strings.HasPrefix(tgt, "socket:[") {
			// "tcp <local> <remote> ..." (s19SocketPeer)
			if f := strings.Fields(s19SocketPeer(tgt)); len(f) > 2 && f[0] == "tcp" && f[2] == peer {
				hub++
				continue
			}
		}
		other++
	}
	return hub, other, nil
}

// s19FDList returns the symlink targets of the process's open fds
// (readlink of every /proc/<pid>/fd entry) — the diagnosable face of a
// leak: WHICH descriptor grew, not just how many.
func s19FDList(pid int) []string {
	ents, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return []string{fmt.Sprintf("(fd list unreadable: %v)", err)}
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		tgt, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, e.Name()))
		if err != nil {
			tgt = "(unreadable)"
		}
		if strings.HasPrefix(tgt, "socket:[") {
			tgt += " (" + s19SocketPeer(tgt) + ")"
		}
		out = append(out, e.Name()+" -> "+tgt)
	}
	sort.Strings(out)
	return out
}

// s19SocketPeer classifies one socket inode against the net-namespace
// tables (tcp / tcp6 / unix) so a leak names its socket, not just a
// count. Best-effort: "unknown" when no table row matches.
func s19SocketPeer(inode string) string {
	inode = strings.TrimSuffix(strings.TrimPrefix(inode, "socket:["), "]")
	for _, spec := range []struct {
		path string
		kind string
	}{
		{"/proc/self/net/tcp", "tcp"},
		{"/proc/self/net/tcp6", "tcp6"},
		{"/proc/self/net/unix", "unix"},
		{"/proc/self/net/udp", "udp"},
	} {
		raw, err := os.ReadFile(spec.path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if s19LineHasField(line, inode) {
				return spec.kind + " " + strings.Join(strings.Fields(line)[1:], " ")
			}
		}
	}
	return "unknown"
}

func s19LineHasField(line, field string) bool {
	for _, f := range strings.Fields(line) {
		if f == field {
			return true
		}
	}
	return false
}

// s19MinFDCount samples the daemon's fd count n times, gap apart, and
// returns the minimum (see the constants block for why the minimum).
func s19MinFDCount(t *testing.T, pid, hubPort int, n int, gap time.Duration) (hub, other int) {
	t.Helper()
	hub, other = -1, -1
	for i := 0; i < n; i++ {
		h, o, err := s19FDCountsOf(pid, hubPort)
		if err != nil {
			t.Fatalf("fd count of daemon pid %d: %v", pid, err)
		}
		if hub < 0 || h < hub {
			hub = h
		}
		if other < 0 || o < other {
			other = o
		}
		if i < n-1 {
			time.Sleep(gap)
		}
	}
	return hub, other
}

// s19AssertEventsLogClean reads the daemon's whole durable events log
// and asserts the run's event hygiene:
//
//   - every non-empty line is one valid protocol.EventEntry (decodable
//     JSON object with a non-empty kind and an RFC3339Nano timestamp);
//   - no event occurrence is written twice. EventLog.Append is the only
//     writer and marshals the entry deterministically, so a re-written
//     event reproduces its line byte-for-byte: byte-identical duplicate
//     lines are exactly the failure signal, and two DISTINCT appends
//     can never collide because each carries its own nanosecond-precision
//     `at`. With every line valid and every line distinct, the total
//     line count equals the number of events this run actually produced;
//   - the per-work-item accounting is the healthy driver-run shape: every
//     one of the run's work items is present with exactly one
//     job_started and one job_stopped (a re-derivation or a double stop
//     would be per-job debris the probe must surface), and no line names
//     a work item outside the run.
func s19AssertEventsLogClean(t *testing.T, dataDir string, wis []string) {
	t.Helper()
	raw := eventsRaw(t, dataDir)
	run := map[string]bool{}
	for _, wi := range wis {
		run[wi] = true
	}
	lines := 0
	daemonScope := 0
	perWI := map[string]int{}
	started := map[string]int{}
	stopped := map[string]int{}
	seenLine := map[string]int{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines++
		var e protocol.EventEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("events line %d is not a valid protocol.EventEntry: %v (%q)", lines, err, line)
		}
		if e.Kind == "" {
			t.Fatalf("events line %d carries no kind: %q", lines, line)
		}
		if _, err := time.Parse(time.RFC3339Nano, e.At); err != nil {
			t.Fatalf("events line %d carries an invalid timestamp %q: %v", lines, e.At, err)
		}
		seenLine[line]++
		if e.WI == "" {
			daemonScope++ // daemon-scope bookkeeping (wi="" rows); whole-file by design
			continue
		}
		if !run[e.WI] {
			t.Fatalf("the events name a work item outside this run: %q", e.WI)
		}
		perWI[e.WI]++
		switch e.Kind {
		case "job_started":
			started[e.WI]++
		case "job_stopped":
			stopped[e.WI]++
		}
	}
	for line, n := range seenLine {
		if n > 1 {
			t.Fatalf("event occurrence written %d times (re-written/duplicated event line): %q", n, line)
		}
	}
	for _, wi := range wis {
		if perWI[wi] == 0 {
			t.Fatalf("work item %s produced no events", wi)
		}
		if started[wi] != 1 {
			t.Fatalf("work item %s saw %d job_started events, want exactly 1 (one attempt per job)", wi, started[wi])
		}
		if stopped[wi] != 1 {
			t.Fatalf("work item %s saw %d job_stopped events, want exactly 1 (one monitor lifecycle per job)", wi, stopped[wi])
		}
	}
	// Line count equals event count: every line above was a valid, unique
	// occurrence, so the produced-event count IS the line count.
	t.Logf("s19: events clean: %d lines, %d per-WI + %d daemon-scope, no duplicates", lines, lines-daemonScope, daemonScope)
}

func TestV1S19LongRunLeakProbe(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	d.start()
	pid := d.cmd.Process.Pid

	// Warm BOTH standing client paths BEFORE the baseline, so the
	// baseline captures the daemon's real steady state including the
	// connections an operator's session keeps open, and the only fd
	// deltas left after 20 jobs are real per-job leaks:
	//
	//   - the daemon's hub client shares one http.Transport whose
	//     keep-alive connection to the aihub is a persistent fd; if the
	//     first claim opened it after the baseline, that legitimate
	//     steady-state fd would read as post-run growth. One uncounted
	//     warm-up job (file, submit, wait for wrapped) exercises the
	//     same hub client before the baseline is taken.
	//   - the test's own in-process DaemonClient keeps its idle
	//     keep-alive connection open on the CLIENT side, which pins the
	//     daemon-side ACCEPTED socket open too (the server has no idle
	//     timeout); a first post-run status call would open it after the
	//     baseline. One status call now pins that accepted fd in the
	//     baseline instead.
	warm := inst.newWI(t, "s19: hub-client warm-up job", envelopeAutopilot(), nil)
	d.pf(t, "submit", warm)
	inst.waitWIStatus(t, warm, "wrapped", 120*time.Second)
	d.status(t)

	// Baseline: after start-up idle-settle, before job 1.
	time.Sleep(s19StartupSettle)
	baseHub, baseline := s19MinFDCount(t, pid, inst.port, s19FDSamples, s19FDGap)
	baselineList := s19FDList(pid) // captured at baseline time for failure diagnosis
	t.Logf("s19: daemon fd baseline after start-up settle: %d (+%d hub connections)", baseline, baseHub)

	// 20 strictly consecutive driver work items: each is an autopilot
	// workflow that completes and seals itself, and the next one starts
	// only after this one reached its terminal state.
	wis := make([]string, 0, s19Jobs)
	for i := 0; i < s19Jobs; i++ {
		wi := inst.newWI(t, fmt.Sprintf("s19: long-run leak probe job %02d", i+1),
			envelopeAutopilot(), nil)
		wis = append(wis, wi)
		d.pf(t, "submit", wi)
		inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	}

	// The daemon holds no job any more.
	// (the WI status flips to wrapped before the last job's sealing
	// bookkeeping drains, so allow it a moment)
	waitUntil(t, 30*time.Second, "daemon still holds jobs after the run", func() bool {
		return len(d.status(t).Jobs) == 0
	})

	// Every job's monitor reached its job_stopped event: the wrap
	// status is set by session-end, which precedes RTFinished, so
	// this waits out the last async event writes before measuring.
	waitUntil(t, 30*time.Second, "not every job reached its job_stopped event", func() bool {
		for _, wi := range wis {
			if !contains(d.eventKinds(t, wi), "job_stopped") {
				return false
			}
		}
		return true
	})

	// Final measurement: post-run settle, then the same min-of-samples
	// read. The bar is "does not grow": final must not exceed baseline,
	// and the hub keep-alive connections stay within the transport's idle
	// bound (a connection leaked per job is never returned to the pool, so
	// 20 jobs of it pass that bound; a single leaked one may hide inside it).
	time.Sleep(s19FinalSettle)
	finalHub, final := s19MinFDCount(t, pid, inst.port, s19FDSamples, s19FDGap)
	if final > baseline || finalHub > http.DefaultMaxIdleConnsPerHost {
		t.Logf("s19: baseline fd table:\n  %s", strings.Join(baselineList, "\n  "))
		t.Logf("s19: final fd table:\n  %s", strings.Join(s19FDList(pid), "\n  "))
		t.Fatalf("daemon fd count grew across %d consecutive jobs: baseline %d, final %d; hub connections %d (keep-alive bound %d)",
			s19Jobs, baseline, final, finalHub, http.DefaultMaxIdleConnsPerHost)
	}
	t.Logf("s19: daemon fd final after settle: %d (baseline %d), hub connections %d (baseline %d)", final, baseline, finalHub, baseHub)

	// The durable events log tells the whole run with no duplicates.
	s19AssertEventsLogClean(t, d.dataDir, append([]string{warm}, wis...))
}
