package daemon

import (
	"os"
	"strings"
	"testing"
)

// TestCleanupIsExplicitAndComplete: default and a configured Cell are
// refused; without the confirmation only the impact is reported; with it
// the state dir and every decision feed entry naming the profile are gone,
// and a repeated cleanup is a no-op.
func TestCleanupIsExplicitAndComplete(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	dir := th.cell("alpha").d.cfg.DataDir
	if err := th.feed.RecordStatus("alpha", "wi_a", "a", "blocked"); err != nil {
		t.Fatal(err)
	}
	if err := th.feed.RecordStatus("default", "wi_d", "d", "blocked"); err != nil {
		t.Fatal(err)
	}
	if _, err := th.cleanupProfile("default", "default"); err == nil {
		t.Fatal("default must never be cleaned")
	}
	if _, err := th.cleanupProfile("alpha", "alpha"); err == nil || !strings.Contains(err.Error(), "remove") {
		t.Fatalf("a configured Cell must be refused (remove first): %v", err)
	}
	if err := th.removeProfile("alpha"); err != nil {
		t.Fatal(err)
	}
	im, err := th.cleanupProfile("alpha", "")
	if err != nil || im.Applied || !im.StateDirExists || im.Decisions != 1 {
		t.Fatalf("impact report = %+v %v", im, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the impact report deleted the state dir: %v", err)
	}
	if _, err := th.cleanupProfile("alpha", "beta"); err == nil {
		t.Fatal("a mismatched confirmation must be refused")
	}
	im, err = th.cleanupProfile("alpha", "alpha")
	if err != nil || !im.Applied {
		t.Fatalf("cleanup = %+v %v", im, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the state dir survived the cleanup: %v", err)
	}
	if n, _ := th.feed.PurgeProfile("alpha", false); n != 0 {
		t.Fatalf("%d feed entr(y/ies) still name alpha", n)
	}
	if n, _ := th.feed.PurgeProfile("default", false); n != 1 {
		t.Fatalf("another Cell's entry was dropped (left %d)", n)
	}
	if im, err := th.cleanupProfile("alpha", "alpha"); err != nil || !im.Applied {
		t.Fatalf("a repeated cleanup must be a no-op: %+v %v", im, err)
	}
}
