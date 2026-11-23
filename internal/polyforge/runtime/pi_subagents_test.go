package runtime

// The pi_subagents dispatch gate (polyforge#227 AC3, owner rulings
// 2026-11-22 Q1/Q2/Q3): off and empty never run a pi command; an invalid
// value is refused with its own class; on runs the built-in resource check
// and the compatibility probe and refuses THAT dispatch with a class when
// either fails; on with both passing dispatches exactly once, with the
// prompt built from the VERIFIED capability (never from the parameter).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

const compatGateAllFlags = `printf '%s\n' '--no-skills --no-context-files --no-prompt-templates --no-extensions --no-approve'`
const compatGateFourFlags = `printf '%s\n' '--no-skills --no-context-files --no-prompt-templates --no-extensions'`

// compatGateCfg writes a fake pi that records each invocation and reads the
// one prompt line the runtime writes; the script then holds the channel open
// so the spawn stays alive for the test to terminate.
func compatGateCfg(t *testing.T, helpScript string) (*Config, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	bin := filepath.Join(dir, "pi")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + logPath + "'\ncase \"$1\" in\n--version)\nprintf '1.4.0\\n'\nexit 0\n;;\n--help)\n" + helpScript + "\nexit 0\n;;\nesac\nIFS= read -r line\nprintf 'PROMPT %s\\n' \"$line\" >> '" + logPath + "'\ncat >/dev/null\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		PiBin:                    bin,
		DataDir:                  t.TempDir(),
		HarnessRoot:              t.TempDir(),
		OperatorAgentDir:         operatorDirFixture(t),
		OperatorAgentDirExplicit: true,
	}
	cfg.FillDefaults()
	return cfg, logPath
}

func compatGateLog(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func compatGateCounts(t *testing.T, path string) (discovery, steps int) {
	for _, line := range compatGateLog(t, path) {
		switch {
		case strings.Contains(line, "--version"), strings.Contains(line, "--help"):
			discovery++
		case strings.Contains(line, "--mode rpc"):
			steps++
		}
	}
	return discovery, steps
}

// compatGateWaitFor polls (bounded) for the asynchronous state the fake pi
// writes after Spawn returned — the step's prompt line — then answers the
// final counts. A fixed delay before one read would race the write (review
// r1#3, the settle duty).
func compatGateWaitFor(t *testing.T, logPath string, cond func(discovery, steps int, log string) bool) (discovery, steps int, log string) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		discovery, steps = compatGateCounts(t, logPath)
		log = strings.Join(compatGateLog(t, logPath), "\n")
		if cond(discovery, steps, log) {
			return discovery, steps, log
		}
		time.Sleep(10 * time.Millisecond)
	}
	return discovery, steps, log
}

func TestPiSubagentsDispatchGate(t *testing.T) {
	spawnGate := func(t *testing.T, cfg *Config, params map[string]string, callback func(spawn.Capability) (string, error)) (AgentHandle, error) {
		t.Helper()
		sp := newPiSpawner(cfg)
		return sp.Spawn(context.Background(), SpawnParams{
			WorkItemID:    "wi_gate",
			CapsuleDir:    t.TempDir(),
			SpawnN:        1,
			InitialPrompt: "",
			Params:        params,
			FinalPrompt:   callback,
		})
	}

	t.Run("off-and-empty-never-probe", func(t *testing.T) {
		for _, params := range []map[string]string{nil, {}, {"pi_subagents": ""}, {"pi_subagents": " off "}} {
			cfg, logPath := compatGateCfg(t, compatGateAllFlags)
			var calls atomic.Int32
			var verified atomic.Bool
			h, err := spawnGate(t, cfg, params, func(cap spawn.Capability) (string, error) {
				calls.Add(1)
				verified.Store(cap.PiSubagents)
				return "PROMPT-OFF", nil
			})
			if err != nil {
				t.Fatalf("params %v: off must dispatch: %v", params, err)
			}
			discovery, steps, _ := compatGateWaitFor(t, logPath, func(_, steps int, log string) bool {
				return steps == 1 && strings.Contains(log, "PROMPT-OFF")
			})
			if discovery != 0 {
				t.Errorf("params %v: a disabled switch ran %d discovery command(s)", params, discovery)
			}
			if calls.Load() != 1 || verified.Load() {
				t.Errorf("params %v: callback calls=%d verified=%v, want 1/false", params, calls.Load(), verified.Load())
			}
			if steps != 1 {
				t.Errorf("params %v: step processes = %d, want 1", params, steps)
			}
			_ = h.Terminate(2 * time.Second)
		}
	})

	t.Run("invalid-value", func(t *testing.T) {
		cfg, logPath := compatGateCfg(t, compatGateAllFlags)
		h, err := spawnGate(t, cfg, map[string]string{"pi_subagents": "yes"}, func(spawn.Capability) (string, error) {
			t.Error("an invalid switch value must not build a prompt")
			return "x", nil
		})
		if err == nil {
			_ = h.Terminate(2 * time.Second)
			t.Fatal("an invalid pi_subagents value must be refused")
		}
		if got := compatReason(err); got != "pi_subagents_invalid" {
			t.Fatalf("reason = %q, want pi_subagents_invalid (%v)", got, err)
		}
		for _, want := range []string{"pi_subagents", "yes", "want on or off"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
		discovery, steps := compatGateCounts(t, logPath)
		if discovery != 0 || steps != 0 {
			t.Errorf("an invalid value ran %d discovery command(s) and %d step process(es), want none", discovery, steps)
		}
	})

	t.Run("on-failing-check-refuses-dispatch", func(t *testing.T) {
		cfg, logPath := compatGateCfg(t, compatGateFourFlags)
		h, err := spawnGate(t, cfg, map[string]string{"pi_subagents": "on"}, func(spawn.Capability) (string, error) {
			t.Error("a refused dispatch must not build a prompt")
			return "x", nil
		})
		if err == nil {
			_ = h.Terminate(2 * time.Second)
			t.Fatal("on with a failing compatibility check must be refused")
		}
		if got := compatReason(err); got != "pi_flag" {
			t.Fatalf("reason = %q, want pi_flag (%v)", got, err)
		}
		_, steps := compatGateCounts(t, logPath)
		if steps != 0 {
			t.Errorf("a refused dispatch started %d step process(es), want none", steps)
		}
	})

	t.Run("on-passing-checks-dispatches-once", func(t *testing.T) {
		cfg, logPath := compatGateCfg(t, compatGateAllFlags)
		oldProbe := piCapabilityProbeFn
		defer func() { piCapabilityProbeFn = oldProbe }()
		var probes atomic.Int32
		piCapabilityProbeFn = func(ctx context.Context, bin, version string) error {
			probes.Add(1)
			return nil
		}
		var calls atomic.Int32
		var verified atomic.Bool
		h, err := spawnGate(t, cfg, map[string]string{"pi_subagents": "on"}, func(cap spawn.Capability) (string, error) {
			calls.Add(1)
			verified.Store(cap.PiSubagents)
			return "PROMPT-VERIFIED", nil
		})
		if err != nil {
			t.Fatalf("on with both checks passing must dispatch: %v", err)
		}
		// a synchronous fact: Spawn returned only after the probe finished.
		if probes.Load() != 1 {
			t.Errorf("the capability probe ran %d time(s), want 1", probes.Load())
		}
		if calls.Load() != 1 || !verified.Load() {
			t.Errorf("callback calls=%d verified=%v, want 1/true", calls.Load(), verified.Load())
		}
		_, steps, log := compatGateWaitFor(t, logPath, func(_, steps int, log string) bool {
			return steps == 1 && strings.Contains(log, "PROMPT-VERIFIED")
		})
		if steps != 1 {
			t.Errorf("step processes = %d, want exactly 1", steps)
		}
		if !strings.Contains(log, "PROMPT-VERIFIED") {
			t.Errorf("the verified prompt was never delivered to the step process: %v", log)
		}
		_ = h.Terminate(2 * time.Second)
	})

	t.Run("on-resource-failure-refuses", func(t *testing.T) {
		// owner ruling Q6: on without a daemon-owned step agent directory is
		// the resource class, refused before any probe or process.
		cfg, logPath := compatGateCfg(t, compatGateAllFlags)
		cfg.DataDir = ""
		h, err := spawnGate(t, cfg, map[string]string{"pi_subagents": "on"}, func(spawn.Capability) (string, error) {
			t.Error("a resource-refused dispatch must not build a prompt")
			return "x", nil
		})
		if err == nil {
			_ = h.Terminate(2 * time.Second)
			t.Fatal("on without a step agent directory must be refused as a resource failure")
		}
		if got := compatReason(err); got != "pi_resources" {
			t.Fatalf("reason = %q, want pi_resources (%v)", got, err)
		}
		if !strings.Contains(err.Error(), "embedded resource validation failed") {
			t.Errorf("error %q lacks the resource-validation text", err)
		}
		discovery, steps := compatGateCounts(t, logPath)
		if discovery != 0 || steps != 0 {
			t.Errorf("a resource-refused dispatch ran %d discovery command(s) and %d step process(es), want none", discovery, steps)
		}
	})

	t.Run("callback-failure-refuses", func(t *testing.T) {
		cfg, _ := compatGateCfg(t, compatGateAllFlags)
		oldProbe := piCapabilityProbeFn
		defer func() { piCapabilityProbeFn = oldProbe }()
		piCapabilityProbeFn = func(ctx context.Context, bin, version string) error { return nil }
		h, err := spawnGate(t, cfg, map[string]string{"pi_subagents": "on"}, func(spawn.Capability) (string, error) {
			return "", errors.New("no prompt")
		})
		if err == nil {
			_ = h.Terminate(2 * time.Second)
			t.Fatal("a failed final-prompt build must refuse the dispatch")
		}
	})
}
