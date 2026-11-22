package testenv

import (
	"bytes"
	"os"
	"os/exec"
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

// The exit guard's null-device check: a regular file standing where the
// null device should be, or nothing at all, is an error; the real null
// device is not. It only reads (stat), never touches the real device.
func TestNullDeviceGuard(t *testing.T) {
	standIn := filepath.Join(t.TempDir(), "null")
	if err := os.WriteFile(standIn, []byte("bash: line 1: x: command not found\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := nullDeviceErr(standIn); err == nil {
		t.Error("a regular file holding text passed as the null device")
	}
	if err := nullDeviceErr(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing path passed as the null device")
	}
	if err := nullDeviceErr(os.DevNull); err != nil {
		t.Errorf("the real null device %s was refused: %v", os.DevNull, err)
	}
}

// capsuleChild marks the re-exec'd helper of TestMainIsolatesPFCapsule: it
// runs in a fresh process whose Main has isolated the environment.
const capsuleChild = "TESTENV_CAPSULE_CHILD"

// A step process always carries PF_CAPSULE, its capsule root, and the
// verify-scope engine reads it to resolve the default diff base from the
// capsule's runtime-state/target-branch.json (polyforge#216): a test that
// inherits it judges against a branch its synthetic repository never built
// and falls back to full. Main must keep it away from the tests and the
// processes they start. The parent binary is already isolated and passes
// TESTENV_ISOLATED on, so isolation is proven on a fresh process: one
// started with PF_CAPSULE set and without the isolated-env marker.
func TestMainIsolatesPFCapsule(t *testing.T) {
	if os.Getenv(capsuleChild) == "1" {
		if got, ok := os.LookupEnv("PF_CAPSULE"); ok {
			t.Fatalf("PF_CAPSULE=%q survived Main's isolation; a verify-scope run would take the step's capsule target branch as the test's diff base", got)
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestMainIsolatesPFCapsule$")
	cmd.Env = append(envWithout(os.Environ(), activeEnv), capsuleChild+"=1", "PF_CAPSULE="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a test binary started with PF_CAPSULE set: %v\n%s", err, out)
	}
}

// envWithout answers env minus every entry setting one of names.
func envWithout(env []string, names ...string) []string {
	var keep []string
	for _, kv := range env {
		drop := false
		for _, n := range names {
			if strings.HasPrefix(kv, n+"=") {
				drop = true
			}
		}
		if !drop {
			keep = append(keep, kv)
		}
	}
	return keep
}

func TestMain(m *testing.M) { Main(m) }
