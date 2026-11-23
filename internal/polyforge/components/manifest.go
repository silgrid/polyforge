package components

// manifest.go — the version directory's manifest: the contract the
// image's seed and the volume's installed versions share. It names the
// product version, the source commit, the platform, the channel
// repository and the minimum base requirement, and one entry per
// component with that component's own version and digest — the harness
// entries carry their npm package versions, never the product version
// (a harness may be pinned anywhere inside its own line).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
)

// SchemaVersion is the manifest shape this package reads and writes.
const SchemaVersion = 1

// platformRe is the os/arch shape (linux/amd64, darwin/arm64).
var platformRe = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)

// channelRepoRe is the owner/name shape a channel repository takes.
var channelRepoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9_.-]+$`)

// Manifest is one version directory's manifest.
type Manifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	Platform      string `json:"platform"`
	ChannelRepo   string `json:"channelRepo"`
	MinBase       string `json:"minBase"`
	// DataLayout is the data-layer layout this build READS (the
	// cellcfg layout version, stamped by the seed's own build — the
	// owner's ruling of 2026-11-21): the rollback compares it with
	// the data dir's live layout-version and refuses the versions
	// that crossed a data migration. Empty = a version older than
	// the field: never machine-judged, the documented operator
	// responsibility.
	DataLayout string      `json:"dataLayout,omitempty"`
	Components []Component `json:"components"`
}

// Component is one manifest entry: the name from the closed set, the
// version-directory path it occupies, its own version and its digest.
type Component struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// ReadManifest reads and validates the manifest of the version
// directory dir. A missing manifest, unparsable JSON or any invalid
// field is an error naming the file.
func ReadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, ManifestFilename)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// Validate checks the manifest against the contract: the schema
// version, the accepted version forms (its own version and the minimum
// base), the non-empty provenance fields with their shapes (commit,
// platform os/arch, channel repo owner/name), the exact component set
// with each component at its settled path, the product version on the
// pf and release entries, and a well-formed digest on every entry.
func (m *Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion %d: this layout reads schema version %d", m.SchemaVersion, SchemaVersion)
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return fmt.Errorf("version: %w", err)
	}
	if m.Commit == "" {
		return fmt.Errorf("commit: the source commit is required (a seed without provenance is unverifiable)")
	}
	if !platformRe.MatchString(m.Platform) {
		return fmt.Errorf("platform %q: want the os/arch shape (linux/amd64)", m.Platform)
	}
	if !channelRepoRe.MatchString(m.ChannelRepo) {
		return fmt.Errorf("channelRepo %q: want the owner/name shape of the channel repository", m.ChannelRepo)
	}
	if _, err := ParseVersion(m.MinBase); err != nil {
		return fmt.Errorf("minBase: %w", err)
	}
	if len(m.Components) != len(pathOfComponent) {
		return fmt.Errorf("components: %d entries, want exactly the %d of the set (%s)",
			len(m.Components), len(pathOfComponent), strings.Join(ComponentNames, ", "))
	}
	seen := make(map[string]bool, len(m.Components))
	for i, c := range m.Components {
		path, ok := pathOfComponent[c.Name]
		if !ok {
			return fmt.Errorf("components[%d]: %q is not a component of this layout", i, c.Name)
		}
		if seen[c.Name] {
			return fmt.Errorf("components[%d]: %q appears twice", i, c.Name)
		}
		seen[c.Name] = true
		if c.Path != path {
			return fmt.Errorf("component %q: path %q, want the settled %q", c.Name, c.Path, path)
		}
		if c.Version == "" {
			return fmt.Errorf("component %q: version is required", c.Name)
		}
		if c.Name == ComponentPF || c.Name == ComponentRelease {
			if c.Version != m.Version {
				return fmt.Errorf("component %q: version %q, want the product version %q it was built from", c.Name, c.Version, m.Version)
			}
		}
		if !IsDigest(c.Digest) {
			return fmt.Errorf("component %q: digest %q is not a sha256 digest", c.Name, c.Digest)
		}
	}
	return nil
}

// Component answers the named component's entry, or nil.
func (m *Manifest) Component(name string) *Component {
	for i := range m.Components {
		if m.Components[i].Name == name {
			return &m.Components[i]
		}
	}
	return nil
}

// releaseSkips answers the top-level entries the release component's
// digest excludes: the manifest and every other component's top-level
// directory (bin, pi-web, harness).
func releaseSkips() []string {
	skips := []string{ManifestFilename}
	for _, name := range ComponentNames {
		if name == ComponentRelease {
			continue
		}
		path, _ := PathOfComponent(name)
		skips = append(skips, topName(path))
	}
	return skips
}

// digestOf answers the digest of one component against the version
// directory root. The release entry digests the root minus the
// entries the other components own and the manifest; every other
// entry digests its own path.
func digestOf(root string, c *Component) (string, error) {
	if c.Name == ComponentRelease {
		return TreeDigest(root, releaseSkips())
	}
	return TreeDigest(filepath.Join(root, c.Path), nil)
}

// FillDigests computes every component's digest against the assembled
// tree at root and fills the entries' Digest fields. It is the writer
// side (the build's seedtool); VerifyTree is the reader side.
func (m *Manifest) FillDigests(root string) error {
	for i := range m.Components {
		d, err := digestOf(root, &m.Components[i])
		if err != nil {
			return fmt.Errorf("component %s: %w", m.Components[i].Name, err)
		}
		m.Components[i].Digest = d
	}
	return nil
}

// VerifyTree walks the version directory root and compares every
// component's digest with the tree it finds. Any mismatch — a changed
// byte, a lost exec bit, a retargeted symlink, an extra file inside
// the release component — is an error naming the component and the
// path, so a caller can tell a corrupt copy from a wrong seed.
func (m *Manifest) VerifyTree(root string) error {
	for i := range m.Components {
		c := &m.Components[i]
		d, err := digestOf(root, c)
		if err != nil {
			return fmt.Errorf("component %s (%s): %w", c.Name, c.Path, err)
		}
		if d != c.Digest {
			return fmt.Errorf("component %s (%s): the tree's digest is %s, the manifest names %s — the content does not match the manifest",
				c.Name, c.Path, d, c.Digest)
		}
	}
	return nil
}

// WriteManifest writes the manifest into the version directory dir as
// manifest.json, crash-atomically.
func WriteManifest(dir string, m *Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return atomicfile.Write(filepath.Join(dir, ManifestFilename), body, 0o644)
}
