package runtime

// The compatibility verdict's cache (polyforge#227 AC2, owner rulings
// 2026-11-22 Q4/D4): one in-memory SUCCESS cache keyed by the executable
// target (resolved path + entry fingerprint), the version and the built-in
// resource digest. A same-key repeat does not probe again; a same-key
// concurrent batch probes once; a failure is never cached; any key dimension
// changing probes again. The capability call is counted through the probe
// seam — the real probe's own evidence is TestPiCompatibilityRealPi.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
)

// compatCountingPi writes a fake pi whose version comes from a separate
// file: rewriting that file changes the version while the entry point's
// bytes — the target fingerprint — stay identical.
func compatCountingPi(t *testing.T) (bin, versionFile string) {
	t.Helper()
	dir := t.TempDir()
	versionFile = filepath.Join(dir, "version")
	if err := os.WriteFile(versionFile, []byte("1.4.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "pi")
	body := "#!/bin/sh\ncase \"$1\" in\n--version)\ncat '" + versionFile + "'\n;;\n--help)\nprintf '%s\\n' '--no-skills --no-context-files --no-prompt-templates --no-extensions --no-approve'\n;;\nesac\nexit 0\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, versionFile
}

func compatCountedProbe(counter *atomic.Int32, delay time.Duration) func(context.Context, string, string) error {
	return func(ctx context.Context, bin, version string) error {
		counter.Add(1)
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
}

func TestPiCompatibilityCache(t *testing.T) {
	resetPiCompatCache()
	t.Cleanup(resetPiCompatCache)
	oldFS := piCompatResourceFS
	piCompatResourceFS = compatResourceMap(t)
	t.Cleanup(func() { piCompatResourceFS = oldFS })
	oldProbe := piCapabilityProbeFn
	t.Cleanup(func() { piCapabilityProbeFn = oldProbe })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var probes atomic.Int32
	piCapabilityProbeFn = compatCountedProbe(&probes, 0)

	// same key, sequential: one probe
	binA, versionA := compatCountingPi(t)
	for i := 0; i < 2; i++ {
		if err := validatePiCompatibility(ctx, binA); err != nil {
			t.Fatalf("compatibility check %d failed: %v", i, err)
		}
	}
	if got := probes.Load(); got != 1 {
		t.Errorf("same-key sequential checks probed %d time(s), want 1", got)
	}

	// same key, concurrent: one flight
	binB, _ := compatCountingPi(t)
	probes.Store(0)
	piCapabilityProbeFn = compatCountedProbe(&probes, 50*time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := validatePiCompatibility(ctx, binB); err != nil {
				t.Errorf("concurrent compatibility check failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := probes.Load(); got != 1 {
		t.Errorf("16 concurrent same-key checks probed %d time(s), want 1", got)
	}

	// failure is not cached: the next call probes again
	binC, _ := compatCountingPi(t)
	probes.Store(0)
	fail := errors.New("probe failed")
	var failed atomic.Bool
	failed.Store(true)
	piCapabilityProbeFn = func(ctx context.Context, bin, version string) error {
		probes.Add(1)
		if failed.Load() {
			return fail
		}
		return nil
	}
	if err := validatePiCompatibility(ctx, binC); !errors.Is(err, fail) {
		t.Fatalf("the first check returned %v, want the probe failure", err)
	}
	failed.Store(false)
	if err := validatePiCompatibility(ctx, binC); err != nil {
		t.Fatalf("the retry after a failed probe returned %v", err)
	}
	if got := probes.Load(); got != 2 {
		t.Errorf("a failed probe then a retry probed %d time(s), want 2 (failure must not be cached)", got)
	}

	// the executable target changes: probe again
	binD, _ := compatCountingPi(t)
	probes.Store(0)
	piCapabilityProbeFn = compatCountedProbe(&probes, 0)
	if err := validatePiCompatibility(ctx, binD); err != nil {
		t.Fatal(err)
	}
	if err := validatePiCompatibility(ctx, binA); err != nil {
		t.Fatal(err)
	}
	if got := probes.Load(); got != 2 {
		t.Errorf("a different executable target probed %d time(s), want 2", got)
	}

	// the version changes (same entry bytes): probe again
	probes.Store(0)
	if err := os.WriteFile(versionA, []byte("1.4.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validatePiCompatibility(ctx, binA); err != nil {
		t.Fatal(err)
	}
	if got := probes.Load(); got != 1 {
		t.Errorf("a changed version probed %d time(s), want 1", got)
	}

	// any built-in file's digest changes: probe again
	probes.Store(0)
	changed := compatResourceMap(t)
	changed["extensions/guard/rules.ts"] = &fstest.MapFile{Data: []byte("// changed\n")}
	piCompatResourceFS = changed
	if err := validatePiCompatibility(ctx, binA); err != nil {
		t.Fatal(err)
	}
	if got := probes.Load(); got != 1 {
		t.Errorf("a changed resource digest probed %d time(s), want 1", got)
	}
}

// TestEmbeddedResourceDigest pins the digest's authority and coverage: every
// embedded file — worker, subagent (index, agents, SOURCE), guard (index,
// rules) — is enumerated from the tree itself, so a changed byte or path
// changes the digest and the same content in another map order does not.
func TestEmbeddedResourceDigest(t *testing.T) {
	oldFS := piCompatResourceFS
	piCompatResourceFS = stepagentassets.FS
	t.Cleanup(func() { piCompatResourceFS = oldFS })
	base, err := piEmbeddedResourceDigest()
	if err != nil {
		t.Fatal(err)
	}
	if base == "" {
		t.Fatal("the embedded resource digest is empty")
	}
	// the tree's own enumeration: exactly the six embedded files, the guard
	// and SOURCE.md included, the guard's node test files excluded.
	var files []string
	if err := fs.WalkDir(stepagentassets.FS, ".", func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		files = append(files, p)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	want := []string{
		"agents/worker.md",
		"extensions/guard/index.ts",
		"extensions/guard/rules.ts",
		"extensions/subagent/SOURCE.md",
		"extensions/subagent/agents.ts",
		"extensions/subagent/index.ts",
	}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Fatalf("the embedded resource tree is %v, want %v", files, want)
	}
	for _, rel := range want {
		clone := compatResourceMap(t)
		raw := clone[rel]
		mutated := append([]byte(nil), raw.Data...)
		mutated[0] ^= 0xff
		clone[rel] = &fstest.MapFile{Data: mutated}
		piCompatResourceFS = clone
		got, err := piEmbeddedResourceDigest()
		if err != nil {
			t.Fatal(err)
		}
		if got == base {
			t.Errorf("changing %s left the digest unchanged", rel)
		}
	}
	// a renamed path is a different resource set
	renamed := compatResourceMap(t)
	renamed["extensions/guard/moved.ts"] = renamed["extensions/guard/rules.ts"]
	delete(renamed, "extensions/guard/rules.ts")
	piCompatResourceFS = renamed
	if got, err := piEmbeddedResourceDigest(); err != nil || got == base {
		t.Errorf("a renamed resource left the digest at %q (err %v)", got, err)
	}
	// identical content and paths: one digest, whatever the map's order
	piCompatResourceFS = compatResourceMap(t)
	again, err := piEmbeddedResourceDigest()
	if err != nil {
		t.Fatal(err)
	}
	if again != base {
		t.Errorf("the same resource content digested as %q, want %q", again, base)
	}
}
