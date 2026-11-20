package components

// fetch.go (polyforge#190, 84-inplace-upgrade): the channel's release
// source — the one network half of the in-container upgrade. The
// channel repository is the one the current version's manifest names
// (the image build wrote it there, polyforge#162's Q5 ruling), and the
// release is the channel repository's own GitHub Release: the same
// five payload assets tools/install.sh consumes, read anonymously over
// HTTPS — no gh, no login (the internal -gmi line's private repository
// simply answers 404 and the refusal lands in the status file).
//
// What is downloaded is verified before anything is staged: every
// payload's sha256 against its SHA256SUMS line, and the pf binary
// proves --version — a release published with the right checksums but
// the wrong version inside the binary never reaches the volume.

import (
	"context"
	"errors"
)

// ErrReleaseAssetMismatch: a downloaded payload's sha256 does not match
// its SHA256SUMS line (or the line is missing) — the release's own
// checksum file refuses it.
var ErrReleaseAssetMismatch = errors.New("a release payload does not match its SHA256SUMS line")

// The URL seams: the API and download faces of the channel
// repository's GitHub. Package vars so the tests point them at a
// stub server; production never changes them.
var (
	githubAPIBase      = "https://api.github.com"
	githubDownloadBase = "https://github.com"
)

// ReleaseInfo is one release of the channel repository as the upgrade
// reads it: the tag and the source commit its manifest names.
type ReleaseInfo struct {
	Tag    string
	Commit string
}

// LatestRelease answers the channel repository's latest formal
// release (GitHub's /releases/latest: no pre-releases, no drafts).
func LatestRelease(ctx context.Context, repo string) (ReleaseInfo, error) {
	_ = ctx
	_ = repo
	return ReleaseInfo{}, errors.New("LatestRelease: not implemented")
}

// FetchRelease downloads the release tag's upgrade payloads into dir
// (which must exist): SHA256SUMS, release-manifest.json, the platform's
// pf binary (pf-linux-<goarch>) and the Pi Web package
// (silgrid-pi-web-<tag>.tgz). Each downloaded payload is verified
// against its SHA256SUMS line and the pf binary must answer --version
// with the tag. The answer names the tag's source commit (from
// release-manifest.json, cross-checked against the requested tag).
func FetchRelease(ctx context.Context, dir, repo, tag, goarch string) (ReleaseInfo, error) {
	_ = ctx
	_ = dir
	_ = repo
	_ = tag
	_ = goarch
	return ReleaseInfo{}, errors.New("FetchRelease: not implemented")
}

// FetchReleaseCheckout extracts the release checkout of the tag into
// dir (which must exist): the codeload tarball of the channel
// repository at the tag, reduced to the entries a version directory's
// release component owns (package.json, skills/, workflows/) — the
// same set the image's seed stage copies.
func FetchReleaseCheckout(ctx context.Context, dir, repo, tag string) error {
	_ = ctx
	_ = dir
	_ = repo
	_ = tag
	return errors.New("FetchReleaseCheckout: not implemented")
}
