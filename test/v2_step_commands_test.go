package test

// v2_step_commands_test.go: the retired step commands as a machine may
// still have them — symlinks to pf an earlier tools/install.sh laid beside
// it (pf-verify, pf-run, pf-sync, pf-merge, pf-revert, pf-facts,
// pf-integrate and pf-gate, now the workflow's scripts). The pf binary
// built from this tree has no argv[0] dispatch: a link of any name is pf
// itself. The installer links none and removes the retired links that
// point at pf, leaving any other file of those names alone, on a hermetic
// machine (the w174 harness), idempotently.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestV2StepCommands' -count=1

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// v2PfBinDir builds cmd/pf into a fresh bin dir and links the retired step
// commands (and a stranger) beside it, as an earlier tools/install.sh laid
// them out.
func v2PfBinDir(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "pf"), "./cmd/pf")
	build.Dir = ".."
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/pf: %v\n%s", err, out)
	}
	// pf-verify, pf-run, pf-facts, pf-integrate and pf-gate: links a
	// machine installed before, #474, #475 and #477 made them
	// workflow scripts — no step commands any more, so pf itself
	for _, name := range []string{"pf-run", "pf-verify", "pf-facts", "pf-integrate", "pf-gate", "pf-stranger"} {
		if err := os.Symlink("pf", filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// v2RunLink runs the named link in dir with args and answers its stdout,
// stderr and exit code.
func v2RunLink(t *testing.T, bin, name, dir string, env []string, args ...string) (string, string, int) {
	t.Helper()
	c := exec.Command(filepath.Join(bin, name), args...)
	c.Dir = dir
	c.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}, env...)
	var out, errb strings.Builder
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out.String(), errb.String(), code
}

func TestV2StepCommandsDispatchByArgv0(t *testing.T) {
	bin := v2PfBinDir(t)
	capsule := t.TempDir()
	pins := filepath.Join(capsule, "pins.json")
	if err := os.MkdirAll(filepath.Join(capsule, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pins, []byte(`{"heads": {}, "gated": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// a stranger's link, and every retired command's (pf-verify, pf-run,
	// pf-facts, pf-integrate and pf-gate left for the workflow scripts
	// verify.py, run.py, facts.py, integrate.py and gate.py), is pf itself
	// (no argv[0] dispatch): the usage, exit 2, no result file
	for _, name := range []string{"pf-stranger", "pf-verify", "pf-run", "pf-facts", "pf-integrate", "pf-gate"} {
		out := filepath.Join("artifacts", name, "result.json")
		if _, errb, code := v2RunLink(t, bin, name, capsule, []string{"PF_ROUND=round-e2e"}, "--capsule", ".", "--pins", pins, "--out", out); code != 2 || !strings.Contains(errb, "pf") || strings.Contains(errb, "step command") {
			t.Fatalf("%s: exit %d, stderr %q", name, code, errb)
		}
		if _, err := os.Stat(filepath.Join(capsule, out)); !os.IsNotExist(err) {
			t.Fatalf("%s wrote a result file: %v", name, err)
		}
	}
	// and pf itself is untouched
	if out, _, code := v2RunLink(t, bin, "pf", capsule, nil, "--version"); code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("pf --version: exit %d, %q", code, out)
	}
}

func TestV2StepCommandsInstallLinks(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-v2sc")
	binDir := filepath.Dir(in.bin)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// an earlier install's links: relative `pf-<name> -> pf`, and one
	// absolute to the binary's path; beside them the user's own files of
	// two retired names (a regular file, a link to another program) and a
	// stranger's link
	retired := []string{"pf-verify", "pf-run", "pf-sync", "pf-facts", "pf-integrate"}
	for _, name := range retired {
		if err := os.Symlink("pf", filepath.Join(binDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(in.bin, filepath.Join(binDir, "pf-revert")); err != nil {
		t.Fatal(err)
	}
	retired = append(retired, "pf-revert")
	if err := os.WriteFile(filepath.Join(binDir, "pf-merge"), []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/true", filepath.Join(binDir, "pf-gate")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/true", filepath.Join(binDir, "pf-stranger")); err != nil {
		t.Fatal(err)
	}
	out, code := in.run(t)
	if code != 0 {
		t.Fatalf("install.sh: exit %d\n%s", code, out)
	}
	// every step command is a workflow script now: the installer links
	// none, and removes each retired link that points at pf
	for _, name := range retired {
		if _, err := os.Lstat(filepath.Join(binDir, name)); !os.IsNotExist(err) {
			t.Fatalf("the retired link %s is still there after the install: %v", name, err)
		}
		if !strings.Contains(out, "   removed the retired link "+name+" -> ") {
			t.Fatalf("the installer did not report removing %s:\n%s", name, out)
		}
	}
	// the user's own files are left alone
	if raw, err := os.ReadFile(filepath.Join(binDir, "pf-merge")); err != nil || string(raw) != "#!/bin/sh\necho mine\n" {
		t.Fatalf("the user's own pf-merge was touched: %v %q", err, raw)
	}
	for _, name := range []string{"pf-gate", "pf-stranger"} {
		if target, err := os.Readlink(filepath.Join(binDir, name)); err != nil || target != "/usr/bin/true" {
			t.Fatalf("%s, a link to another program, was touched: %v %q", name, err, target)
		}
		if strings.Contains(out, "removed the retired link "+name+" ") {
			t.Fatalf("the installer reported removing %s:\n%s", name, out)
		}
	}
	// a second run changes nothing
	if out, code = in.run(t); code != 0 || strings.Contains(out, "removed the retired link") {
		t.Fatalf("second install.sh: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(binDir, "pf-merge")); err != nil {
		t.Fatalf("the second run touched the user's pf-merge: %v", err)
	}
}
