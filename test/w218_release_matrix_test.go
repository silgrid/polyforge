package test

// polyforge#218 e2e: the release matrix gains the darwin assets. Spec
// (artifacts/spec.md, "## Requirements"): "A release MUST publish the full
// darwin asset matrix covered by SHA256SUMS, and --verify MUST check the
// complete set" — the unchanged linux set (aihub-linux-amd64,
// pf-linux-amd64, aihub-image.digest) plus aihub-darwin-arm64,
// pf-darwin-arm64, aihub-darwin-amd64 and pf-darwin-amd64 — exactly the 8
// assets (6 binaries + digest + SHA256SUMS), with SHA256SUMS covering every
// asset; `release.sh --verify` checks the complete 8-asset list including
// the darwin assets, `sha256sum -c SHA256SUMS` passes on the downloaded set,
// and a checksum mismatch fails it. The aihub docker image stays
// linux/amd64-only and no linux-arm64 asset appears (asserted here as the
// no-linux-arm64 guard of the same scenarios).
//
// Same fake-env world as release_script_test.go (newRelWorld): a local
// bare repo is origin, git/docker/gh are stubs on PATH, no network is
// touched.
//
// Spec under-specification (flagged, not reverse-engineered): the darwin
// artifact layout RELEASE_BUILD_CMD must leave is not in the spec (its
// documented contract names only aihub and pf). The build stub below
// leaves BOTH plausible layouts (flat <asset-name> files next to aihub/pf,
// and one directory per arch); the assertions are on the PUBLISHED release
// only — whichever layout the release consumes, the published set must
// carry the full matrix.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w218BinAssets is the full binary matrix of a release: the unchanged
// linux set plus the darwin pair (spec: "carries pf + aihub
// darwin-arm64/darwin-amd64 assets in the same release ... alongside
// linux-amd64"), sorted like the script's own WANT list.
var w218BinAssets = []string{
	"aihub-darwin-amd64", "aihub-darwin-arm64", "aihub-linux-amd64",
	"pf-darwin-amd64", "pf-darwin-arm64", "pf-linux-amd64",
}

// w218BuildCmd stands in for the build gate (RELEASE_BUILD_CMD, the
// documented override in .polyforge/release.sh's header): besides the two
// artifacts the script consumes today ($ART/aihub, $ART/pf), it leaves the
// darwin set of the matrix in artifacts/release-$TAG, in both plausible
// artifact layouts (see the under-specification note above).
const w218BuildCmd = `echo build >> "$CALLLOG"
ART=artifacts/release-$TAG
mkdir -p "$ART"
for n in aihub pf aihub-darwin-arm64 pf-darwin-arm64 aihub-darwin-amd64 pf-darwin-amd64; do
  echo polyforge-release-bin > "$ART/$n"
done
mkdir -p "$ART/darwin-arm64" "$ART/darwin-amd64"
cp "$ART/aihub" "$ART/darwin-arm64/aihub"; cp "$ART/pf" "$ART/darwin-arm64/pf"
cp "$ART/aihub" "$ART/darwin-amd64/aihub"; cp "$ART/pf" "$ART/darwin-amd64/pf"`

// w218Publish cuts v1.3.0 in the fake world with the full-matrix build cmd
// and answers the world and the published release's directory.
func w218Publish(t *testing.T) (*relWorld, string) {
	t.Helper()
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	out, err := w.run([]string{"RELEASE_BUILD_CMD=" + w218BuildCmd})
	if err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	return w, filepath.Join(w.fakeRel, "v1.3.0")
}

// TestW218ReleaseCarriesFullDarwinMatrix: a release cut by release.sh MUST
// carry the full matrix — the 8 assets (the 6 binaries + aihub-image.digest
// + SHA256SUMS), SHA256SUMS covering every asset — and no linux-arm64
// asset (the matrix gains darwin only; the image stays linux/amd64-only,
// unchanged production, proven by the unchanged docker build line the
// existing happy-path test pins).
func TestW218ReleaseCarriesFullDarwinMatrix(t *testing.T) {
	_, rel := w218Publish(t)
	want := append(append([]string{}, w218BinAssets...), "aihub-image.digest", "SHA256SUMS")
	entries, err := os.ReadDir(rel)
	if err != nil {
		t.Fatalf("read published release dir: %v", err)
	}
	got := map[string]bool{}
	var gotNames []string
	for _, e := range entries {
		if e.Name() == ".body" {
			continue // the release notes, not an asset
		}
		got[e.Name()] = true
		gotNames = append(gotNames, e.Name())
	}
	for _, f := range want {
		if !got[f] {
			t.Fatalf("published release v1.3.0 is missing %s: every release MUST carry the full matrix (aihub+pf darwin-arm64 and darwin-amd64 alongside the unchanged linux-amd64 set; 6 binaries + the image digest + SHA256SUMS); published assets: %v", f, gotNames)
		}
	}
	for f := range got {
		if strings.Contains(f, "linux-arm64") {
			t.Fatalf("linux-arm64 asset %q published: the matrix gains darwin only, never linux-arm64; published assets: %v", f, gotNames)
		}
	}
	// SHA256SUMS covers every asset of the matrix (the 6 binaries + the
	// image digest), one line each.
	sums, _ := os.ReadFile(filepath.Join(rel, "SHA256SUMS"))
	covered := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		if fields := strings.Fields(l); len(fields) == 2 {
			covered[fields[1]] = true
		}
	}
	for _, f := range append(append([]string{}, w218BinAssets...), "aihub-image.digest") {
		if !covered[f] {
			t.Fatalf("SHA256SUMS does not cover %s: it MUST cover every asset of the matrix; observed SHA256SUMS:\n%s", f, sums)
		}
	}
	if len(covered) != len(w218BinAssets)+1 {
		t.Fatalf("SHA256SUMS covers %d files, want exactly the %d assets of the matrix; observed SHA256SUMS:\n%s", len(covered), len(w218BinAssets)+1, sums)
	}
}

// TestW218ReleaseVerifyChecksFullMatrix: --verify of a release carrying
// the full matrix MUST pass — the complete 8-asset list (the darwin assets
// included) checks out and `sha256sum -c SHA256SUMS` passes on the
// downloaded set; and a tampered darwin asset MUST fail it (the checksum
// covers the darwin set, not just the linux one).
func TestW218ReleaseVerifyChecksFullMatrix(t *testing.T) {
	w, rel := w218Publish(t)
	// GIVEN a release carrying the full matrix: a release cut by the
	// pre-change script publishes the linux set only, so complete it here
	// — add any missing darwin asset and rewrite SHA256SUMS over every
	// asset (what a full-matrix release's own publish leaves behind).
	// Against a release cut by the changed script this is a no-op rewrite
	// of an identical file.
	for _, f := range w218BinAssets {
		if _, err := os.Stat(filepath.Join(rel, f)); err != nil {
			relWrite(t, filepath.Join(rel, f), "polyforge-release-bin\n", 0o755)
		}
	}
	c := exec.Command("sha256sum", append(append([]string{}, w218BinAssets...), "aihub-image.digest")...)
	c.Dir = rel
	sums, err := c.Output()
	if err != nil {
		t.Fatalf("sha256sum over the release assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rel, "SHA256SUMS"), sums, 0o644); err != nil {
		t.Fatal(err)
	}
	// WHEN release.sh --verify audits it, THEN the complete asset list
	// (the darwin assets included) must check out and sha256sum -c must
	// pass: exit 0.
	vout, verr := w.run(nil, "--verify", "v1.3.0")
	if verr != nil {
		t.Fatalf("release.sh --verify of a release carrying the full matrix MUST pass (the complete asset list, darwin assets included, plus sha256sum -c SHA256SUMS); observed exit %v:\n%s", verr, vout)
	}
	if !strings.Contains(vout, "PASS sha256sum -c SHA256SUMS") {
		t.Fatalf("verify output does not show the checksum check passing:\n%s", vout)
	}
	// a tampered darwin asset fails it: the checksum check covers the
	// darwin set (the mismatch is caught at sha256sum, not by the asset
	// set differing).
	if err := os.WriteFile(filepath.Join(rel, "aihub-darwin-arm64"), []byte("tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	vout2, verr2 := w.run(nil, "--verify", "v1.3.0")
	if verr2 == nil {
		t.Fatalf("a tampered darwin asset MUST fail --verify (sha256sum -c SHA256SUMS covers the darwin set); observed exit 0:\n%s", vout2)
	}
	ok := false
	for _, l := range strings.Split(vout2, "\n") {
		if strings.HasPrefix(l, "FAIL") && strings.Contains(l, "sha256sum") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("the tampered darwin asset must fail the checksum check (a FAIL line naming sha256sum); observed:\n%s", vout2)
	}
}
