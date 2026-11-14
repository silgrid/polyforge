package cli

// w350_migrate_lock_test.go (D1): a mutating
// `pf migrate cell-layout` takes the data dir's Host lock before its first
// check — a held lock refuses it with nothing changed — and releases it
// when the run ends; a completed layout re-applies as the "already on the
// cell layout" no-op.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

func w350OldDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range map[string]string{"daemon-id": "m-old\n", "events/1.json": "{}\n"} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestW350MigrateHoldsTheHostLock(t *testing.T) {
	dir := w350OldDataDir(t)
	sockEnv := map[string]string{"PF_DAEMON_SOCKET": filepath.Join(t.TempDir(), "none.sock")}
	held, err := cellcfg.HoldHostLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"--apply", "--rollback"} {
		code, _, stderr := runPf(t, sockEnv, "migrate", "cell-layout", mode, "--data-dir", dir)
		if code == 0 || !strings.Contains(stderr, cellcfg.HostLockFile) || !strings.Contains(stderr, "nothing was changed") {
			t.Fatalf("migrate %s under a held Host lock: exit %d (%q)", mode, code, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, cellcfg.LayoutVersionFile)); err == nil {
		t.Fatal("a refused apply wrote layout-version")
	}
	_ = held.Close()
	if code, _, stderr := runPf(t, sockEnv, "migrate", "cell-layout", "--apply", "--data-dir", dir); code != 0 {
		t.Fatalf("apply: exit %d (%q)", code, stderr)
	}
	after, err := cellcfg.HoldHostLock(dir)
	if err != nil {
		t.Fatalf("the migration did not release the Host lock: %v", err)
	}
	_ = after.Close()
	code, stdout, stderr := runPf(t, sockEnv, "migrate", "cell-layout", "--apply", "--data-dir", dir)
	if code != 0 || !strings.Contains(stdout, "already on the cell layout") {
		t.Fatalf("re-apply of a completed layout: exit %d (%q / %q)", code, stdout, stderr)
	}
}
