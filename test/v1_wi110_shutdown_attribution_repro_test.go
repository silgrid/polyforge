package test

// v1_wi110_shutdown_attribution_repro_test.go: the polyforge#110 REPRO
// artifact, authored by the repro step of work item wi_C3cH6eRg6OEv
// (unattributed graceful shutdown of the production daemon during the
// 2026-10-09 unattended run).
//
// The reported failure: the daemon exited through its graceful shutdown
// sequence with NO operator action and no attributable caller. main.go
// wires SIGTERM/SIGINT to the same bounded graceful sequence the
// POST /shutdown route triggers, so the log signature does NOT
// distinguish `pf ops shutdown` from a signal — the exit leaves no
// cause record (route vs signal, signal number, or the UDS peer
// credential for route-originated shutdowns).
//
// This test encodes the work item's fix ask #1 (attribution first) as
// executable assertions. At authoring time (base commit a8a616e) every
// assertion FAILS for the reported reason: neither shutdown trigger
// writes any cause attribution, and the two log signatures are the
// same.
//
// After the shutdown-cause logging lands, all assertions must pass:
//
//   - Case A (signal): a SIGTERM delivered to the daemon pid runs the
//     graceful sequence, and the daemon log carries a line that names
//     BOTH the shutdown and the signal cause (SIGTERM / signal 15) —
//     the signal number per the fix ask.
//   - Case B (route): a POST /shutdown dialed over the daemon's UDS
//     socket FROM THIS TEST PROCESS runs the graceful sequence, and the
//     daemon log carries a line that names BOTH the shutdown and this
//     process's pid — the SO_PEERCRED UDS peer credential per the fix
//     ask (the test process is the socket peer, so its pid is exactly
//     what SO_PEERCRED must report).
//
// Both cases run with a live mid-step driver WI (delayed envelope
// write), mirroring the production incident shape: two driver WIs were
// mid-flight when the daemon exited. The attribution lines are written
// at sequence ENTRY, before the latch. On failure the test prints every
// shutdown-related log
// line from both runs as the observed evidence.

import (
	"context"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// wi110SignalCauseRe accepts a line that attributes a shutdown to its
// signal cause: it must mention the shutdown AND the signal, naming
// SIGTERM or "signal 15" (the signal number per the fix ask).
var wi110SignalCauseRe = regexp.MustCompile(`(?i)shutdown[^\n]*(sigterm|signal[^\n]*15)`)

// wi110RoutePidRe builds the route-attribution matcher: the line must
// mention the shutdown and the UDS peer pid (SO_PEERCRED of the
// connection this test process dialed).
func wi110RoutePidRe(pid int) *regexp.Regexp {
	return regexp.MustCompile(`(?i)shutdown[^\n]*pid[^\n]*` + strconv.Itoa(pid))
}

func TestV1WI110ShutdownAttributionRepro(t *testing.T) {
	inst := newV1Install(t)

	// bootWithLiveWI mirrors the production incident shape: a daemon
	// with one driver WI mid-step (delayed envelope write) when the
	// shutdown lands. The flush(shutdown) window is shortened (1s) via
	// the real daemon flag: the driver WI has no flush face, so the
	// wait still degrades on timeout (the production degrade path,
	// reached in 1s instead of 60s) and the incident shape — a live
	// step process interrupted mid-flight — is unchanged. The
	// attribution under assertion happens at sequence entry, before
	// any flush waiting.
	bootWithLiveWI := func(label string) (*v1Daemon, string) {
		t.Helper()
		wi := inst.newWI(t, label, []map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true, "ms": 60000},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
		d := newV1Daemon(t, inst)
		d.start()
		d.pf(t, "submit", wi)
		waitSpawnCount(t, d, wi, 1, 30*time.Second)
		return d, wi
	}

	// wi110Scrub removes the test-name noise: the harness temp dirs embed
	// the test name (which contains "Shutdown") in every drop= path inside
	// the driver's alive lines — a false-positive vector for every match
	// below, so both the assertions and the evidence dump run on scrubbed
	// text.
	wi110Scrub := strings.ToLower("TestV1WI110ShutdownAttributionRepro")
	wi110Clean := func(log string) string {
		return strings.ReplaceAll(strings.ToLower(log), wi110Scrub, "")
	}

	// wi110ReadLog returns the FULL daemon log (logTail caps at 4KB).
	wi110ReadLog := func(d *v1Daemon) string {
		raw, err := os.ReadFile(d.logPath)
		if err != nil {
			t.Fatalf("read daemon log: %v", err)
		}
		return string(raw)
	}

	// wi110ShutdownLines extracts every log line mentioning a shutdown —
	// the log signature a post-mortem has to attribute from (on scrubbed
	// text; the printed line stays verbatim).
	wi110ShutdownLines := func(log string) []string {
		var out []string
		for i, line := range strings.Split(log, "\n") {
			if strings.Contains(strings.Split(wi110Clean(log), "\n")[i], "shutdown") {
				out = append(out, line)
			}
		}
		return out
	}

	// wi110DumpEvidence prints the observed evidence on failure.
	wi110DumpEvidence := func(logA, logB string) {
		t.Logf("CASE A (SIGTERM) shutdown-related log lines:\n%s", strings.Join(wi110ShutdownLines(logA), "\n"))
		t.Logf("CASE B (POST /shutdown over UDS) shutdown-related log lines:\n%s", strings.Join(wi110ShutdownLines(logB), "\n"))
	}

	// ---------------- Case A: signal-originated shutdown ----------------
	dA, _ := bootWithLiveWI("wi110 repro A: signal-originated graceful shutdown")
	if err := dA.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM the daemon: %v", err)
	}
	if !dA.exitedWithin(90 * time.Second) {
		t.Fatalf("daemon did not exit gracefully after SIGTERM (log tail: %s)", dA.logTail())
	}
	logA := wi110ReadLog(dA)
	cleanA := wi110Clean(logA)

	// ---------------- Case B: route-originated shutdown ----------------
	dB, _ := bootWithLiveWI("wi110 repro B: route-originated graceful shutdown")
	// Dial the UDS face from THIS process and POST /shutdown: the
	// connection's SO_PEERCRED peer pid is os.Getpid().
	peerPID := os.Getpid()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var nd net.Dialer
			return nd.DialContext(ctx, "unix", dB.sock)
		},
	}
	hc := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	resp, err := hc.Post("http://pf.invalid/shutdown", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST /shutdown over UDS: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /shutdown answered %d, want 200", resp.StatusCode)
	}
	if !dB.exitedWithin(90 * time.Second) {
		t.Fatalf("daemon did not exit gracefully after POST /shutdown (log tail: %s)", dB.logTail())
	}
	logB := wi110ReadLog(dB)
	cleanB := wi110Clean(logB)

	// ---------------- The attribution assertions ----------------
	//
	// The reported failure, stated as the contract: after ANY graceful
	// shutdown the daemon log must carry the shutdown CAUSE — enough to
	// attribute the exit in one read, with no other evidence.

	failed := false

	if line := wi110SignalCauseRe.FindString(cleanA); line == "" {
		failed = true
		t.Errorf("CASE A (SIGTERM, signal 15): the daemon log carries no shutdown-cause attribution naming the signal — the post-mortem cannot distinguish a signal-originated graceful exit from an operator shutdown (polyforge#110)")
	} else {
		t.Logf("case A attribution line: %s", line)
	}

	if line := wi110RoutePidRe(peerPID).FindString(cleanB); line == "" {
		failed = true
		t.Errorf("CASE B (POST /shutdown over UDS from pid %d): the daemon log carries no shutdown-cause attribution naming the UDS peer credential — a route-originated graceful exit is indistinguishable from a signal-originated one (polyforge#110)", peerPID)
	} else {
		t.Logf("case B attribution line: %s", line)
	}

	if failed {
		wi110DumpEvidence(logA, logB)
	}
}
