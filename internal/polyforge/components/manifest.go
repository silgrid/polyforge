package components

// manifest.go — the version directory's manifest: the contract the
// image's seed and the volume's installed versions share. It names the
// product version, the source commit, the platform, the channel
// repository and the minimum base requirement, and one entry per
// component with that component's own version and digest — the harness
// entries carry their npm package versions, never the product version
// (a harness may be pinned anywhere inside its own line).

import "errors"

// SchemaVersion is the manifest shape this package reads and writes.
const SchemaVersion = 1

// Manifest is one version directory's manifest.
type Manifest struct {
	SchemaVersion int         `json:"schemaVersion"`
	Version       string      `json:"version"`
	Commit        string      `json:"commit"`
	Platform      string      `json:"platform"`
	ChannelRepo   string      `json:"channelRepo"`
	MinBase       string      `json:"minBase"`
	Components    []Component `json:"components"`
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
func ReadManifest(dir string) (*Manifest, error) { return nil, errors.New("unimplemented") }

// Validate checks the manifest against the contract: the schema
// version, the accepted version forms (its own version and the minimum
// base), the non-empty provenance fields with their shapes (commit,
// platform os/arch, channel repo owner/name), the exact component set
// with each component at its settled path, the product version on the
// pf and release entries, and a well-formed digest on every entry.
func (m *Manifest) Validate() error { return errors.New("unimplemented") }

// Component answers the named component's entry, or nil.
func (m *Manifest) Component(name string) *Component { return nil }

// FillDigests computes every component's digest against the assembled
// tree at root and fills the entries' Digest fields. It is the writer
// side (the build's seedtool); VerifyTree is the reader side.
func (m *Manifest) FillDigests(root string) error { return errors.New("unimplemented") }

// VerifyTree walks the version directory root and compares every
// component's digest with the tree it finds. Any mismatch — a changed
// byte, a lost exec bit, a retargeted symlink, an extra file inside
// the release component — is an error naming the component and the
// path, so a caller can tell a corrupt copy from a wrong seed.
func (m *Manifest) VerifyTree(root string) error { return errors.New("unimplemented") }

// WriteManifest writes the manifest into the version directory dir as
// manifest.json, crash-atomically.
func WriteManifest(dir string, m *Manifest) error { return errors.New("unimplemented") }
