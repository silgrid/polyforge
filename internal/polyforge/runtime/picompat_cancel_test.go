package runtime

// The compatibility gate's cancellation contract (polyforge#227 AC1,
// owner ruling Q2: an external cancellation keeps its own reason, is never
// reported as a pi incompatibility, and is never cached; Q4: a flight whose
// waiters have all left is recovered, so the next same-key caller probes
// again). Review r1#1: the initiator must observe its OWN cancellation too,
// not only a joining caller.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// compatWaitForFile polls (bounded) for a marker a child process writes, so
// the test cancels only once the stage it means to interrupt has started.
func compatCancelVersionPi(t *testing.T) (bin, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "version-started")
	bin = filepath.Join(dir, "pi")
	body := `#!/bin/sh
case "$1" in
--version)
printf 'started\n' > '` + marker + `'
sleep 30
;;
--help)
printf '%s\n' '--no-skills --no-context-files --no-prompt-templates --no-extensions --no-approve'
;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, marker
}

func TestPiCompatibilityCancellation(t *testing.T) {
	resetPiCompatCache()
	t.Cleanup(resetPiCompatCache)
	oldProbe := piCapabilityProbeFn
	t.Cleanup(func() { piCapabilityProbeFn = oldProbe })

	t.Run("version-stage", func(t *testing.T) {
		bin, marker := compatCancelVersionPi(t)
		// a 10s fallback deadline bounds the test; the cancel under test is
		// the explicit one, fired once the stage has proved it started.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		go func() {
			until := time.Now().Add(8 * time.Second)
			for time.Now().Before(until) {
				if _, err := os.Stat(marker); err == nil {
					cancel()
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		}()
		start := time.Now()
		err := validatePiCompatibility(ctx, bin)
		elapsed := time.Since(start)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a caller cancel during the version stage returned %v, want a context.Canceled error", err)
		}
		if got := compatReason(err); got != "" {
			t.Fatalf("reason = %q, want none: an external cancellation is not a pi incompatibility", got)
		}
		if elapsed > 2*time.Second {
			t.Errorf("the canceled version stage returned after %s, want the caller's own cancel to end it promptly", elapsed)
		}
	})

	t.Run("probe-stage", func(t *testing.T) {
		bin, _ := compatCountingPi(t)
		var probes atomic.Int32
		started := make(chan struct{}, 1)
		piCapabilityProbeFn = func(ctx context.Context, bin, version string) error {
			probes.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return ctx.Err()
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		type answer struct {
			err     error
			elapsed time.Duration
		}
		done := make(chan answer, 1)
		go func() {
			t0 := time.Now()
			err := validatePiCompatibility(ctx, bin)
			done <- answer{err: err, elapsed: time.Since(t0)}
		}()
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("the capability probe never started")
		}
		cancel()
		var got answer
		select {
		case got = <-done:
		case <-time.After(12 * time.Second):
			t.Fatal("the canceled initiator never returned")
		}
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("an initiator cancel during the probe returned %v, want a context.Canceled error", got.err)
		}
		if reason := compatReason(got.err); reason != "" {
			t.Fatalf("reason = %q, want none: an external cancellation is not a pi incompatibility", reason)
		}
		if got.elapsed > 2*time.Second {
			t.Errorf("the canceled initiator returned after %s, want its own cancel to end the wait promptly", got.elapsed)
		}
		// the abandoned flight is recovered and NOT cached: the next
		// same-key caller probes again.
		piCapabilityProbeFn = func(ctx context.Context, bin, version string) error {
			probes.Add(1)
			return nil
		}
		if err := validatePiCompatibility(context.Background(), bin); err != nil {
			t.Fatalf("the call after a canceled flight failed: %v", err)
		}
		if n := probes.Load(); n != 2 {
			t.Fatalf("after a canceled flight the next same-key call ran %d probe(s), want 2 (a canceled flight is never cached)", n)
		}
	})
}
