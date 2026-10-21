package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/testenv"
)

// TestMain isolates HOME/XDG/POLYFORGE_* so no test can touch the real install.
func TestMain(m *testing.M) { testenv.Main(m) }

// TestBootstrapAdminKeyFile pins repo review 2026-10-16 V-10: a fresh
// install writes the bootstrap key to <data-dir>/bootstrap-admin.key
// (0600) and the key authenticates; an already-seeded database writes
// nothing.
func TestBootstrapAdminKeyFile(t *testing.T) {
	dir := t.TempDir()
	db, err := core.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	path := bootstrapAdmin(db, dir)
	if path != filepath.Join(dir, "bootstrap-admin.key") {
		t.Fatalf("key file = %q", path)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v (%v), want 0600", fi.Mode().Perm(), err)
	}
	raw, _ := os.ReadFile(path)
	key := strings.TrimSpace(string(raw))
	sum := sha256.Sum256([]byte(key))
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id='usr_root' AND key_hash=?1`, hex.EncodeToString(sum[:])).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the key in the file does not match the seeded key (%d, %v)", n, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if again := bootstrapAdmin(db, dir); again != "" {
		t.Fatalf("second bootstrap on a seeded db = %q, want nothing", again)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("second bootstrap wrote a key file (%v)", err)
	}
}
