package main

// main_test.go — seedtool's observable behaviour: what the image build
// can see. The expected manifest content comes from the settled layout
// contract (the components package the tool fronts), never from the
// tool's own output: write must produce a manifest the install
// accepts, refuse a tree that lacks one of the four component
// categories, and verify must catch a tampered tree.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/components"
)

// assembleSeed writes a minimal but shape-complete seed version
// directory: the four component categories of the settled layout. The
// harness package files carry their own versions, the way the image
// build's npm install leaves them.
func assembleSeed(t *testing.T, dir string) {
	t.Helper()
	wfile(t, filepath.Join(dir, "bin/pf"), "pf-binary\n", 0o755)
	wjson(t, filepath.Join(dir, "package.json"), map[string]any{"name": "polyforge", "version": "v0.2.4"})
	_ = os.MkdirAll(filepath.Join(dir, "skills/polyforge"), 0o755)
	wfile(t, filepath.Join(dir, "skills/polyforge/SKILL.md"), "# the routing skill\n", 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "workflows/develop"), 0o755)
	wfile(t, filepath.Join(dir, "workflows/develop/README.md"), "# the develop lane\n", 0o644)
	wjson(t, filepath.Join(dir, "pi-web/package.json"), map[string]any{"name": "pi-web", "version": "0.2.4"})
	wfile(t, filepath.Join(dir, "pi-web/app/page.tsx"), "export default function Page() {}\n", 0o644)
	wjson(t, filepath.Join(dir, "harness/pi/node_modules/@earendil-works/pi-coding-agent/package.json"), map[string]any{
		"name": "@earendil-works/pi-coding-agent", "version": "1.4.0",
	})
	wjson(t, filepath.Join(dir, "harness/claude/node_modules/@anthropic-ai/claude-code/package.json"), map[string]any{
		"name": "@anthropic-ai/claude-code", "version": "2.1.326",
	})
}

func wfile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func wjson(t *testing.T, path string, v any) {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	wfile(t, path, string(body)+"\n", 0o644)
}

// writeArgs answers the write command's argv for dir.
func writeArgs(dir string) []string {
	return []string{"write",
		"--root", dir,
		"--version", "v0.2.4",
		"--commit", "0123abcd4567",
		"--platform", "linux/amd64",
		"--channel-repo", "silgrid/polyforge",
		"--harness-pi-package", filepath.Join(dir, "harness/pi/node_modules/@earendil-works/pi-coding-agent"),
		"--harness-claude-package", filepath.Join(dir, "harness/claude/node_modules/@anthropic-ai/claude-code"),
	}
}

// TestSeedToolWriteAndVerify pins the tool's happy path: write lands a
// manifest the components package (the install transaction) accepts —
// the four component categories with the harness versions read from
// the packages themselves — and verify passes the intact seed.
func TestSeedToolWriteAndVerify(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "v0.2.4")
	assembleSeed(t, dir)

	if err := run(writeArgs(dir)); err != nil {
		t.Fatalf("seedtool write: %v", err)
	}
	m, err := components.ReadManifest(dir)
	if err != nil {
		t.Fatalf("the written manifest is not accepted by the install's own reader: %v", err)
	}
	if m.Commit != "0123abcd4567" || m.Platform != "linux/amd64" || m.ChannelRepo != "silgrid/polyforge" {
		t.Errorf("the manifest carries %q/%q/%q, want the flags' values", m.Commit, m.Platform, m.ChannelRepo)
	}
	if m.MinBase != "v0.2.4" {
		t.Errorf("MinBase = %q, want the write's own version (the default: the base and the seed of one image are one build)", m.MinBase)
	}
	if c := m.Component(components.ComponentHarnessPi); c == nil || c.Version != "1.4.0" {
		t.Errorf("harness-pi entry: got %+v, want the package's own version 1.4.0", m.Component(components.ComponentHarnessPi))
	}
	if c := m.Component(components.ComponentHarnessClaude); c == nil || c.Version != "2.1.326" {
		t.Errorf("harness-claude entry: got %+v, want the package's own version 2.1.326", m.Component(components.ComponentHarnessClaude))
	}
	if err := m.VerifyTree(dir); err != nil {
		t.Errorf("the written manifest does not verify against its tree: %v", err)
	}

	if err := run([]string{"verify", "--root", dir}); err != nil {
		t.Errorf("seedtool verify on the intact seed: %v", err)
	}
}

// TestSeedToolWriteRefusesIncomplete pins that a tree missing one of
// the four component categories never gets a manifest, and that the
// tool's required inputs are enforced.
func TestSeedToolWriteRefusesIncomplete(t *testing.T) {
	fresh := func(t *testing.T) string {
		dir := filepath.Join(t.TempDir(), "v0.2.4")
		assembleSeed(t, dir)
		return dir
	}
	cases := []struct {
		name string
		dir  func(t *testing.T) string
		args func(dir string, argv []string) []string
	}{
		{
			name: "no pf binary",
			dir: func(t *testing.T) string {
				dir := fresh(t)
				if err := os.Remove(filepath.Join(dir, "bin/pf")); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			args: func(dir string, argv []string) []string { return argv },
		},
		{
			name: "no release checkout",
			dir: func(t *testing.T) string {
				dir := fresh(t)
				if err := os.RemoveAll(filepath.Join(dir, "workflows")); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			args: func(dir string, argv []string) []string { return argv },
		},
		{
			name: "no pi-web",
			dir: func(t *testing.T) string {
				dir := fresh(t)
				if err := os.RemoveAll(filepath.Join(dir, "pi-web")); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			args: func(dir string, argv []string) []string { return argv },
		},
		{
			name: "no harness",
			dir: func(t *testing.T) string {
				dir := fresh(t)
				if err := os.RemoveAll(filepath.Join(dir, "harness")); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			args: func(dir string, argv []string) []string { return argv },
		},
		{
			name: "the harness package flags are required",
			dir:  fresh,
			args: func(dir string, argv []string) []string {
				out := make([]string, 0, len(argv))
				for i := 0; i < len(argv); i += 2 {
					if argv[i] == "--harness-pi-package" || argv[i] == "--harness-claude-package" {
						continue
					}
					out = append(out, argv[i], argv[i+1])
				}
				return out
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := c.dir(t)
			argv := c.args(dir, writeArgs(dir))
			if err := run(argv); err == nil {
				t.Fatalf("seedtool write accepted an incomplete seed (%s)", c.name)
			}
			if _, err := os.Lstat(filepath.Join(dir, "manifest.json")); err == nil {
				t.Errorf("an incomplete seed (%s) still got a manifest", c.name)
			}
		})
	}

	// a bad flag set names the problem, and an unknown command refuses.
	if err := run([]string{"write", "--root"}); err == nil {
		t.Error("write accepted a dangling flag value")
	}
	if err := run([]string{"explode"}); err == nil {
		t.Error("an unknown command was accepted")
	}
	if err := run(nil); err == nil {
		t.Error("no command was accepted")
	}
}

// TestSeedToolVerifyCatchesTamper pins the reader side: a tree whose
// content no longer matches its manifest fails verify.
func TestSeedToolVerifyCatchesTamper(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "v0.2.4")
	assembleSeed(t, dir)
	if err := run(writeArgs(dir)); err != nil {
		t.Fatalf("seedtool write: %v", err)
	}
	wfile(t, filepath.Join(dir, "pi-web/app/page.tsx"), "export default function Page() { /* tampered */ }\n", 0o644)

	err := run([]string{"verify", "--root", dir})
	if err == nil {
		t.Fatal("seedtool verify passed a tampered seed")
	}
	if !strings.Contains(err.Error(), components.ComponentPiWeb) {
		t.Errorf("the verify error should name the mismatching component:\n%s", err)
	}

	// and a directory without a manifest refuses, naming the file.
	if err := run([]string{"verify", "--root", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "manifest.json") {
		t.Errorf("verify of a manifest-less directory = %v, want an error naming manifest.json", err)
	}
}
