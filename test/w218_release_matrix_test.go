package test

// polyforge#218 e2e: the release matrix carries the darwin assets; since
// the server is the one pf binary (`pf aihub`), so the
// matrix is one binary per platform. Spec (polyforge#218, "## Requirements"):
// "A release MUST publish the full darwin asset matrix covered by
// SHA256SUMS, and --verify MUST check the complete set" — now exactly the 4
// payload assets pf-linux-amd64, pf-darwin-arm64, pf-darwin-amd64 and
// aihub-image.digest, plus SHA256SUMS covering those four; no aihub-linux-*
// or aihub-darwin-* binary asset is published any more. `release.sh
// --verify` checks that complete list, `sha256sum -c SHA256SUMS` passes on
// the downloaded set, and a checksum mismatch (a tampered pf asset) fails
// it. The aihub docker image stays linux/amd64-only and no linux-arm64
// asset appears (asserted here as the no-linux-arm64 guard of the same
// scenarios).
//
// Same fake-env world as release_script_test.go (newRelWorld): a local
// bare repo is origin, git/docker/gh are stubs on PATH, no network is
// touched.
//
// The build stub leaves the artifacts RELEASE_BUILD_CMD's documented
// contract names (artifacts/release-$TAG/pf, .../pf-darwin-arm64,
// .../pf-darwin-amd64); the assertions are on the PUBLISHED release only.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w218BinAssets is the full binary matrix of a release: the one pf binary
// for linux-amd64 and both darwin architectures, sorted
// like the script's own WANT list.
var w218BinAssets = []string{
	"pf-darwin-amd64", "pf-darwin-arm64", "pf-linux-amd64",
}

// w218BuildCmd stands in for the build gate (RELEASE_BUILD_CMD, the
// documented override in .polyforge/release.sh's header): it leaves the
// three pf artifacts in artifacts/release-$TAG.
const w218BuildCmd = `echo build >> "$CALLLOG"
ART=artifacts/release-$TAG
mkdir -p "$ART"
for n in pf pf-darwin-arm64 pf-darwin-amd64; do
  echo polyforge-release-bin > "$ART/$n"
done`

// w218Publish cuts v1.2.1 in the fake world with the full-matrix build cmd
// and answers the world and the published release's directory.
func w218Publish(t *testing.T) (*relWorld, string) {
	t.Helper()
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	out, err := w.run([]string{"RELEASE_BUILD_CMD=" + w218BuildCmd})
	if err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	return w, filepath.Join(w.fakeRel, "v1.2.1")
}

// TestW218ReleaseCarriesFullDarwinMatrix: a release cut by release.sh MUST
// carry exactly the full matrix — the 4 payload assets (the 3 pf binaries +
// aihub-image.digest) + SHA256SUMS, SHA256SUMS covering the four — no
// aihub binary asset and no linux-arm64 asset (the image
// stays linux/amd64-only, proven by the docker build line the happy-path
// test pins).
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
			t.Fatalf("published release v1.2.1 is missing %s: every release MUST carry the full matrix (pf linux-amd64, darwin-arm64, darwin-amd64 + the image digest + SHA256SUMS); published assets: %v", f, gotNames)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("published release v1.2.1 has %d assets, want exactly the %d of the matrix %v; published assets: %v", len(got), len(want), want, gotNames)
	}
	for f := range got {
		if strings.Contains(f, "linux-arm64") {
			t.Fatalf("linux-arm64 asset %q published: the matrix gains darwin only, never linux-arm64; published assets: %v", f, gotNames)
		}
		if strings.HasPrefix(f, "aihub-linux-") || strings.HasPrefix(f, "aihub-darwin-") {
			t.Fatalf("aihub binary asset %q published: the server is the pf binary (`pf aihub`,); published assets: %v", f, gotNames)
		}
	}
	// SHA256SUMS covers every payload asset of the matrix (the 3 pf
	// binaries + the image digest), one line each.
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
// the full matrix MUST pass — the complete list (4 payload assets +
// SHA256SUMS) checks out and `sha256sum -c SHA256SUMS` passes on the
// downloaded set; a tampered pf asset MUST fail it (the checksum covers
// the darwin set, not just the linux one); and a release that still
// carries an aihub binary asset fails the exact asset-set check.
func TestW218ReleaseVerifyChecksFullMatrix(t *testing.T) {
	w, rel := w218Publish(t)
	// GIVEN the release release.sh itself published: SHA256SUMS over the
	// four payload assets.
	c := exec.Command("sha256sum", "-c", "SHA256SUMS")
	c.Dir = rel
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("the published SHA256SUMS does not check out: %v\n%s", err, out)
	}
	// WHEN release.sh --verify audits it, THEN the complete asset list
	// (the darwin assets included) must check out and sha256sum -c must
	// pass: exit 0.
	vout, verr := w.run(nil, "--verify", "v1.2.1")
	if verr != nil {
		t.Fatalf("release.sh --verify of a release carrying the full matrix MUST pass (the complete asset list, darwin assets included, plus sha256sum -c SHA256SUMS); observed exit %v:\n%s", verr, vout)
	}
	if !strings.Contains(vout, "PASS sha256sum -c SHA256SUMS") {
		t.Fatalf("verify output does not show the checksum check passing:\n%s", vout)
	}
	if !strings.Contains(vout, "PASS exactly the 4 payload assets + SHA256SUMS") {
		t.Fatalf("verify output does not show the exact asset-set check passing:\n%s", vout)
	}
	// a pre-consolidation aihub binary asset next to the matrix fails the
	// exact asset-set check.
	extra := filepath.Join(rel, "aihub-linux-amd64")
	relWrite(t, extra, "polyforge-release-bin\n", 0o755)
	if vout3, verr3 := w.run(nil, "--verify", "v1.2.1"); verr3 == nil || !strings.Contains(vout3, "FAIL asset set differs") {
		t.Fatalf("a release carrying an aihub binary asset MUST fail --verify's exact asset-set check; observed exit %v:\n%s", verr3, vout3)
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	// a tampered darwin pf asset fails it: the checksum check covers the
	// darwin set (the mismatch is caught at sha256sum, not by the asset
	// set differing).
	if err := os.WriteFile(filepath.Join(rel, "pf-darwin-arm64"), []byte("tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	vout2, verr2 := w.run(nil, "--verify", "v1.2.1")
	if verr2 == nil {
		t.Fatalf("a tampered darwin pf asset MUST fail --verify (sha256sum -c SHA256SUMS covers the darwin set); observed exit 0:\n%s", vout2)
	}
	ok := false
	for _, l := range strings.Split(vout2, "\n") {
		if strings.HasPrefix(l, "FAIL") && strings.Contains(l, "sha256sum") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("the tampered darwin pf asset must fail the checksum check (a FAIL line naming sha256sum); observed:\n%s", vout2)
	}
}
