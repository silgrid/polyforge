package components

// install.go — the local install transaction every component switch
// goes through (polyforge#162): the workstation's first boot and #92's
// in-container upgrade verb both call Install and hold the same lock,
// so there is one version comparator, one staging discipline and one
// chain-switching rule, never two.
//
// The transaction's shape is the ticket's settled one: stage the seed
// under staging/, verify the staged copy against the manifest's
// digests, publish it by one rename into releases/<ver>, then swap the
// root's current symlink — the single commit point. previous follows
// current; a journal records the switch in progress and recover.go
// completes or discards it, so an interruption at any point leaves
// current on a complete version and previous explainable.

import "errors"

// The sentinel errors a caller matches on. Every one of them leaves
// current, previous and the published versions exactly as they were.
var (
	// ErrLockHeld: another process holds the install lock.
	ErrLockHeld = errors.New("the components install lock is held by another process")

	// ErrLegacyLayout: the components root carries the #161-era shape
	// (a release/ directory with a current symlink). No auto-import,
	// no bridge: the migration is by hand or a fresh volume.
	ErrLegacyLayout = errors.New("the components root carries the pre-#162 release layout")

	// ErrDowngrade: the seed is older than current.
	ErrDowngrade = errors.New("refusing the downgrade: the seed is older than the current version")

	// ErrChannelMismatch: the seed and current are of different
	// channels (kind or repository) — an implicit cross-channel switch.
	ErrChannelMismatch = errors.New("refusing the cross-channel switch")

	// ErrSameVersionDifferentDigest: the seed and current carry the
	// same version with different content.
	ErrSameVersionDifferentDigest = errors.New("the same version carries a different digest")

	// ErrDigestMismatch: the seed's (or the staged copy's) content does
	// not match its manifest's digests.
	ErrDigestMismatch = errors.New("the content does not match the manifest's digests")

	// ErrCurrentCorrupt: current points at a version directory whose
	// manifest cannot be read — the install refuses to guess.
	ErrCurrentCorrupt = errors.New("the current version is corrupt")

	// ErrPlatformMismatch: the seed was built for another platform.
	ErrPlatformMismatch = errors.New("the seed was built for another platform")

	// ErrBaseTooOld: the running base does not meet the seed's minimum
	// base requirement.
	ErrBaseTooOld = errors.New("the running base is older than the seed's minimum base requirement")
)

// Options is one install's inputs.
type Options struct {
	// Root is the components volume's root (the /components volume in
	// the workstation): releases/, current, previous and the
	// transaction internals live directly in it.
	Root string
	// Seed is the seed's version directory (the image's
	// /opt/polyforge/seed/releases/<ver>, or any directory of the same
	// shape).
	Seed string
	// BaseVersion is the running base's (bootstrap pf's) version, for
	// the manifest's minimum base requirement.
	BaseVersion string
	// Platform is the running platform (os/arch, e.g. linux/amd64).
	Platform string
}

// Result is what one install did.
type Result struct {
	// Action: "installed" (no current before), "upgraded" (current
	// moved) or "noop" (the same version and digest were already
	// current — nothing was written).
	Action string
	// Version is the version that is current after the install.
	Version string
	// From is the version that was current before ("" on a first
	// install).
	From string
}

// The action names a Result carries.
const (
	ActionInstalled = "installed"
	ActionUpgraded  = "upgraded"
	ActionNoop      = "noop"
)

// Install installs the seed into the components root and switches
// current to it under the install lock. A refusal — a downgrade, a
// cross-channel seed, the same version with a different digest, a
// corrupt seed or current, another platform, an old base, a legacy
// layout or a held lock — answers a sentinel error and writes nothing.
func Install(opts Options) (*Result, error) { return nil, errors.New("unimplemented") }
