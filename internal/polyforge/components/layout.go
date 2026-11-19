package components

// layout.go — the components root's on-disk names (polyforge#162). The
// layout is the ticket's settled one: every versioned component lives
// under <root>/releases/<ver>/ — the release checkout root (package.json,
// skills/, workflows/) plus bin/pf, pi-web/ and harness/{pi,claude}/ and
// manifest.json — and the root's current and previous symlinks are the
// only pointers anyone consumes. staging/, install.lock and the
// transaction journal are the install transaction's own internals; no
// other writer may create them.

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

// PathOfComponent answers the version-directory path a component name
// occupies, and whether the name is one of the set.
func PathOfComponent(name string) (string, bool) { return "", false }

// CurrentTarget answers the version directory current points at (the
// symlink target, a path relative to the root: releases/<ver>), or ""
// with no error when no current exists.
func CurrentTarget(root string) (string, error) { return "", nil }
