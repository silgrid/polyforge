package testenv

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The real-gitconfig guard compares content fingerprints: creating,
// editing and deleting a guarded file must each change it.
func TestFingerprintDetectsEveryChange(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, ".gitconfig"), filepath.Join(dir, "git", "config")}
	absent := fingerprint(paths)
	if !bytes.Equal(absent, fingerprint(paths)) {
		t.Fatal("fingerprint is not stable")
	}
	if err := os.WriteFile(paths[0], []byte("[user]\n\tname = a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created := fingerprint(paths)
	if bytes.Equal(absent, created) {
		t.Fatal("creating a guarded file went unnoticed")
	}
	if err := os.WriteFile(paths[0], []byte("[user]\n\tname = b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(created, fingerprint(paths)) {
		t.Fatal("editing a guarded file went unnoticed")
	}
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(absent, fingerprint(paths)) {
		t.Fatal("restoring the absent state must restore the fingerprint")
	}
}

// Inside a Main-isolated binary the global git config is the scratch one
// with the test identity, never the operator's.
func TestMainIsolatesGitConfig(t *testing.T) {
	gc := os.Getenv("GIT_CONFIG_GLOBAL")
	if gc == "" || filepath.Dir(gc) != os.Getenv("HOME") {
		t.Fatalf("GIT_CONFIG_GLOBAL=%q is not the scratch home's (HOME=%q)", gc, os.Getenv("HOME"))
	}
	raw, err := os.ReadFile(gc)
	if err != nil || !bytes.Contains(raw, []byte(TestGitEmail)) {
		t.Fatalf("scratch config lacks the test identity: %q, %v", raw, err)
	}
	if os.Getenv("GIT_CONFIG_NOSYSTEM") != "1" {
		t.Fatal("the system git config must be off")
	}
}

// Inside a Main-isolated binary TMPDIR is the run's private dir (short,
// under /tmp), so every temp entry a test makes is checked at exit.
func TestMainIsolatesTmpdir(t *testing.T) {
	tmp := os.TempDir()
	if filepath.Dir(tmp) != "/tmp" || len(filepath.Base(tmp)) != 6 || !strings.HasPrefix(filepath.Base(tmp), "pf") {
		t.Fatalf("TMPDIR=%q is not the run's private /tmp/pfXXXX dir", tmp)
	}
}

// leftovers names every entry, sorted, and caps a long list with a count.
func TestLeftoversListsEntries(t *testing.T) {
	dir := t.TempDir()
	if got, err := leftovers(dir, 2); got != nil || err != nil {
		t.Fatalf("empty dir: %v %v", got, err)
	}
	for _, n := range []string{"c", "a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := leftovers(dir, 5); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("leftovers = %q, want a,b,c", got)
	}
	if got, _ := leftovers(dir, 2); strings.Join(got, ",") != "a,b,... and 1 more" {
		t.Fatalf("capped leftovers = %q", got)
	}
	if _, err := leftovers(filepath.Join(dir, "gone"), 2); err == nil {
		t.Fatal("a missing dir must be an error, not a clean pass")
	}
}

func TestMain(m *testing.M) { Main(m) }
