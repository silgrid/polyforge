package components

// layout.go — the components root's on-disk names (polyforge#162). The
// layout is the ticket's settled one: every versioned component lives
// under <root>/releases/<ver>/ — the release checkout root (package.json,
// skills/, workflows/) plus bin/pf, pi-web/ and harness/{pi,claude}/ and
// manifest.json — and the root's current and previous symlinks are the
// only pointers anyone consumes. staging/, install.lock and the
// transaction journal are the install transaction's own internals; no
// other writer may create them.

import (
	"fmt"
	"os"
	"path/filepath"
)

// The root's directory and file names.
const (
	// ReleasesDirname holds one complete version directory per version.
	ReleasesDirname = "releases"
	// CurrentLinkname is the one commit point: the symlink every
	// consumer resolves.
	CurrentLinkname = "current"
	// PreviousLinkname names the version current replaced (absent until
	// the first upgrade).
	PreviousLinkname = "previous"
	// StagingDirname holds in-progress copies; an entry there is never
	// published and never consumed.
	StagingDirname = "staging"
	// LockFilename serializes installs (flock, exclusive,
	// non-blocking): the first-boot entry and #92's upgrade verb hold
	// the same lock.
	LockFilename = "install.lock"
	// JournalFilename records the switch in progress ({newVersion,
	// oldVersion}); its recovery rule is recover.go's.
	JournalFilename = "install-journal.json"
	// ManifestFilename is the version directory's manifest.
	ManifestFilename = "manifest.json"
	// LegacyReleaseDirname is the #161-era shape: a release/ directory
	// holding <ver> directories and a current symlink inside the
	// components root. It carries no manifests and no digests, so it is
	// refused with a migration note, never imported.
	LegacyReleaseDirname = "release"
)

// The component set of one version directory — the four categories the
// seed builds (pf, the release checkout, Pi Web, the two harnesses) —
// and the path each occupies inside it.
const (
	ComponentPF            = "pf"
	ComponentRelease       = "release"
	ComponentPiWeb         = "pi-web"
	ComponentHarnessPi     = "harness-pi"
	ComponentHarnessClaude = "harness-claude"

	PathPF            = "bin/pf"
	PathRelease       = "."
	PathPiWeb         = "pi-web"
	PathHarnessPi     = "harness/pi"
	PathHarnessClaude = "harness/claude"
)

// ComponentNames lists the component names in their canonical order.
var ComponentNames = []string{
	ComponentPF,
	ComponentRelease,
	ComponentPiWeb,
	ComponentHarnessPi,
	ComponentHarnessClaude,
}

// pathOfComponent is the settled path table. The release entry owns
// the version directory's root minus the entries the other components
// own and the manifest.
var pathOfComponent = map[string]string{
	ComponentPF:            PathPF,
	ComponentRelease:       PathRelease,
	ComponentPiWeb:         PathPiWeb,
	ComponentHarnessPi:     PathHarnessPi,
	ComponentHarnessClaude: PathHarnessClaude,
}

// topName answers the top-level entry a component path occupies (the
// release digest's skip set is built of these).
func topName(path string) string {
	if i := len(path); i > 0 {
		for j := 0; j < len(path); j++ {
			if path[j] == '/' {
				return path[:j]
			}
		}
	}
	return path
}

// PathOfComponent answers the version-directory path a component name
// occupies, and whether the name is one of the set.
func PathOfComponent(name string) (string, bool) {
	path, ok := pathOfComponent[name]
	return path, ok
}

// CurrentTarget answers the version directory current points at (the
// symlink target, a path relative to the root: releases/<ver>), or ""
// with no error when no current exists. A current that exists but is
// not a symlink is an error — the commit point is a symlink by
// definition.
func CurrentTarget(root string) (string, error) {
	link := filepath.Join(root, CurrentLinkname)
	fi, err := os.Lstat(link)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return "", fmt.Errorf("%s exists but is not a symlink", link)
	}
	target, err := os.Readlink(link)
	if err != nil {
		return "", err
	}
	return target, nil
}
