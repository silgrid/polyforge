package components

// manifest_test.go — the manifest contract, authored test-first
// against the ticket's settled layout (analysis 1.2: the manifest
// names the product version, the source commit, the platform, the
// channel repository, the minimum base requirement and one entry per
// component with its own version and digest; the harness entries carry
// their package versions, never the product version). Expected values
// come from that contract, never from the implementation's output.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManifestRoundTrip pins what a reader can read out of a written
// manifest — AC2's face: product version, commit, platform, channel
// and the harness versions, one entry per component of the four
// categories.
func TestManifestRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "v0.2.4")
	writeVersionDir(t, dir, seedSpec{})

	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", m.SchemaVersion, SchemaVersion)
	}
	if m.Version != "v0.2.4" {
		t.Errorf("Version = %q, want v0.2.4", m.Version)
	}
	if m.Commit != "0123abcd4567" {
		t.Errorf("Commit = %q, want the spec's commit", m.Commit)
	}
	if m.Platform != "linux/amd64" {
		t.Errorf("Platform = %q, want linux/amd64", m.Platform)
	}
	if m.ChannelRepo != "silgrid/polyforge" {
		t.Errorf("ChannelRepo = %q, want silgrid/polyforge", m.ChannelRepo)
	}
	if m.MinBase != "v0.2.4" {
		t.Errorf("MinBase = %q, want v0.2.4", m.MinBase)
	}
	if len(m.Components) != len(ComponentNames) {
		t.Fatalf("Components: %d entries, want %d", len(m.Components), len(ComponentNames))
	}
	for i, name := range ComponentNames {
		c := m.Components[i]
		if c.Name != name {
			t.Errorf("Components[%d].Name = %q, want %q", i, c.Name, name)
			continue
		}
		path, ok := PathOfComponent(name)
		if !ok || c.Path != path {
			t.Errorf("component %q: Path = %q, want %q", name, c.Path, path)
		}
		if !IsDigest(c.Digest) {
			t.Errorf("component %q: Digest = %q, not a digest", name, c.Digest)
		}
	}
	// the product version rides the pf and release entries; the harness
	// entries carry their own package versions (a harness pinned
	// anywhere in its own line must never read as the product version).
	if c := m.Component(ComponentPF); c == nil || c.Version != "v0.2.4" {
		t.Errorf("pf entry version: got %+v, want v0.2.4", m.Component(ComponentPF))
	}
	if c := m.Component(ComponentPiWeb); c == nil || c.Version != "0.2.4" {
		t.Errorf("pi-web entry version: got %+v, want 0.2.4 (the package's own)", m.Component(ComponentPiWeb))
	}
	if c := m.Component(ComponentHarnessPi); c == nil || c.Version != "1.4.0" {
		t.Errorf("harness-pi entry version: got %+v, want 1.4.0 (the package's own)", m.Component(ComponentHarnessPi))
	}
	if c := m.Component(ComponentHarnessClaude); c == nil || c.Version != "2.1.326" {
		t.Errorf("harness-claude entry version: got %+v, want 2.1.326 (the package's own)", m.Component(ComponentHarnessClaude))
	}
	if err := m.VerifyTree(dir); err != nil {
		t.Errorf("VerifyTree on the intact tree: %v", err)
	}
}

// TestManifestValidate pins every refusal: a manifest of the wrong
// shape never becomes a seed or an installed version.
func TestManifestValidate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "v0.2.4")
	good := writeVersionDir(t, dir, seedSpec{})

	bad := map[string]func(m *Manifest){
		"schema version":      func(m *Manifest) { m.SchemaVersion = SchemaVersion + 1 },
		"version of no form":  func(m *Manifest) { m.Version = "0.2.4" },
		"empty commit":        func(m *Manifest) { m.Commit = "" },
		"platform without os": func(m *Manifest) { m.Platform = "amd64" },
		"repo without owner":  func(m *Manifest) { m.ChannelRepo = "polyforge" },
		"minBase of no form":  func(m *Manifest) { m.MinBase = "dev" },
		"a missing component": func(m *Manifest) { m.Components = m.Components[:len(m.Components)-1] },
		"an extra component": func(m *Manifest) {
			m.Components = append(m.Components, Component{Name: "other", Path: "x", Version: "v0.2.4", Digest: good.Component(ComponentPF).Digest})
		},
		"a misplaced component":         func(m *Manifest) { m.Components[0].Path = "pf" },
		"pf not at the product version": func(m *Manifest) { m.Components[0].Version = "v9.9.9" },
		"a non-hex digest":              func(m *Manifest) { m.Components[2].Digest = strings.Repeat("z", 64) },
		"a short digest":                func(m *Manifest) { m.Components[2].Digest = strings.Repeat("0", 63) },
		"an empty component version":    func(m *Manifest) { m.Components[2].Version = "" },
	}
	for name, breakIt := range bad {
		m := manifestOf(seedSpec{})
		if err := m.FillDigests(dir); err != nil {
			t.Fatalf("FillDigests: %v", err)
		}
		breakIt(m)
		if err := m.Validate(); err == nil {
			t.Errorf("Validate accepted a manifest with %s", name)
		}
	}

	// the intact manifest passes, and a directory without one refuses.
	if err := good.Validate(); err != nil {
		t.Errorf("Validate refused the intact manifest: %v", err)
	}
	empty := t.TempDir()
	if _, err := ReadManifest(empty); err == nil || !strings.Contains(err.Error(), ManifestFilename) {
		t.Errorf("ReadManifest of a directory without a manifest = (%v), want an error naming %s", err, ManifestFilename)
	}
}

// TestVerifyTreeCatchesTamper pins the reader side: any byte, mode or
// symlink change inside a component breaks the digest, and the release
// entry covers everything the other entries do not own.
func TestVerifyTreeCatchesTamper(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "v0.2.4")
	writeVersionDir(t, dir, seedSpec{})

	tamper := map[string]func(t *testing.T){
		"a changed pf byte": func(t *testing.T) {
			wfile(t, filepath.Join(dir, PathPF), "pf-binary-tampered\n", 0o755)
		},
		"a lost exec bit": func(t *testing.T) {
			if err := os.Chmod(filepath.Join(dir, PathPF), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a changed skill byte": func(t *testing.T) {
			wfile(t, filepath.Join(dir, "skills/polyforge/SKILL.md"), "# the routing skill, tampered\n", 0o644)
		},
		"an extra release file": func(t *testing.T) {
			wfile(t, filepath.Join(dir, "extra.sh"), "echo hi\n", 0o755)
		},
		"a retargeted symlink": func(t *testing.T) {
			_ = os.Remove(filepath.Join(dir, PathPiWeb, "bin/next"))
			wlink(t, filepath.Join(dir, PathPiWeb, "bin/next"), "../app")
		},
		"a changed pi-web byte": func(t *testing.T) {
			wfile(t, filepath.Join(dir, PathPiWeb, "app/page.tsx"), "export default function Page() { /* x */ }\n", 0o644)
		},
		"a changed harness version": func(t *testing.T) {
			wjson(t, filepath.Join(dir, PathHarnessPi, "node_modules/@earendil-works/pi-coding-agent/package.json"), map[string]any{
				"name": "@earendil-works/pi-coding-agent", "version": "1.4.1",
			})
		},
	}
	for name, do := range tamper {
		t.Run(name, func(t *testing.T) {
			// a fresh copy per case: tamper one, verify all.
			fresh := filepath.Join(t.TempDir(), "v0.2.4")
			writeVersionDir(t, fresh, seedSpec{})
			do(t)
			m, err := ReadManifest(fresh)
			if err != nil {
				t.Fatalf("ReadManifest: %v", err)
			}
			if err := m.VerifyTree(fresh); err == nil {
				t.Fatalf("VerifyTree passed a tampered tree (%s)", name)
			}
			_ = dir
		})
	}
}
