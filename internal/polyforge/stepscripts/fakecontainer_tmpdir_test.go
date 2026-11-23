package stepscripts

// fakecontainer_tmpdir_test.go: a fake "container" runs its commands on
// this host, so the container paths the step runner hands it are real
// host paths (polyforge#263). pfstep.tmpdir names a command's private
// TMPDIR /tmp/pfv-<n> inside the container; under a verification
// container that is the full-1 command's own TMPDIR, Go's $WORK
// (/tmp/pfv-1/go-build<n>) included — so a fake container that wrote
// there or whose reap removed its container path deleted the outer run's
// build directory, and the packages still building failed with
// "fork/exec …: no such file or directory". The fake maps a container
// /tmp path under its own root, one namespace per container id: no real
// path is ever written to or removed.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDockerReapScript answers the production container runtime's
// REAP_SCRIPT (pfstep.container): what every container command runs after
// it, killing what it left and removing its TMPDIR. Reading it from the
// production module keeps this test on the script a real run uses.
func fakeDockerReapScript(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	cmd := exec.Command(py, "-B", "-c",
		"import sys; sys.path.insert(0, sys.argv[1]); from pfstep import container; sys.stdout.write(container.REAP_SCRIPT)",
		scriptsDir(t))
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("reading the production REAP_SCRIPT: %v\n%s", err, errb.String())
	}
	if out.Len() == 0 {
		t.Fatal("the production REAP_SCRIPT is empty")
	}
	return out.String()
}

// runFakeDocker runs the fake docker client in dir with args, answering
// its stdout and its exit code.
func runFakeDocker(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(dir, "docker"), args...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &ee):
		return out.String(), ee.ExitCode()
	}
	t.Fatalf("fake docker %v: %v\n%s", args, err, errb.String())
	return "", 0
}

// outerRunTmpdir is a path of the shape the step runner names inside a
// container (pfstep.tmpdir's /tmp/pfv-<n>) that this process can use as
// the outer run's TMPDIR without colliding with a concurrent test
// package's.
func outerRunTmpdir() string {
	return filepath.Join("/tmp", fmt.Sprintf("pfv-%d", os.Getpid()))
}

// TestFakeContainerReapLeavesTheRealTmpAlone: the reap of a fake
// container removes only that fake container's copy of its TMPDIR. The
// container path is also a real host path — the outer run's TMPDIR, with
// a file in it (Go's $WORK holds the build's output) — and it must
// survive untouched.
func TestFakeContainerReapLeavesTheRealTmpAlone(t *testing.T) {
	dir := (&fakeRuntime{}).dockerDir(t)
	real := outerRunTmpdir()
	canary := filepath.Join(real, "go-build1", "b001", "_pkg_.a")
	if err := os.MkdirAll(filepath.Dir(canary), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(real) })
	const content = "the outer run's build output\n"
	if err := os.WriteFile(canary, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, code := runFakeDocker(t, dir, "exec", "cid-1", "sh", "-c", fakeDockerReapScript(t), "sh", real); code != 0 {
		t.Fatalf("the fake container's reap exited %d", code)
	}
	raw, err := os.ReadFile(canary)
	if err != nil {
		t.Fatalf("the fake container's reap removed the real TMPDIR %s the outer run is using: %v", real, err)
	}
	if string(raw) != content {
		t.Fatalf("the canary under %s reads %q, want %q", real, raw, content)
	}
	// and the fake container's own copy of that path is gone: the reap did
	// run, in the fake's namespace
	if _, err := os.Stat(filepath.Join(dir, "tmp", "cid-1", filepath.Base(real))); !os.IsNotExist(err) {
		t.Fatalf("the fake container kept its own copy of %s: %v", real, err)
	}
}

// TestFakeContainerCommandTmpdirIsItsOwn: the TMPDIR a container command
// is given is the fake container's own directory, not the real path the
// runner names — a command that makes or writes its TMPDIR (the runner's
// `mkdir -p "$TMPDIR"` wrapper) must not touch the outer run's.
func TestFakeContainerCommandTmpdirIsItsOwn(t *testing.T) {
	dir := (&fakeRuntime{}).dockerDir(t)
	real := outerRunTmpdir()
	t.Cleanup(func() { _ = os.RemoveAll(real) })

	out, code := runFakeDocker(t, dir, "exec", "--workdir", dir, "--env", "TMPDIR="+real, "cid-1", "sh", "-c",
		`mkdir -p "$TMPDIR" && printf ok > "$TMPDIR/probe" && printf %s "$TMPDIR"`)
	if code != 0 {
		t.Fatalf("the container command exited %d: %s", code, out)
	}
	want := filepath.Join(dir, "tmp", "cid-1", filepath.Base(real))
	if got := strings.TrimSpace(out); got != want {
		t.Fatalf("the container command's TMPDIR is %q, want the fake container's own %q", got, want)
	}
	if raw, err := os.ReadFile(filepath.Join(want, "probe")); err != nil || string(raw) != "ok" {
		t.Fatalf("the command's output is not in the fake container's TMPDIR %s: %q, %v", want, raw, err)
	}
	if _, err := os.Stat(real); !os.IsNotExist(err) {
		t.Fatalf("the container command wrote the real TMPDIR %s the outer run is using: %v", real, err)
	}
}
