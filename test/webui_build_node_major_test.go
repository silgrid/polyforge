package test

// The webui-build node gate: the repo-root .nvmrc pins the node MAJOR
// version, and `make webui-build` accepts any node of that major while
// rejecting every other major loudly — before npm runs, naming the
// required major and .nvmrc. These tests drive the real Makefile target
// with stub node/npm binaries, so no assertion depends on the host's node.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// webuiBuildRun runs `make -f <root>/Makefile webui-build` in a temp dir
// holding a copy of the repo's own .nvmrc and an empty webui/. The PATH is
// a stub dir only: `cat`/`cut` (the Makefile's own helpers) link to the
// host tools, `npm` logs its arguments, and `node` — present only when
// nodeVersion is non-empty — prints nodeVersion. It returns make's
// combined output, the npm log and make's error.
func webuiBuildRun(t *testing.T, nodeVersion string) (string, string, error) {
	t.Helper()
	root := v1Root(t)
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Fatalf("make not found: %v", err)
	}
	nvmrc, err := os.ReadFile(filepath.Join(root, ".nvmrc"))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, ".nvmrc"), nvmrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(work, "webui"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, tool := range []string{"cat", "cut"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("%s not found: %v", tool, err)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	stubs := map[string]string{
		"npm": "#!/bin/sh\necho \"$*\" >> \"$STUB_LOG\"\n",
	}
	if nodeVersion != "" {
		stubs["node"] = "#!/bin/sh\necho \"$STUB_NODE_VERSION\"\n"
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	npmLog := filepath.Join(work, "npm.log")
	cmd := exec.Command(makeBin, "-f", filepath.Join(root, "Makefile"), "webui-build")
	cmd.Dir = work
	cmd.Env = []string{
		"PATH=" + bin,
		"HOME=" + work,
		"STUB_NODE_VERSION=" + nodeVersion,
		"STUB_LOG=" + npmLog,
	}
	out, runErr := cmd.CombinedOutput()
	logged, err := os.ReadFile(npmLog)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(out), string(logged), runErr
}

func TestWebuiBuildAcceptsAnyNodeOfTheNvmrcMajor(t *testing.T) {
	for _, v := range []string{"v24.22.2", "v24.0.0"} {
		t.Run(v, func(t *testing.T) {
			out, npmLog, err := webuiBuildRun(t, v)
			if err != nil {
				t.Fatalf("node %s rejected: %v\n%s", v, err, out)
			}
			if npmLog != "ci\nrun build\n" {
				t.Fatalf("node %s: npm calls = %q, want ci then run build\n%s", v, npmLog, out)
			}
		})
	}
}

func TestWebuiBuildRejectsOtherNodeMajors(t *testing.T) {
	for _, v := range []string{"v22.20.0", "v25.1.0", ""} {
		name := v
		if name == "" {
			name = "no-node"
		}
		t.Run(name, func(t *testing.T) {
			out, npmLog, err := webuiBuildRun(t, v)
			if err == nil {
				t.Fatalf("node %q accepted, want a loud failure\n%s", v, out)
			}
			for _, want := range []string{"24", ".nvmrc"} {
				if !strings.Contains(out, want) {
					t.Errorf("node %q: message lacks %q:\n%s", v, want, out)
				}
			}
			if npmLog != "" {
				t.Errorf("node %q: npm ran (%q), want the check to stop before the build", v, npmLog)
			}
		})
	}
}
