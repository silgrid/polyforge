package runtime

// stdout_drain_test.go: a harness's last stdout record can decide its
// exit's failure class — pi ends an errored turn with an agent_end whose
// stopReason is "error", and a provider's "model ... not found" there
// switches the candidate. The reader that hands that record to the taps
// (the translator, the supervisor's per-dispatch state) can lag the exit
// on a loaded host. The exit must still carry the record: a lost error turn
// turns a model-class death into exit_other and the step retries the same
// bad model.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// slowStdoutErrTurn is pi's last record of a turn a provider error ended.
const slowStdoutErrTurn = `{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"upstream provider error: model \"prov/a\" not found"}],"willRetry":false}`

// slowRecordSpawner slows every stdout record by d before the dispatch's
// OnRecord sees it: a stdout reader descheduled on a loaded host.
type slowRecordSpawner struct {
	inner spawn.Spawner
	d     time.Duration
}

func (s slowRecordSpawner) Spawn(ctx context.Context, p spawn.Params) (spawn.Handle, error) {
	if on := p.OnRecord; on != nil {
		p.OnRecord = func(l string) { time.Sleep(s.d); on(l) }
	}
	return s.inner.Spawn(ctx, p)
}

// slowStdoutHarness writes a pi stand-in that takes its initial prompt and
// dies: on candidate prov/a it ends with the provider's error turn on
// stdout (nothing on stderr), on any other candidate with a host-class
// error on stderr (which blocks the step, so the run ends after the
// switch).
func slowStdoutHarness(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-pi")
	script := "#!/bin/sh\n" +
		"read -r _ || true\n" +
		"case \"$*\" in\n" +
		"*'--model prov/a'*) echo '" + slowStdoutErrTurn + "'; exit 1 ;;\n" +
		"esac\n" +
		"echo 'write artifacts/x: no space left on device' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSlowStdoutReaderStillSwitchesTheCandidate: the production chain —
// the pi spawner's process, the adapter's translator and the supervisor
// link, the driver's step run and its exit classification, the candidate
// pointer — with every stdout record slowed 700ms on its way to the taps.
// The error turn must still classify the first dispatch exit_model_class
// and the second must run the next candidate.
func TestSlowStdoutReaderStillSwitchesTheCandidate(t *testing.T) {
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
	wi := driver.WIInfo{ID: "wi_slowout", CapsuleDir: capDir, BundleName: "b", SessionHandle: "ab12cd34"}
	wf := &driver.Workflow{Steps: []driver.StepSpec{{ID: "s"}}}
	// the adapter builds the translator only with a link; the driver reads
	// the dispatch's turn state through the same one
	link := supervise.NewLink("wi_slowout")
	ad, err := driver.NewStepSpawnAdapter(driver.StepSpawnAdapter{
		WI:           wi,
		Workflow:     wf,
		Link:         link,
		Spawner:      slowRecordSpawner{inner: newPiSpawner(&Config{PiBin: slowStdoutHarness(t)}), d: 700 * time.Millisecond},
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	d, err := driver.New(driver.Config{
		WI:       wi,
		Workflow: wf,
		Spawner:  ad,
		Link:     link,
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
		t.Fatalf("first dispatch archived as %q on %q (detail %q), want %q on pi:prov/a: the error turn never reached the taps before the exit",
			first.FailureCause, first.Candidate, first.Detail, driver.FailExitModel)
	}
	if !strings.Contains(first.Detail, `model "prov/a" not found`) {
		t.Fatalf("first dispatch detail %q does not carry the provider error", first.Detail)
	}
	if second.Candidate != "pi:prov/b" {
		t.Fatalf("second dispatch ran %q, want the next candidate pi:prov/b (attempts %+v)", second.Candidate, st.Attempts)
	}
}

// TestSlowStdoutReaderBeatsADescendantHoldingThePipe: a step's tool
// process outlives the harness and keeps both pipes open, on a host where
// the stdout reader lags the exit (a tap slowed 700ms). The exit must be
// announced only once the taps saw the error turn — waiting for the reader
// to hold what the dead process wrote, never for the EOF the descendant
// withholds — so well before the stdout bound. The descendant (sleep 30,
// in the harness's process group) is killed at cleanup by startPiped.
func TestSlowStdoutReaderBeatsADescendantHoldingThePipe(t *testing.T) {
	cmd, outR, errR := startPiped(t, "sleep 30 & echo '"+slowStdoutErrTurn+"'; exit 1")
	start := time.Now()
	var tapped atomic.Bool
	got := make(chan bool, 1)
	newProcOpts(cmd, nil, outR, procOpts{
		stderr: errR,
		taps: []func(string){func(l string) {
			time.Sleep(700 * time.Millisecond)
			if strings.Contains(l, `"agent_end"`) {
				tapped.Store(true)
			}
		}},
		onExit: func(spawn.ExitInfo) { got <- tapped.Load() },
	})
	select {
	case ok := <-got:
		if !ok {
			t.Fatal("exit announced before the stdout taps saw the error-turn record")
		}
		if took := time.Since(start); took >= 4*time.Second {
			t.Fatalf("exit announced after %s: the descendant's open pipe held it toward the stdout bound", took)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the exit was never announced")
	}
}

// TestStdoutReaderPastItsBoundIsRecordedAndReread: a stdout reader still
// behind when its bound passes (its tap held on a gate) does not hold the
// exit, and does not pass silently — the announced ExitInfo, and
// ExitInfo after Done, say the stdout records are incomplete — and
// ExitInfo reads the catch-up on every call, so once the tap is released
// and the record has reached it the same handle answers complete.
func TestStdoutReaderPastItsBoundIsRecordedAndReread(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	cmd, outR, errR := startPiped(t, "echo '"+slowStdoutErrTurn+"'; exit 1")
	var tapped atomic.Bool
	got := make(chan spawn.ExitInfo, 1)
	p := newProcOpts(cmd, nil, outR, procOpts{
		stderr:      errR,
		stdoutBound: 100 * time.Millisecond,
		taps: []func(string){func(l string) {
			<-gate
			if strings.Contains(l, `"agent_end"`) {
				tapped.Store(true)
			}
		}},
		onExit: func(e spawn.ExitInfo) { got <- e },
	})
	select {
	case e := <-got:
		if e.Code != 1 || !e.StdoutIncomplete || tapped.Load() {
			t.Fatalf("announced exit = %+v (tap saw the record: %v), want code 1 and the reader reported behind (incomplete, record not yet tapped)", e, tapped.Load())
		}
	case <-time.After(stdoutDrainBound + 5*time.Second):
		t.Fatal("the exit was never announced")
	}
	awaitDone(t, p)
	if info := p.ExitInfo(); info.Code != 1 || !info.StdoutIncomplete || tapped.Load() {
		t.Fatalf("ExitInfo while the reader is held = %+v (tap saw the record: %v), want code 1, incomplete, record not yet tapped", info, tapped.Load())
	}
	release()
	deadline := time.Now().Add(5 * time.Second)
	for {
		info := p.ExitInfo()
		if !info.StdoutIncomplete {
			if info.Code != 1 || !tapped.Load() {
				t.Fatalf("ExitInfo after the reader caught up = %+v (tap saw the record: %v), want code 1 and the record tapped", info, tapped.Load())
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ExitInfo never turned complete after the reader was released: %+v", info)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
