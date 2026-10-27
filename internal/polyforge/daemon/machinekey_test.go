package daemon

// machinekey_test.go: the data-dir-scoped Ed25519 identity. The key
// survives a restart, two daemons present distinct keys, the first write
// is a 0600 file, and a corrupt file refuses (never silently regenerates).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMachineKeySurvivesRestartAndIsStable(t *testing.T) {
	dir := t.TempDir()
	key1, pub1, err := loadOrCreateMachineKey(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if pub1 == "" || len(key1) == 0 {
		t.Fatalf("empty key: %q", pub1)
	}
	path := filepath.Join(dir, machineKeyFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("machine key mode = %o, want 0600", info.Mode().Perm())
	}
	// A restart reads the same identity.
	key2, pub2, err := loadOrCreateMachineKey(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if pub2 != pub1 || string(key2) != string(key1) {
		t.Fatalf("the key changed across restart: %q -> %q", pub1, pub2)
	}
}

func TestMachineKeysAreDistinctAcrossDaemons(t *testing.T) {
	_, a, err := loadOrCreateMachineKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := loadOrCreateMachineKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two daemons derived the same public key %q", a)
	}
}

func TestMachineKeyCorruptRefuses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, machineKeyFile), []byte("not-a-seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := loadOrCreateMachineKey(dir)
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("a corrupt key must refuse with a repair hint, got %v", err)
	}
}

// TestMachineKeyInterruptedFirstCreateIsRecovered: the pre-atomic path
// could leave an empty machine-key when a crash landed between the create
// and the write. An empty file never held an identity, so it is created
// once more instead of refusing the daemon start forever.
func TestMachineKeyInterruptedFirstCreateIsRecovered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, machineKeyFile)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, pub, err := loadOrCreateMachineKey(dir)
	if err != nil {
		t.Fatalf("an interrupted first create must recover, got %v", err)
	}
	if pub == "" {
		t.Fatal("recovery produced no public key")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSpace(string(raw))
	if len(text) != 64 {
		t.Fatalf("recovered machine key = %q, want a 64-hex seed", text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("recovered machine key mode = %o, want 0600", info.Mode().Perm())
	}
}

// TestMachineKeyStrayTempIsIgnored: a crash before the link leaves a temp
// file beside the key; it must neither block startup nor be mistaken for
// the key.
func TestMachineKeyStrayTempIsIgnored(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "."+machineKeyFile+".tmp-crashed")
	if err := os.WriteFile(stray, []byte("half a seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, pub, err := loadOrCreateMachineKey(dir)
	if err != nil {
		t.Fatalf("a stray temp file blocked startup: %v", err)
	}
	_, again, err := loadOrCreateMachineKey(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again != pub {
		t.Fatalf("the key changed across restart: %q -> %q", pub, again)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "."+machineKeyFile+".tmp-") && e.Name() != filepath.Base(stray) {
			t.Fatalf("a successful create left temp residue %q", e.Name())
		}
	}
}
