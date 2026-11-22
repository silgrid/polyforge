package runtime

// The compatibility gate's single wall-clock budget (polyforge#227 AC1,
// owner rulings 2026-11-22 Q5/D5): version discovery, the flag check, the
// capability probe and its cleanup share ONE 10-second deadline — not 10
// seconds per stage — and the timeout path recovers the process group it
// started (TERM, then KILL, then a bounded wait), leaving no surviving
// descendant.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// compatHangingPi writes a fake pi that answers the two short stages and
// then, in RPC mode, ignores TERM and leaves a child holding its stdout.
// Both pids are recorded so the test can judge the process facts itself.
func compatHangingPi(t *testing.T) (bin, pidFile string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "pi")
	pidFile = filepath.Join(dir, "pids")
	body := `#!/bin/sh
case "$1" in
--version) printf '9.9.9\n'; exit 0 ;;
--help) printf '%s\n' '--no-skills --no-context-files --no-prompt-templates --no-extensions --no-approve'; exit 0 ;;
esac
trap '' TERM
sleep 300 &
child=$!
printf '%s %s\n' "$$" "$child" > "` + pidFile + `"
sleep 300
wait
`
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, pidFile
}

func compatAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func TestPiCompatibilityTotalBudgetAndReap(t *testing.T) {
	bin, pidFile := compatHangingPi(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// the hung process group is this test's own: never leave it behind even
	// on the red run.
	t.Cleanup(func() {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		for _, f := range strings.Fields(string(raw)) {
			if pid, err := strconv.Atoi(f); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	start := time.Now()
	err := validatePiCompatibility(ctx, bin)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a target whose capability call never answers must be refused")
	}
	if got := compatReason(err); got != "pi_timeout" {
		t.Fatalf("reason = %q, want pi_timeout (%v)", got, err)
	}
	for _, want := range []string{"pi compatibility rejected [pi_timeout]", "compatibility probe exceeded 10s total budget"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	// one budget for the whole check, plus the 250ms observation tolerance
	// the owner ruling allows (the production timeout itself stays 10s).
	if elapsed > 10*time.Second+250*time.Millisecond {
		t.Errorf("the compatibility check took %s, over the 10s total budget", elapsed)
	}
	// it must actually use the budget, not give up early.
	if elapsed < 9*time.Second {
		t.Errorf("the compatibility check returned after %s: the budget was not spent on the hung call", elapsed)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the hung fake pi recorded no pids: %v", err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 2 {
		t.Fatalf("unexpected pid record %q", string(raw))
	}
	var pids []int
	for _, f := range fields {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("unexpected pid record %q", string(raw))
		}
		pids = append(pids, pid)
	}
	// the direct process is reaped by Wait; its TERM-ignoring child dies by
	// the group KILL. Poll briefly for the kernel's state to settle.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range pids {
			if compatAlive(pid) {
				alive = true
			}
		}
		if !alive {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, pid := range pids {
		if compatAlive(pid) {
			t.Errorf("probe process %d survived the timeout cleanup", pid)
		}
	}
}
