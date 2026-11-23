// carries: internal/polyforge/components/status.go,
//
//	internal/polyforge/components/fetch.go,
//	internal/polyforge/components/assemble.go
package components

// assemble_test.go — polyforge#190 (84-inplace-upgrade), u1: the
// upgrade's staging half, authored test-first against AC2's own words.
// The release source is a stub GitHub (the channel repository's
// release assets and codeload tarball, the same shapes
// .polyforge/release.sh publishes); the assembled version directory is
// judged by the manifest contract the image's seed carries
// (polyforge#162), never by the implementation's own output. Every
// test names Stage or Verify, so the acceptance proof
// `go test ./internal/polyforge/components/ -run 'Stage|Verify'` runs
// exactly this file's behaviour.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// ---- the stub channel repository ----

// fakeRelease is one stub GitHub's served release: the payloads the
// upgrade downloads and the codeload checkout of the tag.
type fakeRelease struct {
	tag    string
	commit string
	// pfBody is the platform's pf asset; it must answer --version with
	// the tag (a shell script does in the tests).
	pfBody string
	// sums, when non-nil, replaces the derived SHA256SUMS body (the
	// mismatch cases).
	sums *string
	// piWebFiles is the packed pi-web package's files (path under
	// package/ → body).
	piWebFiles map[string]string
	// checkoutFiles is the codeload tarball's files (repo-relative).
	checkoutFiles map[string]string
}

// pfAssetName is the platform's pf payload as release.sh publishes it
// (the pi-web payload's name comes from the package's own helper).
func pfAssetName(goarch string) string { return "pf-linux-" + goarch }

// serveRelease stands the stub GitHub up and points the package's URL
// seams at it; the returned stop restores them.
func serveRelease(t *testing.T, rel fakeRelease) *httptest.Server {
	t.Helper()
	pfName := pfAssetName("amd64")
	tgName := piWebAssetName(rel.tag)
	sums := rel.sums
	if sums == nil {
		sumsBody := &strings.Builder{}
		fmt.Fprintf(sumsBody, "%s  %s\n", fakeSHA(t, rel.pfBody), pfName)
		fmt.Fprintf(sumsBody, "%s  %s\n", fakeSHA(t, "darwin-arm64-bin"), "pf-darwin-arm64")
		fmt.Fprintf(sumsBody, "%s  %s\n", fakeSHA(t, "darwin-amd64-bin"), "pf-darwin-amd64")
		fmt.Fprintf(sumsBody, "%s  %s\n", fakeSHA(t, "aihub-image-digest"), "aihub-image.digest")
		fmt.Fprintf(sumsBody, "%s  %s\n", fakeSHA(t, tgzOf(t, rel.piWebFiles)), tgName)
		s := sumsBody.String()
		sums = &s
	}
	manifest := fmt.Sprintf("{\n  \"tag\": %q,\n  \"commit\": %q,\n  \"order\": \"aihub first\",\n  \"artifacts\": []\n}\n", rel.tag, rel.commit)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/silgrid/polyforge/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name": %q}`, rel.tag)
	})
	dl := func(name string, body []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }
	}
	mux.HandleFunc("/silgrid/polyforge/releases/download/"+rel.tag+"/SHA256SUMS", dl("sums", []byte(*sums)))
	mux.HandleFunc("/silgrid/polyforge/releases/download/"+rel.tag+"/release-manifest.json", dl("manifest", []byte(manifest)))
	mux.HandleFunc("/silgrid/polyforge/releases/download/"+rel.tag+"/"+pfName, dl("pf", []byte(rel.pfBody)))
	mux.HandleFunc("/silgrid/polyforge/releases/download/"+rel.tag+"/"+tgName, dl("piweb", tgzOf(t, rel.piWebFiles)))
	mux.HandleFunc("/silgrid/polyforge/archive/refs/tags/"+rel.tag+".tar.gz", dl("codeload", tarballOf(t, rel.tag, rel.checkoutFiles)))
	srv := httptest.NewServer(mux)

	oldAPI, oldDL := githubAPIBase, githubDownloadBase
	githubAPIBase, githubDownloadBase = srv.URL, srv.URL
	t.Cleanup(func() {
		githubAPIBase, githubDownloadBase = oldAPI, oldDL
		srv.Close()
	})
	return srv
}

// sortedKeys answers the map's keys in order, so the stub archives are
// byte-stable (a checksummed body must not depend on map order).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// fakeSHA answers the sha256 hex of a body (the test's own derivation,
// never the implementation's).
func fakeSHA(t *testing.T, body any) string {
	t.Helper()
	var b []byte
	switch v := body.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		t.Fatalf("fakeSHA: unsupported body %T", body)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// tgzOf answers the pi-web package tarball (the npm pack shape: every
// entry under package/).
func tgzOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, path := range sortedKeys(files) {
		body := files[path]
		name := "package/" + strings.TrimPrefix(path, "package/") + "\x00"
		name = strings.TrimSuffix(name, "\x00")
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("tgz header %s: %v", path, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tgz body %s: %v", path, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// tarballOf answers the codeload tarball of the tag: every entry under
// <repo>-<tag>/.
func tarballOf(t *testing.T, tag string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, path := range sortedKeys(files) {
		body := files[path]
		name := "polyforge-" + tag + "/" + strings.TrimPrefix(path, "/")
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("tarball header %s: %v", path, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tarball body %s: %v", path, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseFixture is one complete stub release of the public channel,
// the shape the assembly tests serve.
func releaseFixture(tag, commit string) fakeRelease {
	return fakeRelease{
		tag:    tag,
		commit: commit,
		pfBody: "#!/bin/sh\ncase \"$1\" in --version) echo " + tag + " ;; esac\n",
		piWebFiles: map[string]string{
			"package.json": `{"name": "@silgrid/pi-web", "version": "` + strings.TrimPrefix(tag, "v") + `"}` + "\n",
			"app/page.js":  "export default function Page() {}\n",
			"bin/probe.js": "// the packed package's bin\n",
		},
		checkoutFiles: map[string]string{
			"package.json":                `{"name": "polyforge", "version": "` + tag + `"}` + "\n",
			"skills/polyforge/SKILL.md":   "# the routing skill of " + tag + "\n",
			"workflows/develop/README.md": "# the develop lane of " + tag + "\n",
			"docs/README.md":              "# not part of the release component\n",
		},
	}
}

// currentRoot answers one components root whose current is the spec's
// version directory (installed by the layout's own writer), with one
// carried file inside Pi Web's node_modules.
func currentRoot(t *testing.T, spec seedSpec) (root string, cur *Manifest) {
	t.Helper()
	root = t.TempDir()
	// the spec's own defaults name the version (a zero spec is v0.2.4);
	// the symlink must target exactly the directory the tree was
	// written to (u2's red run caught the unfilled spelling pointing
	// current at the releases directory itself).
	s := spec.filled()
	dir := filepath.Join(root, ReleasesDirname, s.version)
	cur = writeVersionDir(t, dir, spec)
	wfile(t, filepath.Join(dir, PathPiWeb, "node_modules/node-pty/build/pty.node"), "the native build\n", 0o644)
	if err := os.Symlink(filepath.Join(ReleasesDirname, s.version), filepath.Join(root, CurrentLinkname)); err != nil {
		t.Fatal(err)
	}
	return root, cur
}

// TestComponentsStageStatusFileContract pins the volume's status file
// and cancel marker: the round trip of one failed upgrade's record,
// the absent read, and the marker's request/detect/clear lifecycle.
func TestComponentsStageStatusFileContract(t *testing.T) {
	root := t.TempDir()

	if got, err := ReadUpgradeStatus(root); err != nil || got != nil {
		t.Fatalf("a root with no record answers (nil, nil), got (%v, %v)", got, err)
	}
	wrote := &UpgradeStatus{
		Operation: OperationUpgrade,
		State:     UpgradeStateFailed,
		Phase:     "stage",
		From:      "v0.2.4",
		To:        "v0.2.5",
		Error:     "the payload silgrid-pi-web-v0.2.5.tgz does not match its SHA256SUMS line",
	}
	if err := WriteUpgradeStatus(root, wrote); err != nil {
		t.Fatalf("WriteUpgradeStatus: %v", err)
	}
	got, err := ReadUpgradeStatus(root)
	if err != nil {
		t.Fatalf("ReadUpgradeStatus after a write: %v", err)
	}
	if got == nil {
		t.Fatal("the written record must read back")
	}
	if got.SchemaVersion != upgradeStatusSchemaVersion || got.Operation != OperationUpgrade ||
		got.State != UpgradeStateFailed || got.Phase != "stage" || got.From != "v0.2.4" ||
		got.To != "v0.2.5" || !strings.Contains(got.Error, "SHA256SUMS") {
		t.Fatalf("the record round-tripped wrong: %+v", got)
	}
	if got.At == "" {
		t.Fatal("the write must stamp its own moment")
	}
	if _, err := time.Parse(time.RFC3339, got.At); err != nil {
		t.Fatalf("the stamp must be RFC3339: %v", err)
	}
	// the raw body is JSON at the settled name (Pi Web reads it across
	// its own restart; the name is the cross-language contract).
	if _, err := os.Stat(filepath.Join(root, UpgradeStatusFilename)); err != nil {
		t.Fatalf("the record lives at %s: %v", UpgradeStatusFilename, err)
	}

	if UpgradeCancelRequested(root) {
		t.Fatal("a fresh root carries no cancel marker")
	}
	if err := RequestUpgradeCancel(root); err != nil {
		t.Fatalf("RequestUpgradeCancel: %v", err)
	}
	if !UpgradeCancelRequested(root) {
		t.Fatal("the marker must be detected")
	}
	if err := RequestUpgradeCancel(root); err != nil {
		t.Fatalf("RequestUpgradeCancel must be idempotent: %v", err)
	}
	if err := ClearUpgradeCancel(root); err != nil {
		t.Fatalf("ClearUpgradeCancel: %v", err)
	}
	if UpgradeCancelRequested(root) {
		t.Fatal("a cleared marker must not be detected")
	}
}

// TestComponentsStageAssemblesReleaseIntoVersionDir pins the whole
// staging half: the fetched assets and checkout become one complete
// version directory — every component at its settled path, the carried
// harness trees and Pi Web dependencies from current, the manifest of
// one contract with the image's seed — and the components root is not
// touched by any of it.
func TestComponentsStageAssemblesReleaseIntoVersionDir(t *testing.T) {
	root, cur := currentRoot(t, seedSpec{})
	before := rootState(t, root)
	rel := releaseFixture("v0.2.5", "abcd1234ef56")
	serveRelease(t, rel)

	ctx := context.Background()
	assets := t.TempDir()
	info, err := FetchRelease(ctx, assets, "silgrid/polyforge", "v0.2.5", "amd64")
	if err != nil {
		t.Fatalf("FetchRelease: %v", err)
	}
	if info.Tag != "v0.2.5" || info.Commit != "abcd1234ef56" {
		t.Fatalf("FetchRelease answered %+v, want the tag and the manifest's commit", info)
	}
	checkout := t.TempDir()
	if err := FetchReleaseCheckout(ctx, checkout, "silgrid/polyforge", "v0.2.5"); err != nil {
		t.Fatalf("FetchReleaseCheckout: %v", err)
	}
	for _, path := range []string{"package.json", "skills/polyforge/SKILL.md", "workflows/develop/README.md"} {
		if _, err := os.Stat(filepath.Join(checkout, path)); err != nil {
			t.Fatalf("the checkout must carry %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(checkout, "docs/README.md")); err == nil {
		t.Fatal("the checkout must keep to the release component's set: docs/ is not part of it")
	}

	dest := filepath.Join(t.TempDir(), "v0.2.5")
	npmDirs := []string(nil)
	m, err := AssembleVersionDir(AssembleOptions{
		Root: root, Dest: dest, Version: "v0.2.5", Commit: info.Commit,
		Platform: cur.Platform, AssetsDir: assets, CheckoutDir: checkout,
		NPMInstall: func(dir string) error { npmDirs = append(npmDirs, dir); return nil },
	})
	if err != nil {
		t.Fatalf("AssembleVersionDir: %v", err)
	}
	if len(npmDirs) != 1 || npmDirs[0] != filepath.Join(dest, PathPiWeb) {
		t.Fatalf("npm reconciles exactly the assembled pi-web tree, got %v", npmDirs)
	}

	// bin/pf: the verified payload, executable.
	pf, err := os.ReadFile(filepath.Join(dest, PathPF))
	if err != nil || string(pf) != rel.pfBody {
		t.Fatalf("bin/pf must be the release's payload verbatim (%v)", err)
	}
	if fi, _ := os.Stat(filepath.Join(dest, PathPF)); fi != nil && fi.Mode()&0o111 == 0 {
		t.Fatal("bin/pf must be executable")
	}
	// the release checkout at the version directory's root.
	for _, path := range []string{"package.json", "skills/polyforge/SKILL.md", "workflows/develop/README.md"} {
		body, err := os.ReadFile(filepath.Join(checkout, path))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dest, path))
		if err != nil || !bytes.Equal(body, got) {
			t.Fatalf("the version directory must carry the checkout's %s verbatim", path)
		}
	}
	// pi-web: the packed package, stripped of its package/ prefix,
	// with current's node_modules carried beside it.
	pw, err := os.ReadFile(filepath.Join(dest, PathPiWeb, "package.json"))
	if err != nil || !strings.Contains(string(pw), `"version": "0.2.5"`) {
		t.Fatalf("the packed package.json must land at pi-web/: %v (%s)", err, pw)
	}
	if _, err := os.Stat(filepath.Join(dest, PathPiWeb, "app/page.js")); err != nil {
		t.Fatalf("the packed package's files must land at pi-web/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, PathPiWeb, "node_modules/node-pty/build/pty.node")); err != nil {
		t.Fatalf("pi-web's node_modules must be carried from current: %v", err)
	}
	// the harnesses carried from current with their own versions.
	for _, path := range []string{PathHarnessPi, PathHarnessClaude} {
		if _, err := os.Stat(filepath.Join(dest, path, "package.json")); err != nil {
			t.Fatalf("the harness tree %s must be carried from current: %v", path, err)
		}
	}

	// the manifest: one contract with the image's seed — the product
	// version on pf and release, the packed pi-web's own version, the
	// carried harness versions, current's channel and base floor.
	if m == nil {
		t.Fatal("the assembly must answer its manifest")
	}
	if m.Version != "v0.2.5" || m.Commit != "abcd1234ef56" || m.Platform != cur.Platform {
		t.Fatalf("the manifest's provenance: %+v", m)
	}
	if m.ChannelRepo != cur.ChannelRepo || m.MinBase != cur.MinBase {
		t.Fatalf("the channel and the base floor are carried from current: %+v", m)
	}
	if e := m.Component(ComponentPF); e == nil || e.Version != "v0.2.5" {
		t.Fatalf("the pf entry: %+v", m.Component(ComponentPF))
	}
	if e := m.Component(ComponentRelease); e == nil || e.Version != "v0.2.5" {
		t.Fatalf("the release entry: %+v", m.Component(ComponentRelease))
	}
	if e := m.Component(ComponentPiWeb); e == nil || e.Version != "0.2.5" {
		t.Fatalf("the pi-web entry carries the packed package's own version: %+v", m.Component(ComponentPiWeb))
	}
	for _, name := range []string{ComponentHarnessPi, ComponentHarnessClaude} {
		want := cur.Component(name)
		got := m.Component(name)
		if got == nil || want == nil || got.Version != want.Version {
			t.Fatalf("the harness %s carries current's own version: %+v", name, got)
		}
	}
	if err := m.VerifyTree(dest); err != nil {
		t.Fatalf("the assembled tree must verify against its manifest: %v", err)
	}
	// the published manifest is what the directory carries.
	if published, err := ReadManifest(dest); err != nil || published.Version != "v0.2.5" {
		t.Fatalf("the manifest must be written into the directory: %v", err)
	}

	if got := rootState(t, root); got != before {
		t.Fatalf("the staging half must not touch the components root:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}

// TestComponentsVerifyRefusesSHA256SUMSMismatch pins AC2's first half:
// a payload whose bytes do not match its SHA256SUMS line never reaches
// the volume — the fetch refuses with the settled sentinel and the
// components root is untouched.
func TestComponentsVerifyRefusesSHA256SUMSMismatch(t *testing.T) {
	root, _ := currentRoot(t, seedSpec{})
	before := rootState(t, root)
	rel := releaseFixture("v0.2.5", "abcd1234ef56")
	rel.pfBody += "# tampered after the sums were derived\n"
	rel.sums = nil // derived from the untampered bodies would be a lie; serve explicit sums instead
	untampered := releaseFixture("v0.2.5", "abcd1234ef56")
	sums := &strings.Builder{}
	fmt.Fprintf(sums, "%s  %s\n", fakeSHA(t, untampered.pfBody), pfAssetName("amd64"))
	fmt.Fprintf(sums, "%s  %s\n", fakeSHA(t, tgzOf(t, untampered.piWebFiles)), piWebAssetName("v0.2.5"))
	s := sums.String()
	rel.sums = &s
	serveRelease(t, rel)

	_, err := FetchRelease(context.Background(), t.TempDir(), "silgrid/polyforge", "v0.2.5", "amd64")
	if err == nil {
		t.Fatal("a payload that does not match its SHA256SUMS line must be refused")
	}
	if !errors.Is(err, ErrReleaseAssetMismatch) {
		t.Fatalf("the refusal is the settled sentinel, got: %v", err)
	}
	if !strings.Contains(err.Error(), pfAssetName("amd64")) {
		t.Fatalf("the refusal must name the payload: %v", err)
	}
	if got := rootState(t, root); got != before {
		t.Fatalf("a refused fetch must not touch the components root:\n%s", got)
	}
}

// TestComponentsVerifyRefusesMissingSumsLine pins the fail-closed side:
// a SHA256SUMS without the Pi Web payload's line refuses that payload
// — an unverifiable asset never reaches the volume.
func TestComponentsVerifyRefusesMissingSumsLine(t *testing.T) {
	root, _ := currentRoot(t, seedSpec{})
	before := rootState(t, root)
	rel := releaseFixture("v0.2.5", "abcd1234ef56")
	sums := &strings.Builder{}
	fmt.Fprintf(sums, "%s  %s\n", fakeSHA(t, rel.pfBody), pfAssetName("amd64"))
	s := sums.String()
	rel.sums = &s
	serveRelease(t, rel)

	_, err := FetchRelease(context.Background(), t.TempDir(), "silgrid/polyforge", "v0.2.5", "amd64")
	if err == nil || !errors.Is(err, ErrReleaseAssetMismatch) {
		t.Fatalf("a payload with no SHA256SUMS line must be refused with the sentinel, got: %v", err)
	}
	if !strings.Contains(err.Error(), piWebAssetName("v0.2.5")) {
		t.Fatalf("the refusal must name the payload: %v", err)
	}
	if got := rootState(t, root); got != before {
		t.Fatalf("a refused fetch must not touch the components root:\n%s", got)
	}
}

// TestComponentsVerifyRefusesPfVersionMismatch pins the run-proof: the
// checksummed pf binary must answer --version with the tag — a release
// published with the right checksums but the wrong binary inside is
// refused before anything is staged.
func TestComponentsVerifyRefusesPfVersionMismatch(t *testing.T) {
	rel := releaseFixture("v0.2.5", "abcd1234ef56")
	rel.pfBody = "#!/bin/sh\ncase \"$1\" in --version) echo v0.2.4 ;; esac\n" // the previous release's binary
	rel.sums = nil
	serveRelease(t, rel)

	_, err := FetchRelease(context.Background(), t.TempDir(), "silgrid/polyforge", "v0.2.5", "amd64")
	if err == nil {
		t.Fatal("a pf payload answering --version with another version must be refused")
	}
	if !strings.Contains(err.Error(), "--version") {
		t.Fatalf("the refusal must name the run-proof: %v", err)
	}
}

// TestComponentsVerifyLatestReleaseAnswersTag pins the channel lookup
// the check verb and the upgrade's default version share: the latest
// formal release's tag, read anonymously from the channel repository.
func TestComponentsVerifyLatestReleaseAnswersTag(t *testing.T) {
	serveRelease(t, releaseFixture("v0.2.5", "abcd1234ef56"))
	info, err := LatestRelease(context.Background(), "silgrid/polyforge")
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if info.Tag != "v0.2.5" {
		t.Fatalf("LatestRelease answered %+v, want the tag the channel names", info)
	}
}

// TestComponentsStageFailureRemovesThePartialDirectory pins AC2's
// second half on the assembly: a step that fails mid-way leaves no
// half-assembled version directory behind and the components root
// untouched.
func TestComponentsStageFailureRemovesThePartialDirectory(t *testing.T) {
	root, cur := currentRoot(t, seedSpec{})
	before := rootState(t, root)
	rel := releaseFixture("v0.2.5", "abcd1234ef56")
	serveRelease(t, rel)

	ctx := context.Background()
	assets := t.TempDir()
	if _, err := FetchRelease(ctx, assets, "silgrid/polyforge", "v0.2.5", "amd64"); err != nil {
		t.Fatalf("FetchRelease: %v", err)
	}
	// an empty checkout: the assembly fails when the release checkout's
	// package.json is missing.
	checkout := t.TempDir()
	dest := filepath.Join(t.TempDir(), "v0.2.5")
	if _, err := AssembleVersionDir(AssembleOptions{
		Root: root, Dest: dest, Version: "v0.2.5", Commit: "abcd1234ef56",
		Platform: cur.Platform, AssetsDir: assets, CheckoutDir: checkout,
		NPMInstall: func(string) error { return nil },
	}); err == nil {
		t.Fatal("an assembly whose release checkout is empty must fail")
	} else if !strings.Contains(err.Error(), "package.json") {
		t.Fatalf("the failure must name what it could not stage: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("a failed assembly must remove its partial directory: %v", err)
	}
	if got := rootState(t, root); got != before {
		t.Fatalf("a failed assembly must not touch the components root:\n%s", got)
	}
}
