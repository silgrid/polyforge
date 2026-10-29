package driver

// rewind_input_infra_retry_repro_test.go: the repro of — a
// code/fix step that a review FAIL rewound loses the review's verdict when
// its first (rewound) dispatch dies of an upstream/model error: the retry
// is dispatched again with no rewind input, so it re-submits the previous
// tree unchanged. Seen in production as / #40
// (`failed_rewind_exhausted`, review rounds repeating "HEAD is still …").
//
// Written RED against the current driver. The scripted fake harness alone
// leaves the capsule spawn journal empty, which makes the crash-restoration
// path (rewindReentryOrder, item 6) mistake a dispatch that
// DID run and die for one that never reached its journal line — masking the
// failure. The journaling spawner below writes the same journal line the
// real StepSpawnAdapter writes at every successful spawn, so the retry is
// the honest "the previous attempt already ran and died" shape.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// journalingSpawner wraps the scripted spawner and appends the capsule
// spawn-journal line for every spawn that produced a process — the durable
// fact the driver's rewind-restoration keys on.
type journalingSpawner struct {
	*fakeSpawner
	dir string
}

func (s *journalingSpawner) SpawnStep(ctx context.Context, sp StepSpawn) (StepRun, error) {
	run, err := s.fakeSpawner.SpawnStep(ctx, sp)
	if err != nil {
		return run, err
	}
	if err := s.journal(sp); err != nil {
		return nil, err
	}
	return run, nil
}

func (s *journalingSpawner) journal(sp StepSpawn) error {
	stateDir := filepath.Join(s.dir, capsule.DirRuntimeState)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	entry := capsule.SpawnJournalEntry{
		At:         time.Now().UTC().Format(time.RFC3339Nano),
		WorkItem:   sp.WIID,
		StepID:     sp.StepID,
		SpawnN:     int(sp.SpawnN),
		Resume:     sp.Resume,
		Transcript: filepath.Join(s.dir, capsule.DirSession, "session-repro.jsonl"),
	}
	blob, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(stateDir, capsule.SpawnJournalFile),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(blob, '\n')); err != nil {
		return err
	}
	return nil
}

// infraRetryEnv builds the two-step lane (code + the review gate that
// rewinds it) over the journaling scripted spawner. models is the code
// step's claim-time candidate chain (nil = no chain: the retry keeps the
// same candidate).
func infraRetryEnv(t *testing.T, models ...string) *supEnv {
	t.Helper()
	return newSupEnv(t, func(c *Config) {
		// the model-error backoff must not sleep the test
		c.Sleep = func(context.Context, time.Duration) {}
		// ExecPath is <capsule root>/runtime-state/execution.json, so the
		// capsule root (where the driver reads the journal) is two levels up.
		capsuleRoot := filepath.Dir(filepath.Dir(c.ExecPath))
		c.Spawner = &journalingSpawner{fakeSpawner: c.Spawner.(*fakeSpawner), dir: capsuleRoot}
		if models != nil {
			list := make([]string, 0, len(models))
			for _, m := range models {
				if !strings.HasPrefix(m, "pi:") && !strings.HasPrefix(m, "claude:") {
					m = "pi:" + m
				}
				list = append(list, m)
			}
			c.Compose = composeOf(map[string][]string{"code": list})
		}
	}, spec("code"), gateSpec("review", "code"))
}

// The reported failure, main path: code is rewound by the review FAIL, the
// rewound dispatch dies of a model-class upstream error (502), and the
// retry that follows must still carry the FAIL verdict.
func TestRewindVerdictSurvivesModelErrorRetrySameCandidate(t *testing.T) {
	env := infraRetryEnv(t)
	env.sp.script = []fakeEntry{
		done(`{}`), // code, spawn 1
		gateFAIL(), // review, spawn 2 — rewinds code carrying "FAIL: tests red"
		exitErr(1, "502 Bad Gateway: upstream error", true), // code, spawn 3 — the rewound dispatch dies model-class
		done(`{}`),                 // code, spawn 4 — the retry
		done(`{"verdict":"PASS"}`), // review, spawn 5
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	spawns := env.sp.dispatched()
	if len(spawns) != 5 {
		t.Fatalf("dispatched %v, want 5 (code, review FAIL, dead code retry-path, code retry, review PASS)", spawns)
	}
	if got := env.sp.spawnAt(2); got.StepID != "code" || got.SpawnN != 3 {
		t.Fatalf("setup: dispatch #3 must be the rewound code attempt, got %s@%d", got.StepID, got.SpawnN)
	}
	if ri := env.sp.spawnAt(2).RewindInput; ri == nil || !strings.Contains(ri.Reason, "FAIL: tests red") {
		t.Fatalf("setup: the first rewound dispatch must consume the review FAIL verdict, got %+v", ri)
	}

	retry := env.sp.spawnAt(3)
	if retry.StepID != "code" || retry.SpawnN != 4 {
		t.Fatalf("setup: dispatch #4 must be the retried code attempt, got %s@%d (%v)", retry.StepID, retry.SpawnN, spawns)
	}
	if ri := retry.RewindInput; ri == nil || !strings.Contains(ri.Reason, "FAIL: tests red") {
		t.Fatalf("REPORTED FAILURE: after the rewound code dispatch died of a model-class upstream error, the retry (spawn %d) was dispatched with rewind input %+v — the review FAIL verdict (\"FAIL: tests red\") was lost, so the retry cannot act on the feedback and re-submits the old tree unchanged; dispatches: %v", retry.SpawnN, ri, spawns)
	}
}

// The same loss on the fresh retry shape: a model-class error advances the
// candidate chain, and the fresh dispatch must still carry the verdict.
func TestRewindVerdictSurvivesModelErrorRetryOnFreshCandidate(t *testing.T) {
	env := infraRetryEnv(t, "model-a", "model-b")
	env.sp.script = []fakeEntry{
		done(`{}`),
		gateFAIL(),
		exitErr(1, "502 Bad Gateway: upstream error", true),
		done(`{}`),
		done(`{"verdict":"PASS"}`),
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	spawns := env.sp.dispatched()
	if len(spawns) != 5 {
		t.Fatalf("dispatched %v, want 5", spawns)
	}
	retry := env.sp.spawnAt(3)
	if retry.StepID != "code" || retry.SpawnN != 4 {
		t.Fatalf("setup: dispatch #4 must be the retried code attempt, got %s@%d (%v)", retry.StepID, retry.SpawnN, spawns)
	}
	if got := env.models()[3]; got != "model-b" {
		t.Fatalf("setup: the model-class death must advance the chain, dispatch #4 ran on %q", got)
	}
	if ri := retry.RewindInput; ri == nil || !strings.Contains(ri.Reason, "FAIL: tests red") {
		t.Fatalf("REPORTED FAILURE: the fresh (candidate-switched) retry (spawn %d, model %q) was dispatched with rewind input %+v — the review FAIL verdict was lost across the model-class retry; dispatches: %v", retry.SpawnN, retry.Model, ri, spawns)
	}
}

// The same loss across a daemon death: the rewound dispatch died of the
// upstream error, the daemon was killed before (or during) the retry, and
// the restarted driver re-dispatches the step — that dispatch must still
// carry the FAIL verdict.
func TestRewindVerdictSurvivesDaemonRestartAfterModelError(t *testing.T) {
	env := infraRetryEnv(t)
	env.sp.script = []fakeEntry{
		done(`{}`),
		gateFAIL(),
		exitErr(1, "502 Bad Gateway: upstream error", true),
		{hold: true}, // the in-process retry — the daemon dies while it is in flight
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- env.d.Run(ctx) }()
	waitUntil(t, "the retry dispatch", func() bool { return len(env.sp.dispatched()) == 4 })
	cancel()
	if err := <-errCh; err == nil {
		t.Fatal("the killed driver must not report a clean stop")
	}

	// the restart: a fresh driver over the durable capsule, the recovery
	// lane's daemon_restart archive, the spawn-journal's max ordinal seed.
	cfg := env.d.cfg
	cfg.SpawnN = 4
	cfg.Interrupted = &InterruptedDispatch{Detail: FailDaemonRestart}
	cfg.Spawner = &journalingSpawner{fakeSpawner: env.sp, dir: filepath.Dir(filepath.Dir(cfg.ExecPath))}
	env.sp.script = []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}
	restarted, err := New(cfg)
	if err != nil {
		t.Fatalf("restarted New: %v", err)
	}
	if err := restarted.Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}

	spawns := env.sp.dispatched()
	if len(spawns) != 6 {
		t.Fatalf("dispatched %v, want 6 (…, the in-flight retry, the restart's retry, review PASS)", spawns)
	}
	retry := env.sp.spawnAt(4)
	if retry.StepID != "code" || retry.SpawnN != 5 {
		t.Fatalf("setup: the restart's dispatch must be the retried code attempt, got %s@%d (%v)", retry.StepID, retry.SpawnN, spawns)
	}
	if ri := retry.RewindInput; ri == nil || !strings.Contains(ri.Reason, "FAIL: tests red") {
		t.Fatalf("REPORTED FAILURE: after the daemon died during the model-error retry, the restarted driver dispatched code (spawn %d) with rewind input %+v — the review FAIL verdict (\"FAIL: tests red\") was lost across the restart; dispatches: %v", retry.SpawnN, ri, spawns)
	}
}
