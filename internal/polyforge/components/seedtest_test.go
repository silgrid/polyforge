package components

// seedtest_test.go — the synthetic version trees the package's tests
// assemble. A real seed is a heavy build (pi-web's npm ci, two harness
// package installs); the transaction's semantics — digests, staging,
// switching, recovery — are independent of those bytes, so the tests
// build small trees with the settled five-component shape and derive
// every expectation from the manifest contract, never from a real
// build's output.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// seedSpec names the metadata of one synthetic version directory. The
// zero fields carry the defaults a workstation seed carries; a test
// overrides one field to build the case it pins.
type seedSpec struct {
	version       string
	commit        string
	platform      string
	channelRepo   string
	minBase       string
	piWebVersion  string
	piVersion     string
	claudeVersion string
	pfBody        string
}

func (s seedSpec) with(over seedSpec) seedSpec {
	if over.version != "" {
		s.version = over.version
	}
	if over.commit != "" {
		s.commit = over.commit
	}
	if over.platform != "" {
		s.platform = over.platform
	}
	if over.channelRepo != "" {
		s.channelRepo = over.channelRepo
	}
	if over.minBase != "" {
		s.minBase = over.minBase
	}
	if over.piWebVersion != "" {
		s.piWebVersion = over.piWebVersion
	}
	if over.piVersion != "" {
		s.piVersion = over.piVersion
	}
	if over.claudeVersion != "" {
		s.claudeVersion = over.claudeVersion
	}
	if over.pfBody != "" {
		s.pfBody = over.pfBody
	}
	return s
}

func (s seedSpec) filled() seedSpec {
	if s.version == "" {
		s.version = "v0.2.4"
	}
	if s.commit == "" {
		s.commit = "0123abcd4567"
	}
	if s.platform == "" {
		s.platform = "linux/amd64"
	}
	if s.channelRepo == "" {
		s.channelRepo = "silgrid/polyforge"
	}
	if s.minBase == "" {
		s.minBase = s.version
	}
	if s.piWebVersion == "" {
		s.piWebVersion = "0.2.4"
	}
	if s.piVersion == "" {
		s.piVersion = "1.4.0"
	}
	if s.claudeVersion == "" {
		s.claudeVersion = "2.1.326"
	}
	if s.pfBody == "" {
		s.pfBody = "pf-binary-v0.2.4\n"
	}
	return s
}

// wfile writes one regular file (parent directories created), failing
// the test on any error.
func wfile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// wjson writes one small JSON file from v.
func wjson(t *testing.T, path string, v any) {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	wfile(t, path, string(body)+"\n", 0o644)
}

// wlink writes one symlink (parent directories created).
func wlink(t *testing.T, path, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink %s -> %s: %v", path, target, err)
	}
}

// assembleTree writes the five-component tree of one version directory
// (everything but the manifest): the pf binary, the release checkout
// (package.json, skills, workflows), pi-web with one symlink beside
// its package.json, and the two harness packages.
func assembleTree(t *testing.T, dir string, spec seedSpec) {
	t.Helper()
	s := spec.filled()
	wfile(t, filepath.Join(dir, PathPF), s.pfBody, 0o755)
	wjson(t, filepath.Join(dir, "package.json"), map[string]any{
		"name":    "polyforge",
		"version": s.version,
	})
	wfile(t, filepath.Join(dir, "skills/polyforge/SKILL.md"), "# the routing skill\n", 0o644)
	wfile(t, filepath.Join(dir, "workflows/develop/README.md"), "# the develop lane\n", 0o644)
	wjson(t, filepath.Join(dir, PathPiWeb, "package.json"), map[string]any{
		"name":    "pi-web",
		"version": s.piWebVersion,
	})
	wfile(t, filepath.Join(dir, PathPiWeb, "app/page.tsx"), "export default function Page() {}\n", 0o644)
	wlink(t, filepath.Join(dir, PathPiWeb, "bin/next"), "../package.json")
	wjson(t, filepath.Join(dir, PathHarnessPi, "package.json"), map[string]any{
		"name": "polyforge-harness-pi", "private": true,
	})
	wjson(t, filepath.Join(dir, PathHarnessPi, "node_modules/@earendil-works/pi-coding-agent/package.json"), map[string]any{
		"name": "@earendil-works/pi-coding-agent", "version": s.piVersion,
	})
	wjson(t, filepath.Join(dir, PathHarnessClaude, "package.json"), map[string]any{
		"name": "polyforge-harness-claude", "private": true,
	})
	wjson(t, filepath.Join(dir, PathHarnessClaude, "node_modules/@anthropic-ai/claude-code/package.json"), map[string]any{
		"name": "@anthropic-ai/claude-code", "version": s.claudeVersion,
	})
}

// manifestOf builds the manifest metadata of one spec (no digests).
func manifestOf(spec seedSpec) *Manifest {
	s := spec.filled()
	return &Manifest{
		SchemaVersion: SchemaVersion,
		Version:       s.version,
		Commit:        s.commit,
		Platform:      s.platform,
		ChannelRepo:   s.channelRepo,
		MinBase:       s.minBase,
		Components: []Component{
			{Name: ComponentPF, Path: PathPF, Version: s.version},
			{Name: ComponentRelease, Path: PathRelease, Version: s.version},
			{Name: ComponentPiWeb, Path: PathPiWeb, Version: s.piWebVersion},
			{Name: ComponentHarnessPi, Path: PathHarnessPi, Version: s.piVersion},
			{Name: ComponentHarnessClaude, Path: PathHarnessClaude, Version: s.claudeVersion},
		},
	}
}

// writeVersionDir assembles the tree and lands its manifest: the
// version directory a seed or an installed release is, complete.
func writeVersionDir(t *testing.T, dir string, spec seedSpec) *Manifest {
	t.Helper()
	assembleTree(t, dir, spec)
	m := manifestOf(spec)
	if err := m.FillDigests(dir); err != nil {
		t.Fatalf("FillDigests: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("the assembled manifest must validate: %v", err)
	}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	return m
}
