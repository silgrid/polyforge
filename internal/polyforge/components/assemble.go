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
	"errors"
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

// AssembleVersionDir assembles the release into a complete version
// directory at opts.Dest — bin/pf, the release checkout, Pi Web with
// its carried dependencies, the two harness trees carried from
// current, and the manifest — and answers the manifest. A failure at
// any step removes Dest and answers the error; nothing outside Dest
// is touched.
func AssembleVersionDir(opts AssembleOptions) (*Manifest, error) {
	_ = opts
	return nil, errors.New("AssembleVersionDir: not implemented")
}
