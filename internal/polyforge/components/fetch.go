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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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

// fetchTimeout bounds one HTTP request of the release source.
const fetchTimeout = 5 * time.Minute

// httpGet fetches one URL into memory (bounded by the release assets'
// own sizes; the pf binary and the pi-web package are the large half
// of a release).
func httpGet(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// LatestRelease answers the channel repository's latest formal
// release (GitHub's /releases/latest: no pre-releases, no drafts).
func LatestRelease(ctx context.Context, repo string) (ReleaseInfo, error) {
	body, err := httpGet(ctx, githubAPIBase+"/repos/"+repo+"/releases/latest")
	if err != nil {
		return ReleaseInfo{}, fmt.Errorf("the channel %s names no latest release: %w", repo, err)
	}
	var latest struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &latest); err != nil {
		return ReleaseInfo{}, fmt.Errorf("the channel %s answered an unreadable latest release: %w", repo, err)
	}
	if latest.TagName == "" {
		return ReleaseInfo{}, fmt.Errorf("the channel %s names no latest release", repo)
	}
	return ReleaseInfo{Tag: latest.TagName}, nil
}

// sumsOf parses SHA256SUMS (one "<sha256>  <name>" line per payload)
// and answers the named payload's digest.
func sumsOf(body []byte, name string) (string, error) {
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		file := strings.TrimPrefix(fields[1], "*")
		if file == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%w: SHA256SUMS carries no line for %s", ErrReleaseAssetMismatch, name)
}

// fetchVerified downloads one payload of the release and verifies its
// bytes against its SHA256SUMS line before answering them.
func fetchVerified(ctx context.Context, url string, sums, name string) ([]byte, error) {
	body, err := httpGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	want, err := sumsOf([]byte(sums), name)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%w: %s's digest is %s, SHA256SUMS names %s",
			ErrReleaseAssetMismatch, name, got, want)
	}
	return body, nil
}

// releaseManifest is release-manifest.json's shape as
// .polyforge/release.sh's manifest_of writes it: the tag, the source
// commit and the payload list.
type releaseManifest struct {
	Tag    string `json:"tag"`
	Commit string `json:"commit"`
}

// FetchRelease downloads the release tag's upgrade payloads into dir
// (which must exist): SHA256SUMS, release-manifest.json, the platform's
// pf binary (pf-linux-<goarch>) and the Pi Web package
// (silgrid-pi-web-<tag>.tgz). Each downloaded payload is verified
// against its SHA256SUMS line and the pf binary must answer --version
// with the tag. The answer names the tag's source commit (from
// release-manifest.json, cross-checked against the requested tag).
func FetchRelease(ctx context.Context, dir, repo, tag, goarch string) (ReleaseInfo, error) {
	base := fmt.Sprintf("%s/%s/releases/download/%s", githubDownloadBase, repo, tag)
	sums, err := httpGet(ctx, base+"/SHA256SUMS")
	if err != nil {
		return ReleaseInfo{}, fmt.Errorf("the release %s of %s: %w", tag, repo, err)
	}
	pfName := "pf-linux-" + goarch
	tgName := piWebAssetName(tag)

	pf, err := fetchVerified(ctx, base+"/"+pfName, string(sums), pfName)
	if err != nil {
		return ReleaseInfo{}, err
	}
	// the run-proof: the checksummed binary must answer --version with
	// the tag, exactly the way tools/install.sh proves a staged binary.
	probe := filepath.Join(dir, "."+pfName+".probe")
	if err := os.WriteFile(probe, pf, 0o755); err != nil {
		return ReleaseInfo{}, err
	}
	defer os.Remove(probe)
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(pctx, probe, "--version").Output()
	if err != nil {
		return ReleaseInfo{}, fmt.Errorf("the payload %s does not run (%v) — a release's pf must answer --version", pfName, err)
	}
	if strings.TrimSpace(string(out)) != tag {
		return ReleaseInfo{}, fmt.Errorf("the payload %s answers --version with %q, the release is %s: a published checksum is not a version proof", pfName, strings.TrimSpace(string(out)), tag)
	}

	tgz, err := fetchVerified(ctx, base+"/"+tgName, string(sums), tgName)
	if err != nil {
		return ReleaseInfo{}, err
	}

	manifestBody, err := httpGet(ctx, base+"/release-manifest.json")
	if err != nil {
		return ReleaseInfo{}, fmt.Errorf("the release %s of %s: %w", tag, repo, err)
	}
	var manifest releaseManifest
	if err := json.Unmarshal(manifestBody, &manifest); err != nil {
		return ReleaseInfo{}, fmt.Errorf("release-manifest.json of %s: %w", tag, err)
	}
	if manifest.Tag != tag {
		return ReleaseInfo{}, fmt.Errorf("release-manifest.json of %s names the tag %s", tag, manifest.Tag)
	}
	if manifest.Commit == "" {
		return ReleaseInfo{}, fmt.Errorf("release-manifest.json of %s names no source commit (a seed without provenance is unverifiable)", tag)
	}

	if err := os.WriteFile(filepath.Join(dir, pfName), pf, 0o755); err != nil {
		return ReleaseInfo{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, tgName), tgz, 0o644); err != nil {
		return ReleaseInfo{}, err
	}
	return ReleaseInfo{Tag: tag, Commit: manifest.Commit}, nil
}

// releaseCheckoutSet is the entry set a version directory's release
// component owns — the same set the image's seed stage copies: the
// checkout root's package.json plus the skills and workflows trees.
func releaseCheckoutSet(path string) bool {
	if path == "package.json" {
		return true
	}
	return strings.HasPrefix(path, "skills/") || strings.HasPrefix(path, "workflows/")
}

// FetchReleaseCheckout extracts the release checkout of the tag into
// dir (which must exist): the codeload tarball of the channel
// repository at the tag, reduced to the entries a version directory's
// release component owns (package.json, skills/, workflows/) — the
// same set the image's seed stage copies.
func FetchReleaseCheckout(ctx context.Context, dir, repo, tag string) error {
	body, err := httpGet(ctx, githubDownloadBase+"/"+repo+"/archive/refs/tags/"+tag+".tar.gz")
	if err != nil {
		return fmt.Errorf("the checkout of %s at %s: %w", repo, tag, err)
	}
	// the tarball's entries sit under <repo>-<tag>/; the set is judged
	// after that prefix is stripped.
	prefix := ""
	err = untar(bytes.NewReader(body), func(path string, mode int64, body []byte) error {
		name := strings.TrimPrefix(path, "./")
		if i := strings.IndexByte(name, '/'); i >= 0 && prefix == "" {
			prefix = name[:i]
		}
		if prefix == "" {
			return nil // the archive's root entry is not part of the checkout
		}
		if !strings.HasPrefix(name, prefix+"/") {
			return nil
		}
		name = strings.TrimPrefix(name, prefix+"/")
		if !releaseCheckoutSet(name) {
			return nil
		}
		return writeTarEntry(dir, name, mode, body)
	})
	if err != nil {
		return err
	}
	// every entry of the set must be there: a checkout without the
	// checkout root's own package.json is not the release component.
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		return fmt.Errorf("the checkout of %s at %s carries no package.json: %v", repo, tag, err)
	}
	return nil
}

// untar walks one tar.gz body, handing every regular-file entry to fn.
func untar(r io.Reader, fn func(path string, mode int64, body []byte) error) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := fn(hdr.Name, int64(hdr.Mode), body); err != nil {
			return err
		}
	}
}

// writeTarEntry writes one extracted entry under dir.
func writeTarEntry(dir, path string, mode int64, body []byte) error {
	// a tarball entry never writes outside dir: the path is judged on
	// its cleaned, slash-separated form and only a clean relative path
	// of regular names lands.
	clean := filepath.Clean("/" + strings.TrimPrefix(path, "/"))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || strings.HasPrefix(clean, ".") || strings.Contains(clean, "..") {
		return fmt.Errorf("the tarball's entry %q is not a clean relative path", path)
	}
	target := filepath.Join(dir, clean)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, body, os.FileMode(mode))
}

// piWebAssetName is the Pi Web payload's name as release.sh publishes
// it.
func piWebAssetName(tag string) string {
	return "silgrid-pi-web-" + tag + ".tgz"
}
