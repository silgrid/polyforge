package daemon

// query_test.go: the holder half of the query face. The snapshot is
// answered from the current state (no sleep-polling), the artifact path
// rules refuse escapes, and the read bound is enforced.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSafeArtifactPathMatrix(t *testing.T) {
	dir := t.TempDir()
	art := filepath.Join(dir, "artifacts")
	if err := os.MkdirAll(filepath.Join(art, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(art, "ok.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := safeArtifactPath(dir, "ok.txt"); err != nil {
		t.Fatalf("a clean artifact path must be accepted: %v", err)
	}
	if _, err := safeArtifactPath(dir, "sub/../ok.txt"); err != nil {
		t.Fatalf("a cleaned in-tree path must be accepted: %v", err)
	}
	for _, bad := range []string{"", "..", "../ok.txt", "sub/../../ok.txt", "/etc/passwd", strings.Repeat("../", 6) + "ok.txt"} {
		if _, err := safeArtifactPath(dir, bad); err == nil {
			t.Fatalf("artifact path %q must be refused", bad)
		}
	}
	// A symlink that points outside artifacts/ is refused.
	outside := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(art, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := safeArtifactPath(dir, "escape"); err == nil {
		t.Fatal("a symlink escaping artifacts/ must be refused")
	}
	// No artifacts/ directory at all is a not-found, never a fallback.
	if _, err := safeArtifactPath(t.TempDir(), "ok.txt"); err == nil {
		t.Fatal("a capsule without artifacts/ must refuse")
	}
}

func TestReadArtifactSizeBound(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_art")
	ji, ok := td.rt.Info("wi_art")
	if !ok || ji.CapsuleDir == "" {
		t.Fatalf("no local capsule for the claimed WI: %+v", ji)
	}
	art := filepath.Join(ji.CapsuleDir, "artifacts")
	if err := os.MkdirAll(art, 0o755); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(art, "big.bin")
	if err := os.WriteFile(big, make([]byte, artifactMaxBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := td.Daemon.readArtifact("wi_art", "big.bin"); err == nil {
		t.Fatal("an artifact over the 4 MiB read bound must be refused")
	}
	if err := os.WriteFile(filepath.Join(art, "ok.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	ans, err := td.Daemon.readArtifact("wi_art", "ok.txt")
	if err != nil || string(ans.Content) != "hello" {
		t.Fatalf("clean artifact read: %+v %v", ans, err)
	}
}

// TestProgressSnapshotAnswersImmediately: a held queue entry is queryable
// but not running; the answer is the current snapshot, never a 4 s wait
// for some later state (the removed sleep-polling loop).
func TestProgressSnapshotAnswersImmediately(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) { c.DispatchTick = time.Hour })
	if err := td.q.Enqueue(qEntry{WorkItemID: "wi_queued"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	started := time.Now()
	snap, err := td.Daemon.progressSnapshot("wi_queued")
	if err != nil {
		t.Fatalf("progress snapshot: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a queued snapshot waited %v (the sleep hold loop is back)", elapsed)
	}
	if !snap.Queued {
		t.Fatalf("queued state not projected: %+v", snap)
	}
}

// TestProgressSnapshotRefusesNotHeld: an aihub record alone is not a
// local hold; the holder-side "not held here" refusal must fire.
func TestProgressSnapshotRefusesNotHeld(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.hub.mu.Lock()
	td.hub.status["wi_elsewhere"] = "running"
	td.hub.mu.Unlock()
	_, err := td.Daemon.progressSnapshot("wi_elsewhere")
	if err == nil {
		t.Fatal("a WI this daemon does not hold must be refused")
	}
}
