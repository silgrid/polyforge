package runtime

// stderr_drain_test.go: — a harness's last words on stderr
// decide its exit's failure class (a provider's "model ... not found"
// switches the candidate), and the copy that carries them into the exit's
// stderr tail can lag the exit on a loaded host. The exit must still
// carry them: a lost tail turns a model-class death into exit_other and
// the step retries the same bad model.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

// setTailWriteHook runs fn at the top of every stderr tail write for the
// test's duration.
func setTailWriteHook(t *testing.T, fn func()) {
	t.Helper()
	prev := tailWriteHook.Swap(&fn)
	t.Cleanup(func() { tailWriteHook.Store(prev) })
}

// stallTailWrites stalls every stderr tail write by d for the test's
// duration (a descheduled copier on a loaded host).
func stallTailWrites(t *testing.T, d time.Duration) {
	t.Helper()
	setTailWriteHook(t, func() { time.Sleep(d) })
}

// slowCopyHarness writes a pi stand-in that takes its initial prompt and
// dies: on candidate prov/a with a provider's unresolvable-model error, on
// any other candidate with a host-class error (which blocks the step, so
// the run ends after the switch). Both texts reach the daemon only on
// stderr; the process writes nothing on stdout.
func slowCopyHarness(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-pi")
	script := "#!/bin/sh\n" +
		"read -r _ || true\n" +
		"case \"$*\" in\n" +
		"*'--model prov/a'*) echo '" + slowCopyProviderErr + "' >&2; exit 101 ;;\n" +
		"esac\n" +
		"echo 'write artifacts/x: no space left on device' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSlowStderrCopyStillSwitchesTheCandidate: the production chain —
// the pi spawner's process, the driver's step run and its exit
// classification, the candidate pointer — with every stderr tail write
// stalled 700ms (past the exit). The provider error must
// still classify the first dispatch exit_model_class and the second must
// run the next candidate.
func TestSlowStderrCopyStillSwitchesTheCandidate(t *testing.T) {
	stallTailWrites(t, 700*time.Millisecond)

	capDir := t.TempDir()
	stepDef := filepath.Join(capDir, capsule.DirWorkflows, "b", "steps", "s.md")
	if err := os.MkdirAll(filepath.Dir(stepDef), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stepDef, []byte("s: do the thing"), 0o644); err != nil {
		t.Fatal(err)
	}
	// workflow contract 4: every prompt carries the shared step header
	for _, name := range []string{"step-header", "handoff"} {
		p := filepath.Join(capDir, capsule.DirWorkflows, "_shared", name+".md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wi := driver.WIInfo{ID: "wi_slowcopy", CapsuleDir: capDir, BundleName: "b", SessionHandle: "ab12cd34"}
	wf := &driver.Workflow{Steps: []driver.StepSpec{{ID: "s"}}}
	ad, err := driver.NewStepSpawnAdapter(driver.StepSpawnAdapter{
		WI:           wi,
		Workflow:     wf,
		Spawner:      newPiSpawner(&Config{PiBin: slowCopyHarness(t)}),
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	d, err := driver.New(driver.Config{
		WI:       wi,
		Workflow: wf,
		Spawner:  ad,
		Compose: func() (map[string][]string, error) {
			return map[string][]string{"s": {"pi:prov/a", "pi:prov/b"}}, nil
		},
		Sleep: func(context.Context, time.Duration) {},
	})
	if err != nil {
		t.Fatalf("driver.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := d.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(capDir, capsule.ExecutionJSON))
	if err != nil {
		t.Fatal(err)
	}
	var x capsule.Execution
	if err := json.Unmarshal(raw, &x); err != nil {
		t.Fatalf("execution.json: %v", err)
	}
	st := x.Steps["s"]
	if st == nil || len(st.Attempts) < 2 {
		t.Fatalf("want two archived dispatches of s, got %+v", st)
	}
	first, second := st.Attempts[0], st.Attempts[1]
	if first.FailureCause != driver.FailExitModel || first.Candidate != "pi:prov/a" {
		t.Fatalf("first dispatch archived as %q on %q (detail %q), want %q on pi:prov/a: the provider error never reached the exit's stderr tail",
			first.FailureCause, first.Candidate, first.Detail, driver.FailExitModel)
	}
	if !strings.Contains(first.Detail, `model "prov/a" not found`) {
		t.Fatalf("first dispatch detail %q does not carry the provider error", first.Detail)
	}
	if second.Candidate != "pi:prov/b" {
		t.Fatalf("second dispatch ran %q, want the next candidate pi:prov/b (attempts %+v)", second.Candidate, st.Attempts)
	}
}

// startPiped starts script (sh -c) as its own group leader on fresh
// stdout/stderr pipes, as startHarness does, and returns the read ends.
func startPiped(t *testing.T, script string) (*exec.Cmd, *os.File, *os.File) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	outW.Close()
	errW.Close()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	return cmd, outR, errR
}

const slowCopyProviderErr = `upstream provider error: model "prov/a" not found`

// TestSlowStderrCopyBeatsADescendantHoldingThePipe: the realistic shape —
// a step's tool process outlives the harness and keeps both pipes open,
// on a host where the stderr copy lags the exit. Each pipe has its own
// bound (stdout's reader catches up at once, nothing was written there),
// and the exit waits for the copy to hold what the dead process wrote — never for
// the EOF the descendant withholds — so the exit is announced with the
// provider error, long before the stderr bound.
func TestSlowStderrCopyBeatsADescendantHoldingThePipe(t *testing.T) {
	stallTailWrites(t, 700*time.Millisecond)
	cmd, outR, errR := startPiped(t, "sleep 30 & echo '"+slowCopyProviderErr+"' >&2; exit 101")
	start := time.Now()
	got := make(chan spawn.ExitInfo, 1)
	newProcOpts(cmd, nil, outR, procOpts{stderr: errR, onExit: func(e spawn.ExitInfo) { got <- e }})
	select {
	case e := <-got:
		if e.Code != 101 || !strings.Contains(e.StderrTail, slowCopyProviderErr) || e.StderrIncomplete {
			t.Fatalf("exit = %+v, want code 101 with the provider error in a complete stderr tail", e)
		}
		if took := time.Since(start); took > stderrDrainBound/2 {
			t.Fatalf("exit announced after %s: the descendant's open pipe held it toward the %s stderr bound", took, stderrDrainBound)
		}
	case <-time.After(stderrDrainBound + 5*time.Second):
		t.Fatal("the exit was never announced")
	}
}

// gateTailWrites holds every stderr tail write until the returned
// release runs (it also runs at cleanup, before the hook is restored).
func gateTailWrites(t *testing.T) (release func()) {
	t.Helper()
	gate := make(chan struct{})
	setTailWriteHook(t, func() { <-gate })
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release
}

// awaitDone waits for the handle to turn Done (right after onExit returns).
func awaitDone(t *testing.T, p *proc) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !p.Done() {
		if time.Now().After(deadline) {
			t.Fatal("the handle never turned Done")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestStderrCopyPastItsBoundIsRecordedAndReread: a copy still behind when
// its bound passes (held on a gate) does not hold the exit, and does not
// pass silently — the announced ExitInfo, and ExitInfo after Done, say
// the tail is incomplete — and ExitInfo re-reads the tail on every call,
// so once the copy is released the same handle answers the provider
// error and a complete tail.
func TestStderrCopyPastItsBoundIsRecordedAndReread(t *testing.T) {
	release := gateTailWrites(t)
	cmd, outR, errR := startPiped(t, "echo '"+slowCopyProviderErr+"' >&2; exit 101")
	got := make(chan spawn.ExitInfo, 1)
	p := newProcOpts(cmd, nil, outR, procOpts{stderr: errR, stderrBound: 100 * time.Millisecond,
		onExit: func(e spawn.ExitInfo) { got <- e }})
	select {
	case e := <-got:
		if e.Code != 101 || !e.StderrIncomplete || strings.Contains(e.StderrTail, slowCopyProviderErr) {
			t.Fatalf("announced exit = %+v, want code 101 and the copy reported behind (incomplete, no provider text)", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the exit was never announced")
	}
	awaitDone(t, p)
	if info := p.ExitInfo(); info.Code != 101 || !info.StderrIncomplete || info.StderrTail != "" {
		t.Fatalf("ExitInfo while the copy is held = %+v, want code 101, incomplete, no tail", info)
	}
	release()
	deadline := time.Now().Add(5 * time.Second)
	for {
		info := p.ExitInfo()
		if !info.StderrIncomplete {
			if info.Code != 101 || !strings.Contains(info.StderrTail, slowCopyProviderErr) {
				t.Fatalf("ExitInfo after the copy caught up = %+v, want code 101 and the provider error", info)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ExitInfo never re-read the landed tail: %+v", info)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestInjectionFailureRecordsAnIncompleteTail: the startup-death error
// (injectionFailed) carries the harness's stderr, and when the copy has
// not caught up it says so instead of passing a partial tail silently.
func TestInjectionFailureRecordsAnIncompleteTail(t *testing.T) {
	release := gateTailWrites(t)
	cmd, outR, errR := startPiped(t, "echo '"+slowCopyProviderErr+"' >&2; exit 101")
	p := newProcOpts(cmd, nil, outR, procOpts{stderr: errR, stderrBound: 100 * time.Millisecond})
	// the harness died at startup (its exit announced past the bound, the
	// copy still held) before the prompt write failed
	awaitDone(t, p)
	cause := errors.New("write |1: broken pipe")
	if msg := injectionFailed(p, cause).Error(); !strings.Contains(msg, spawn.StderrIncompleteNote) || strings.Contains(msg, slowCopyProviderErr) {
		t.Fatalf("startup-death error with the copy held = %q, want the incomplete note and no provider text", msg)
	}
	release()
	deadline := time.Now().Add(5 * time.Second)
	for !p.stderrCopyCaughtUp() {
		if time.Now().After(deadline) {
			t.Fatal("the released copy never caught up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if msg := injectionFailed(p, cause).Error(); strings.Contains(msg, spawn.StderrIncompleteNote) || !strings.Contains(msg, slowCopyProviderErr) {
		t.Fatalf("startup-death error after the copy caught up = %q, want the provider text and no note", msg)
	}
}

// TestStaleEmptyStderrReadIsNotACatchUp: an empty read taken while the
// harness was still alive says nothing about the pipe after its exit. The
// copy is held between such a read and acting on it until the harness has
// written its provider error, exited and been reaped; it must not take
// that stale read for the catch-up. Every later tail write is slowed, so a
// copy that did would get the exit announced, "complete", without the
// error — exit_other and a same-model retry, flagged as if nothing were
// missing.
func TestStaleEmptyStderrReadIsNotACatchUp(t *testing.T) {
	var slow atomic.Bool
	setTailWriteHook(t, func() {
		if slow.Load() {
			time.Sleep(700 * time.Millisecond)
		}
	})
	cmd, outR, errR := startPiped(t, "echo warn >&2; sleep 0.5; echo '"+slowCopyProviderErr+"' >&2; exit 101")
	var once sync.Once
	held := make(chan bool, 1)
	got := make(chan spawn.ExitInfo, 1)
	newProcOpts(cmd, nil, outR, procOpts{
		stderr: errR,
		afterEmptyStderrRead: func(p *proc) {
			// an empty read before `warn` reached the tail (the copy's
			// first read can beat the harness's first write) is let
			// through; the one held is the first taken after it — during
			// the harness's sleep, with the provider error still to come
			if !strings.Contains(p.tail.String(), "warn") {
				return
			}
			once.Do(func() {
				// hold it until the harness is gone and reaped
				deadline := time.Now().Add(5 * time.Second)
				for !p.reaped.Load() && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				held <- p.reaped.Load()
				slow.Store(true)
			})
		},
		onExit: func(e spawn.ExitInfo) { got <- e },
	})
	select {
	case ok := <-held:
		if !ok {
			t.Fatal("the harness was never reaped while the copy held its empty read")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the copy never took an empty read")
	}
	select {
	case e := <-got:
		if e.Code != 101 || e.StderrIncomplete || !strings.Contains(e.StderrTail, slowCopyProviderErr) {
			t.Fatalf("announced exit = %+v, want code 101 with the provider error in a complete stderr tail: a pre-exit empty read was taken for the catch-up", e)
		}
	case <-time.After(stderrDrainBound + 5*time.Second):
		t.Fatal("the exit was never announced")
	}
}
