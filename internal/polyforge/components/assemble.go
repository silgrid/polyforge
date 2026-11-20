package components

// assemble.go (polyforge#190, 84-inplace-upgrade): the seed assembly —
// the fetched release assets become one complete version directory,
// the shape internal/polyforge/components' install transaction
// consumes. The version directory needs all five component categories
// (the manifest's closed set, polyforge#162), so the assembly carries
// what this release does not ship: the two harness trees and Pi Web's
// production node_modules come from the current version directory —
// a harness upgrades only with its own release (#86 pins them into
// the manifest first) and node-pty's native build cannot be redone
// inside a container without a compiler, so the carried copy is
// reconciled by npm install --omit=dev: an unchanged pin is a no-op,
// a changed one that cannot build fails the upgrade honestly.
//
// The manifest is written by the same rules the image's seedtool
// applies (the digests by FillDigests against the assembled tree, the
// component versions from the trees themselves), so the installed
// versions and the image's seed are judged by one contract.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AssembleOptions is one assembly's inputs.
type AssembleOptions struct {
	// Root is the components volume's root: current's manifest names
	// the channel and the minimum base, and current's harness trees
	// and Pi Web node_modules are what the assembly carries.
	Root string
	// Dest is the version directory to assemble (it must not exist;
	// a failed assembly removes what it wrote of it).
	Dest string
	// Version is the release's product version (the tag).
	Version string
	// Commit is the release's source commit (release-manifest.json).
	Commit string
	// Platform is the running platform (os/arch) — the manifest's
	// own field, from current's manifest or the runtime.
	Platform string
	// AssetsDir is FetchRelease's directory.
	AssetsDir string
	// CheckoutDir is FetchReleaseCheckout's directory.
	CheckoutDir string
	// NPMInstall reconciles the assembled Pi Web tree's production
	// dependencies (the carried node_modules against its new
	// package.json); nil runs the real `npm install --omit=dev`.
	NPMInstall func(dir string) error
}

// npmInstallTimeout bounds one npm install of the assembled Pi Web
// tree (a cold install of a changed dependency set is minutes, not
// more; a hung one refuses the upgrade).
const npmInstallTimeout = 10 * time.Minute

// currentManifestOf answers the manifest of the version current points
// at (the channel, the base floor, the harness versions and the trees
// the assembly carries).
func currentManifestOf(root string) (*Manifest, string, error) {
	target, err := CurrentTarget(root)
	if err != nil {
		return nil, "", err
	}
	if target == "" {
		return nil, "", fmt.Errorf("the components root %s carries no current: the in-container upgrade upgrades an installed workstation (the first install is the image's own wiring)", root)
	}
	dir := filepath.Join(root, target)
	m, err := ReadManifest(dir)
	if err != nil {
		return nil, "", err
	}
	return m, dir, nil
}

// copyCarried copies one carried tree (src under current into dest),
// answering the copy's error. An absent source is not carried — the
// release itself owns that entry from here on.
func copyCarried(src, dest string) error {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return copyTree(src, dest)
}

// unpackPiWeb extracts the packed Pi Web package (the npm pack shape:
// every entry under package/) into dest/pi-web.
func unpackPiWeb(tgz, dest string) error {
	body, err := os.ReadFile(tgz)
	if err != nil {
		return err
	}
	return untar(bytes.NewReader(body), func(path string, mode int64, body []byte) error {
		name := strings.TrimPrefix(strings.TrimPrefix(path, "./"), "package/")
		if name == "" || name == path {
			// the archive's root or an entry outside package/
			return nil
		}
		return writeTarEntry(dest, name, mode, body)
	})
}

// readPackageVersion answers the version field of the package.json in
// dir — the installed package's own version, never assumed. The same
// rule the image's seedtool applies; one implementation, shared.
func readPackageVersion(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "", err
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return "", err
	}
	if pkg.Version == "" {
		return "", fmt.Errorf("%s: the package carries no version", filepath.Join(dir, "package.json"))
	}
	return pkg.Version, nil
}

// defaultNPMInstall reconciles the assembled Pi Web tree's production
// dependencies with npm.
func defaultNPMInstall(dir string) error {
	ctx, cancel := timeoutContext(npmInstallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npm", "install", "--omit=dev", "--no-audit", "--no-fund")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("npm install --omit=dev in %s: %v\n%s", dir, err, bytes.TrimSpace(out))
	}
	return nil
}

// AssembleVersionDir assembles the release into a complete version
// directory at opts.Dest — bin/pf, the release checkout, Pi Web with
// its carried dependencies, the two harness trees carried from
// current, and the manifest — and answers the manifest. A failure at
// any step removes Dest and answers the error; nothing outside Dest
// is touched.
func AssembleVersionDir(opts AssembleOptions) (*Manifest, error) {
	m, err := assembleVersionDir(opts)
	if err != nil {
		// a failed assembly leaves no half-assembled version directory
		// behind: a later run of the same version must not meet its own
		// remains.
		_ = os.RemoveAll(opts.Dest)
		return nil, err
	}
	return m, nil
}

func assembleVersionDir(opts AssembleOptions) (*Manifest, error) {
	if opts.Dest == "" {
		return nil, fmt.Errorf("the assembly: Dest is required")
	}
	if _, err := os.Lstat(opts.Dest); err == nil {
		return nil, fmt.Errorf("the assembly's destination %s already exists", opts.Dest)
	}
	cur, curDir, err := currentManifestOf(opts.Root)
	if err != nil {
		return nil, err
	}

	// bin/pf: the verified payload, executable.
	pf, err := os.ReadFile(filepath.Join(opts.AssetsDir, "pf-linux-"+platformArch(opts.Platform)))
	if err != nil {
		return nil, fmt.Errorf("the release payload: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(opts.Dest, filepath.Dir(PathPF)), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(opts.Dest, PathPF), pf, 0o755); err != nil {
		return nil, err
	}

	// the release checkout at the version directory's root.
	for _, path := range []string{"package.json"} {
		body, err := os.ReadFile(filepath.Join(opts.CheckoutDir, path))
		if err != nil {
			return nil, fmt.Errorf("the release checkout of %s: %w", opts.Version, err)
		}
		if err := os.MkdirAll(opts.Dest, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(opts.Dest, path), body, 0o644); err != nil {
			return nil, err
		}
	}
	for _, tree := range []string{"skills", "workflows"} {
		if err := copyCarried(filepath.Join(opts.CheckoutDir, tree), filepath.Join(opts.Dest, tree)); err != nil {
			return nil, fmt.Errorf("the release checkout's %s: %w", tree, err)
		}
	}

	// Pi Web: the packed package, with current's node_modules carried
	// and reconciled.
	piWeb := filepath.Join(opts.Dest, PathPiWeb)
	if err := unpackPiWeb(filepath.Join(opts.AssetsDir, piWebAssetName(opts.Version)), piWeb); err != nil {
		return nil, fmt.Errorf("the Pi Web package of %s: %w", opts.Version, err)
	}
	if err := copyCarried(filepath.Join(curDir, PathPiWeb, "node_modules"), filepath.Join(piWeb, "node_modules")); err != nil {
		return nil, fmt.Errorf("carrying pi-web's production dependencies: %w", err)
	}
	npm := opts.NPMInstall
	if npm == nil {
		npm = defaultNPMInstall
	}
	if err := npm(piWeb); err != nil {
		return nil, err
	}

	// the two harness trees carried from current, versions and all.
	for _, name := range []string{ComponentHarnessPi, ComponentHarnessClaude} {
		path, _ := PathOfComponent(name)
		if err := copyCarried(filepath.Join(curDir, path), filepath.Join(opts.Dest, path)); err != nil {
			return nil, fmt.Errorf("carrying the %s tree: %w", name, err)
		}
	}

	// the manifest: the same rules the image's seed applies.
	piWebVersion, err := readPackageVersion(piWeb)
	if err != nil {
		return nil, fmt.Errorf("pi-web's package: %w", err)
	}
	m := &Manifest{
		SchemaVersion: SchemaVersion,
		Version:       opts.Version,
		Commit:        opts.Commit,
		Platform:      opts.Platform,
		ChannelRepo:   cur.ChannelRepo,
		MinBase:       cur.MinBase,
	}
	for _, name := range ComponentNames {
		path, _ := PathOfComponent(name)
		c := Component{Name: name, Path: path}
		switch name {
		case ComponentPF, ComponentRelease:
			c.Version = opts.Version
		case ComponentPiWeb:
			c.Version = piWebVersion
		case ComponentHarnessPi, ComponentHarnessClaude:
			if e := cur.Component(name); e != nil {
				c.Version = e.Version
			}
		}
		m.Components = append(m.Components, c)
	}
	if err := m.FillDigests(opts.Dest); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := WriteManifest(opts.Dest, m); err != nil {
		return nil, err
	}
	return m, nil
}

// platformArch answers the arch half of an os/arch platform.
func platformArch(platform string) string {
	if i := strings.IndexByte(platform, '/'); i >= 0 {
		return platform[i+1:]
	}
	return platform
}

// timeoutContext answers a context the same-shape helpers use: the
// install's own bound; a stuck npm answer refuses the upgrade rather
// than hanging the orchestration.
func timeoutContext(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
