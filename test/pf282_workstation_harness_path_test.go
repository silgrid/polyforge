// carries: dist/workstation/Containerfile
package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run the image recipe's actual link commands in a temporary filesystem,
// then resolve commands through PATH across install, upgrade and rollback.
// The small executable fixtures exercise the links, not the harness packages.
func TestWorkstationHarnessPATHFollowsCurrent(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "usr/local/bin")
	components := filepath.Join(root, "components")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"old", "new"} {
		for _, name := range []string{"pi", "claude"} {
			modules := filepath.Join(components, "releases", version, "harness", name, "node_modules")
			if err := os.MkdirAll(filepath.Join(modules, ".bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(modules, "entry"), []byte("#!/bin/sh\nprintf '%s\\n' '"+name+"-"+version+"'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../entry", filepath.Join(modules, ".bin", name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	body := w535Deliverable(t, "dist/workstation/Containerfile")
	// Join shell continuations, retaining the RUN instruction's shell logic.
	body = strings.ReplaceAll(body, "\\\n", " ")
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "RUN ln -s ") {
			continue
		}
		command := strings.TrimPrefix(line, "RUN ")
		command = strings.ReplaceAll(command, "/components/", components+"/")
		command = strings.ReplaceAll(command, "/usr/local/bin/", bin+"/")
		if output, err := exec.Command("/bin/sh", "-ec", command).CombinedOutput(); err != nil {
			t.Fatalf("image link instruction: %v\n%s", err, output)
		}
	}
	current := filepath.Join(components, "current")
	for _, version := range []string{"old", "new", "old"} {
		// Atomic replacement matches the components transaction's commit point.
		if err := os.Symlink(filepath.Join("releases", version), current+".next"); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(current+".next", current); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"pi", "claude"} {
			t.Run(version+"/"+name, func(t *testing.T) {
				cmd := exec.Command("/bin/sh", "-ec", name)
				// No host harness can mask a missing image entry.
				cmd.Env = []string{"PATH=" + bin}
				output, err := cmd.CombinedOutput()
				if err != nil || strings.TrimSpace(string(output)) != name+"-"+version {
					t.Fatalf("PATH command must follow current: %v, output=%q, want=%s-%s", err, output, name, version)
				}
			})
		}
	}
}
