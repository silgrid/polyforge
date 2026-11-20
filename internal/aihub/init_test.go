package aihub

// init_test.go — polyforge#176 (84-components-C): the local init entry
// `pf aihub init` — the ticket's settled Q1 shape. One function,
// TestAihubInit: the plaintext lands in the 0600 --key-out file and
// matches the store's unrevoked key; a step or task environment is
// refused; a retry reissues (Q2) without duplicating the user; the
// bootstrap-admin key file is neither read nor written; the entry
// refuses beside a live server's lock; the plaintext never reaches
// stdout.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

// initRun runs `pf aihub init --data-dir D --key-out K` and answers
// (exit, stdout, stderr).
func initRun(t *testing.T, dataDir, keyOut string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := initMain([]string{"--data-dir", dataDir, "--key-out", keyOut}, nil, &out, &errb)
	return code, out.String(), errb.String()
}

// initStoreKeys reads the unrevoked key hashes of the store's one user.
func initStoreKeys(t *testing.T, dataDir string) map[string]string {
	t.Helper()
	db, err := core.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT key_hash, name FROM api_keys WHERE revoked_at IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var hash, name string
		if err := rows.Scan(&hash, &name); err != nil {
			t.Fatal(err)
		}
		out[hash] = name
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAihubInit(t *testing.T) {
	t.Run("the plaintext lands in the 0600 key file and matches the store", func(t *testing.T) {
		dir := t.TempDir()
		keyOut := filepath.Join(t.TempDir(), "init.key")
		code, out, errb := initRun(t, dir, keyOut)
		if code != 0 {
			t.Fatalf("pf aihub init on an empty store must exit 0 (code %d):\n%s%s", code, out, errb)
		}
		fi, err := os.Stat(keyOut)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("the key file's mode = %v (%v), want 0600", fi.Mode().Perm(), err)
		}
		raw, err := os.ReadFile(keyOut)
		if err != nil {
			t.Fatal(err)
		}
		key := strings.TrimSpace(string(raw))
		if key == "" {
			t.Fatal("the key file must carry the plaintext")
		}
		keys := initStoreKeys(t, dir)
		if _, ok := keys[core.Sha256Hex(key)]; !ok {
			t.Fatalf("the key file's plaintext must match the store's unrevoked key (store has %v)", keys)
		}
		// the plaintext is a credential: it must never reach stdout or
		// the diagnostics
		if strings.Contains(out, key) || strings.Contains(errb, key) {
			t.Fatal("the issued plaintext must never reach stdout or stderr")
		}
	})

	t.Run("a step or task environment is refused", func(t *testing.T) {
		for _, marker := range []string{"PF_WI_ID", "PF_TASK_ID"} {
			t.Setenv(marker, "wi_test")
			dir := t.TempDir()
			keyOut := filepath.Join(t.TempDir(), "init.key")
			code, _, errb := initRun(t, dir, keyOut)
			if code == 0 {
				t.Fatalf("%s set: the init entry must refuse to run (it is the workstation boot's own entry, never a step's)", marker)
			}
			if !strings.Contains(errb, marker) {
				t.Fatalf("the refusal must name the marker %s: %q", marker, errb)
			}
			if _, err := os.Stat(filepath.Join(dir, "aihub.db")); !os.IsNotExist(err) {
				t.Fatalf("a refused run must open no store: %v", err)
			}
			t.Setenv(marker, "")
		}
	})

	t.Run("a retry reissues the key without duplicating the user", func(t *testing.T) {
		dir := t.TempDir()
		key1 := filepath.Join(t.TempDir(), "init.key")
		if code, out, errb := initRun(t, dir, key1); code != 0 {
			t.Fatalf("first run: %d:\n%s%s", code, out, errb)
		}
		first, err := os.ReadFile(key1)
		if err != nil {
			t.Fatal(err)
		}
		key2 := filepath.Join(t.TempDir(), "init-2.key")
		if code, out, errb := initRun(t, dir, key2); code != 0 {
			t.Fatalf("retry: %d:\n%s%s", code, out, errb)
		}
		second, err := os.ReadFile(key2)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(first)) == strings.TrimSpace(string(second)) {
			t.Fatal("a retry must issue a fresh key (the lost plaintext is unrecoverable, Q2)")
		}
		db, err := core.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var users, revoked int
		if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil || users != 1 {
			t.Fatalf("users = %d (%v), want exactly 1 — a retry must find, never duplicate", users, err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE revoked_at IS NOT NULL`).Scan(&revoked); err != nil || revoked != 1 {
			t.Fatalf("revoked = %d (%v), want 1 — the retry must revoke the same-named key (Q2)", revoked, err)
		}
	})

	t.Run("bootstrap-admin.key is neither read nor written", func(t *testing.T) {
		dir := t.TempDir()
		// a decoy from an unrelated earlier action: the entry must not
		// consume or touch it
		decoy := filepath.Join(dir, "bootstrap-admin.key")
		if err := os.WriteFile(decoy, []byte("decoy-not-a-real-key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		keyOut := filepath.Join(t.TempDir(), "init.key")
		if code, out, errb := initRun(t, dir, keyOut); code != 0 {
			t.Fatalf("init: %d:\n%s%s", code, out, errb)
		}
		raw, err := os.ReadFile(decoy)
		if err != nil || string(raw) != "decoy-not-a-real-key\n" {
			t.Fatalf("bootstrap-admin.key must stay untouched (got %q, %v)", raw, err)
		}
		key, err := os.ReadFile(keyOut)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(key), "decoy") {
			t.Fatal("the issued key must be fresh, never read from bootstrap-admin.key")
		}
	})

	t.Run("refuses beside a live server's lock", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(filepath.Join(dir, "service.lock"), os.O_CREATE|os.O_RDWR, 0o640)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		keyOut := filepath.Join(t.TempDir(), "init.key")
		code, _, errb := initRun(t, dir, keyOut)
		if code == 0 {
			t.Fatal("the entry must refuse while the server holds the single-active lock")
		}
		if !strings.Contains(errb, "service.lock") {
			t.Fatalf("the refusal must name the lock: %q", errb)
		}
	})

	t.Run("no --key-out refuses before anything is written", func(t *testing.T) {
		dir := t.TempDir()
		var out, errb bytes.Buffer
		code := initMain([]string{"--data-dir", dir}, nil, &out, &errb)
		if code == 0 {
			t.Fatal("without --key-out the plaintext has nowhere to go — the entry must refuse")
		}
		if !strings.Contains(errb.String(), "--key-out") {
			t.Fatalf("the refusal must name --key-out: %q", errb.String())
		}
		if _, err := os.Stat(filepath.Join(dir, "aihub.db")); !os.IsNotExist(err) {
			t.Fatalf("the refusal must happen before the store is opened: %v", err)
		}
	})
}
