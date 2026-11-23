// carries: .polyforge/release.sh, dist/workstation/Containerfile,
//
//	docs/workstation.md, docs/reference/release.md,
//	test/w86_workstation_release_test.go
package test

// w86_workstation_release_test.go — polyforge#86: the release pipeline's
// workstation image — built for linux/amd64 and linux/arm64 beside the aihub
// image (before the version is claimed, pushed after it), its index digest
// published as the workstation-image.digest asset, --verify checking the
// index digest, both platforms and the public channel's anonymous read,
// resume and refusal covering it like the aihub image, the channel rules
// (RELEASE_WORKSTATION_IMAGE, the suffixed line's explicit-set requirement)
// and the host-side file export the image carries.
//
// Same fake-env world as release_script_test.go (newRelWorld): a local bare
// repo is origin, git/docker/gh are stubs on PATH, nothing is published.
//
// Run (targeted): GOWORK=off go test -count=1 -run '^TestW86' ./test/

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w86BuildCalls splits the call log into the cacheonly (pre-claim) build, the
// --push (post-claim) build and the tag push's index, with -1 for a missing
// one. The pre-claim build carries --output type=cacheonly and never --push;
// the post-claim build carries --push.
func w86BuildCalls(calls []string) (cache, push, gitPush int) {
	cache, push, gitPush = -1, -1, -1
	for i, c := range calls {
		switch {
		case strings.HasPrefix(c, "git push"):
			gitPush = i
		case strings.HasPrefix(c, "docker buildx build"):
			if strings.Contains(c, " --push") {
				push = i
			} else {
				cache = i
			}
		}
	}
	return cache, push, gitPush
}

// TestW86Build (AC1): a release builds the multi-arch workstation image
// BEFORE the tag push (nothing is published before the version is claimed)
// and pushes it after; both builds carry the two platforms and the seed's
// injection points (the tag, the pinned commit, the channel repository); the
// registry's index digest is published as the workstation-image.digest asset
// and rides SHA256SUMS and release-manifest.json; the aihub image's flow is
// untouched (AC5).
func TestW86Build(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	out, err := w.runPinned(nil)
	if err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	calls := w.calls()
	head := relGit(t, w.work, "rev-parse", "HEAD")
	cache, push, gitPush := w86BuildCalls(calls)
	if cache < 0 {
		t.Fatalf("the release never built the workstation image (no cacheonly buildx build in the log): %v", calls)
	}
	if gitPush < 0 {
		t.Fatalf("no tag push in the log: %v", calls)
	}
	if cache > gitPush {
		t.Fatalf("the workstation image's build must come before the tag push (nothing is published before the claim): %v", calls)
	}
	if push < 0 || push < gitPush {
		t.Fatalf("the workstation image's push build must come after the tag push: %v", calls)
	}
	if aihub := relIndex(calls, "docker build"); aihub < 0 || aihub > cache {
		t.Fatalf("the aihub image build must stay before the workstation build: %v", calls)
	}
	build := calls[cache]
	for _, want := range []string{
		"--platform linux/amd64,linux/arm64",
		"--build-arg SEED_VERSION=v1.2.1",
		"--build-arg SEED_COMMIT=" + head,
		"--build-arg SEED_CHANNEL_REPO=silgrid/polyforge",
		"-f dist/workstation/Containerfile",
		"--tag ghcr.io/silgrid/polyforge-workstation:v1.2.1",
		"--output type=cacheonly",
	} {
		if !strings.Contains(build, want) {
			t.Errorf("the pre-claim buildx build lacks %q:\n%s", want, build)
		}
	}
	pushLine := calls[push]
	for _, want := range []string{
		"--platform linux/amd64,linux/arm64",
		"--build-arg SEED_VERSION=v1.2.1",
		"--tag ghcr.io/silgrid/polyforge-workstation:v1.2.1",
	} {
		if !strings.Contains(pushLine, want) {
			t.Errorf("the post-claim push build lacks %q (the push build repeats the same image):\n%s", want, pushLine)
		}
	}
	if strings.Contains(pushLine, "type=cacheonly") {
		t.Errorf("the post-claim build must push, not stay cacheonly:\n%s", pushLine)
	}

	rel := filepath.Join(w.fakeRel, "v1.2.1")
	// the index digest the registry reports is the published asset
	dg, _ := os.ReadFile(filepath.Join(rel, "workstation-image.digest"))
	if strings.TrimSpace(string(dg)) != relFakeDigest {
		t.Fatalf("workstation-image.digest = %q, want the registry's index digest %q", dg, relFakeDigest)
	}
	// SHA256SUMS covers exactly the 6 payloads, the workstation digest among
	// them; release-manifest.json derives from the sums, so the row rides.
	sums, _ := os.ReadFile(filepath.Join(rel, "SHA256SUMS"))
	if n := strings.Count(string(sums), "\n"); n != 6 {
		t.Fatalf("SHA256SUMS carries %d lines, want exactly the 6 payloads: %q", n, sums)
	}
	for _, n := range []string{"pf-linux-amd64", "pf-darwin-arm64", "pf-darwin-amd64", "aihub-image.digest", "workstation-image.digest", "silgrid-pi-web-v1.2.1.tgz"} {
		if !strings.Contains(string(sums), "  "+n+"\n") {
			t.Fatalf("SHA256SUMS lacks %s: %q", n, sums)
		}
	}
	if man, _ := os.ReadFile(filepath.Join(rel, "release-manifest.json")); string(man) != relManifest("v1.2.1", head, string(sums)) {
		t.Fatalf("release-manifest.json = %q, want the tag, the commit and every SHA256SUMS payload (the workstation digest included)", man)
	}
	// exactly the 8 assets beside the release notes
	relFiles := []string{"pf-linux-amd64", "pf-darwin-arm64", "pf-darwin-amd64", "aihub-image.digest", "workstation-image.digest", "silgrid-pi-web-v1.2.1.tgz", "SHA256SUMS", "release-manifest.json", ".body"}
	ents, err := os.ReadDir(rel)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != len(relFiles) {
		names := make([]string, len(ents))
		for i, e := range ents {
			names[i] = e.Name()
		}
		t.Fatalf("release v1.2.1 carries %d files, want exactly %v: %v", len(ents), relFiles, names)
	}
	// the aihub image's own flow is unchanged (AC5)
	if relIndex(calls, "docker build -f deploy/Dockerfile -t ghcr.io/silgrid/polyforge-aihub:v1.2.1") < 0 {
		t.Fatalf("the aihub image build line must stay exactly as it was: %v", calls)
	}
	if dg, _ := os.ReadFile(filepath.Join(rel, "aihub-image.digest")); strings.TrimSpace(string(dg)) != relFakeDigest {
		t.Fatalf("aihub-image.digest = %q, want the canonical %q (unchanged)", dg, relFakeDigest)
	}
}

// TestW86Verify (AC2, Q2): --verify checks the workstation image beside the
// aihub image - the published workstation-image.digest equals the registry's
// index digest, the index carries both platforms, and the public channel's
// image reads anonymously (an empty DOCKER_CONFIG). A missing arm64, a digest
// mismatch, a tampered digest asset and a still-private package each fail it;
// the suffixed (internal) channel makes no anonymous check.
func TestW86Verify(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	if out, err := w.runPinned(nil); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	vout, verr := w.run(nil, "--verify", "v1.2.1")
	if verr != nil {
		t.Fatalf("verify of a fresh release must pass: %v\n%s", verr, vout)
	}
	for _, want := range []string{
		"PASS workstation image index digest " + relFakeDigest + " matches workstation-image.digest",
		"PASS workstation index platform linux/amd64 present",
		"PASS workstation index platform linux/arm64 present",
		"PASS anonymous read of ghcr.io/silgrid/polyforge-workstation:v1.2.1 (empty DOCKER_CONFIG)",
	} {
		if !strings.Contains(vout, want) {
			t.Errorf("verify output lacks %q:\n%s", want, vout)
		}
	}

	// the index missing arm64 fails --verify, naming it
	vout, verr = w.run([]string{"FAKE_WS_NO_ARM64=1"}, "--verify", "v1.2.1")
	if verr == nil || !strings.Contains(vout, "FAIL workstation image index lacks linux/arm64") {
		t.Fatalf("an index without arm64 must fail --verify naming it: %v\n%s", verr, vout)
	}
	if strings.Contains(vout, "FAIL workstation image index lacks linux/amd64") {
		t.Fatalf("amd64 is present; only the arm64 miss may be named:\n%s", vout)
	}

	// a digest the registry does not hold fails it
	vout, verr = w.run([]string{"FAKE_REGISTRY_DIGEST=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, "--verify", "v1.2.1")
	if verr == nil || !strings.Contains(vout, "FAIL workstation image digest mismatch") {
		t.Fatalf("a workstation digest mismatch must fail --verify: %v\n%s", verr, vout)
	}

	// a tampered digest asset fails the checksum check
	relWrite(t, filepath.Join(w.fakeRel, "v1.2.1", "workstation-image.digest"), "tampered\n", 0o644)
	vout, verr = w.run(nil, "--verify", "v1.2.1")
	if verr == nil {
		t.Fatalf("a tampered workstation-image.digest must fail --verify (sha256sum covers it):\n%s", vout)
	}
	if !strings.Contains(vout, "sha256sum") {
		t.Fatalf("the tamper must fail the checksum check:\n%s", vout)
	}

	// the public channel's anonymous read: a package still private fails it
	w2 := newRelWorld(t, []string{"v1.2.0"}, nil)
	if out, err := w2.runPinned(nil); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	vout, verr = w2.run([]string{"FAKE_ANON_UNAUTHORIZED=1"}, "--verify", "v1.2.1")
	if verr == nil || !strings.Contains(vout, "FAIL anonymous read of ghcr.io/silgrid/polyforge-workstation:v1.2.1") {
		t.Fatalf("an unreadable anonymous read must fail --verify naming it: %v\n%s", verr, vout)
	}

	// the suffixed (internal) channel makes no anonymous check: verify passes
	// with the knob set once RELEASE_WORKSTATION_IMAGE names the internal
	// repository (the suffixed line's own rule, u4)
	sw := newRelSuffixWorld(t, relSuffixConf("fork"))
	if out, err := sw.runPinned([]string{"RELEASE_WORKSTATION_IMAGE=ghcr.io/example-org/fork-workstation"}); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	vout, verr = sw.run([]string{"RELEASE_WORKSTATION_IMAGE=ghcr.io/example-org/fork-workstation", "FAKE_ANON_UNAUTHORIZED=1"}, "--verify", "v0.2.0-fork.1")
	if verr != nil {
		t.Fatalf("the internal channel needs no anonymous read: %v\n%s", verr, vout)
	}
	if strings.Contains(vout, "anonymous") {
		t.Fatalf("the suffixed channel must not check anonymous reads:\n%s", vout)
	}
	for _, want := range []string{
		"PASS workstation image index digest " + relFakeDigest + " matches workstation-image.digest",
		"PASS workstation index platform linux/arm64 present",
	} {
		if !strings.Contains(vout, want) {
			t.Errorf("the internal channel's verify output lacks %q:\n%s", want, vout)
		}
	}
}

// TestW86RefusesEitherExistingImageTag (AC3): a fresh release refuses
// before the version is claimed when EITHER image's tag is already in its
// package - the aihub image's or the workstation image's - and never pushes
// over another build's image.
func TestW86RefusesEitherExistingImageTag(t *testing.T) {
	for name, env := range map[string][]string{
		"the aihub image":       {"FAKE_REGISTRY_HAS=v1.2.1"},
		"the workstation image": {"FAKE_REGISTRY_HAS_WS=v1.2.1"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			out, err := w.runPinned(env)
			if err == nil {
				t.Fatalf("a fresh release whose image tag exists must refuse:\n%s", out)
			}
			which := "ghcr.io/silgrid/polyforge-aihub:v1.2.1"
			vark := "RELEASE_IMAGE"
			if name == "the workstation image" {
				which = "ghcr.io/silgrid/polyforge-workstation:v1.2.1"
				vark = "RELEASE_WORKSTATION_IMAGE"
			}
			for _, want := range []string{which + " already exists in the registry", vark, "no release of " + "origin to resume", "nothing built or published, no version claimed"} {
				if !strings.Contains(out, want) {
					t.Errorf("refusal lacks %q:\n%s", want, out)
				}
			}
			if c := w.calls(); len(c) != 0 {
				t.Fatalf("work ran before the refusal: %v", c)
			}
			if got := w.originTags(); strings.Contains(got, "v1.2.1") {
				t.Fatalf("the version was claimed despite the refusal: %q", got)
			}
		})
	}
}

// TestW86ResumePushesPerImage (AC3): a resume keeps the image that is
// already in the registry and publishes only the missing one - either side.
func TestW86ResumePushesPerImage(t *testing.T) {
	t.Run("aihub pushed, the workstation push failed", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		if out, err := w.runPinned([]string{"FAKE_WS_BUILD_FAIL=1"}); err == nil || !strings.Contains(out, "FAILED AFTER v1.2.1 WAS CLAIMED") {
			t.Fatalf("setup: the first run must fail at the workstation image push: %v\n%s", err, out)
		}
		w372ResetCalls(t, w)
		out, err := w.runPinned(nil)
		if err != nil {
			t.Fatalf("the re-run must resume v1.2.1: %v\n%s", err, out)
		}
		calls := w.calls()
		if relIndex(calls, "docker push") >= 0 {
			t.Fatalf("the aihub image is in the registry: the resume must not push it: %v", calls)
		}
		if relIndex(calls, "docker build -f deploy") >= 0 {
			t.Fatalf("the aihub image is in the registry: the resume must not rebuild it: %v", calls)
		}
		if relIndex(calls, "docker buildx build") < 0 {
			t.Fatalf("the workstation image is missing: the resume must build and push it: %v", calls)
		}
		if !strings.Contains(out, "image ghcr.io/silgrid/polyforge-aihub:v1.2.1 is already in the registry ("+relFakeDigest+")") {
			t.Fatalf("the aihub image's keep must be reported:\n%s", out)
		}
		w372AssertResumedOnce(t, w, out)
	})

	t.Run("workstation pushed, the aihub image missing", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		if out, err := w.runPinned(nil); err != nil {
			t.Fatalf("setup release: %v\n%s", err, out)
		}
		// the aihub push never made it: its marker is gone and its digest
		// asset unpublished; the checksums are unpublished alongside
		if err := os.Remove(w.log + ".pushed"); err != nil {
			t.Fatal(err)
		}
		rel := filepath.Join(w.fakeRel, "v1.2.1")
		for _, n := range []string{"SHA256SUMS", "release-manifest.json", "aihub-image.digest"} {
			if err := os.Remove(filepath.Join(rel, n)); err != nil {
				t.Fatal(err)
			}
		}
		w372ResetCalls(t, w)
		out, err := w.runPinned([]string{"FAKE_REGISTRY_MISSING=1"})
		if err != nil {
			t.Fatalf("the resume must complete v1.2.1: %v\n%s", err, out)
		}
		calls := w.calls()
		if relIndex(calls, "docker buildx build") >= 0 {
			t.Fatalf("the workstation image is in the registry: the resume must not rebuild it: %v", calls)
		}
		if relIndex(calls, "docker push") < 0 || relIndex(calls, "docker build -f deploy") < 0 {
			t.Fatalf("the aihub image is missing: the resume must rebuild and push it: %v", calls)
		}
		if !strings.Contains(out, "workstation image ghcr.io/silgrid/polyforge-workstation:v1.2.1 is already in the registry") {
			t.Fatalf("the workstation image's keep must be reported:\n%s", out)
		}
		w372AssertResumedOnce(t, w, out)
	})
}

// TestW86PreflightNeedsBuildxAndArm64 (AC3): a machine without buildx, or
// whose builder does not list linux/arm64, is refused in the preflight -
// before any work and before the version is claimed - with the way out in
// the message.
func TestW86PreflightNeedsBuildxAndArm64(t *testing.T) {
	for name, env := range map[string][]string{
		"no buildx":       {"FAKE_NO_BUILDX=1"},
		"no arm64 binfmt": {"FAKE_NO_ARM64=1"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			out, err := w.runPinned(env)
			if err == nil {
				t.Fatalf("the preflight must refuse: %v\n%s", err, out)
			}
			if !strings.Contains(out, "preflight failed - nothing built or published, no version claimed") {
				t.Fatalf("the refusal must be the preflight's, before the claim:\n%s", out)
			}
			want := "docker buildx is not available"
			way := "install the buildx plugin"
			if name == "no arm64 binfmt" {
				want = "does not list linux/arm64"
				way = "tonistiigi/binfmt --install arm64"
			}
			if !strings.Contains(out, want) || !strings.Contains(out, way) {
				t.Errorf("the preflight problem must name %q and the way out %q:\n%s", want, way, out)
			}
			if c := w.calls(); len(c) != 0 {
				t.Fatalf("work ran before the preflight refusal: %v", c)
			}
			if got := w.originTags(); strings.Contains(got, "v1.2.1") {
				t.Fatalf("the version was claimed despite the preflight refusal: %q", got)
			}
		})
	}
}

// TestW86Channel (AC4): the public channel pushes the workstation image to
// the default public repository under the plain immutable version tag (Q1,
// pinned by TestW86Build's --tag assertion); a suffixed (internal) line
// that does not explicitly set RELEASE_WORKSTATION_IMAGE refuses before any
// work - an internal release must never silently push to the public
// package - and with it set, the image goes to the named internal
// repository under the suffixed tag vX.Y.Z-<name>.N.
func TestW86Channel(t *testing.T) {
	// a suffixed line without the variable refuses before any work
	w := newRelSuffixWorld(t, relSuffixConf("fork"))
	out, err := w.runPinned(nil)
	if err == nil {
		t.Fatalf("a suffixed line without RELEASE_WORKSTATION_IMAGE must refuse:\n%s", out)
	}
	for _, want := range []string{"RELEASE_WORKSTATION_IMAGE", "suffix=fork", "ghcr.io/silgrid/polyforge-workstation", "nothing done"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, out)
		}
	}
	if c := w.calls(); len(c) != 0 {
		t.Fatalf("work ran before the refusal: %v", c)
	}
	if got := w.originTags(); strings.Contains(got, "fork.1") {
		t.Fatalf("the version was claimed despite the refusal: %q", got)
	}

	// with the variable set, the workstation image pushes to the internal
	// repository under the suffixed tag
	w2 := newRelSuffixWorld(t, relSuffixConf("fork"))
	out, err = w2.runPinned([]string{"RELEASE_WORKSTATION_IMAGE=ghcr.io/example-org/fork-workstation"})
	if err != nil {
		t.Fatalf("the internal release must run with the variable set: %v\n%s", err, out)
	}
	var pushLine string
	for _, c := range w2.calls() {
		if strings.HasPrefix(c, "docker buildx build") && strings.Contains(c, " --push") {
			pushLine = c
		}
	}
	if !strings.Contains(pushLine, "--tag ghcr.io/example-org/fork-workstation:v0.2.0-fork.1") {
		t.Fatalf("the push build must carry the internal repository and the suffixed tag:\n%s", pushLine)
	}
	for _, c := range w2.calls() {
		if strings.Contains(c, "polyforge-workstation") {
			t.Fatalf("the internal release must not touch the public package: %v", w2.calls())
		}
	}
	if dg, _ := os.ReadFile(filepath.Join(w2.fakeRel, "v0.2.0-fork.1", "workstation-image.digest")); strings.TrimSpace(string(dg)) != relFakeDigest {
		t.Fatalf("workstation-image.digest = %q, want the registry's index digest", dg)
	}
	if vout, verr := w2.run([]string{"RELEASE_WORKSTATION_IMAGE=ghcr.io/example-org/fork-workstation"}, "--verify", "v0.2.0-fork.1"); verr != nil {
		t.Fatalf("the internal release must verify: %v\n%s", verr, vout)
	}
}

// TestW86ExportAndTerms (AC9, Q8): the image carries the host-side delivery
// (the smoke's own export set, smoke.sh first) under /opt/polyforge/host-files,
// so a user with nothing but Docker reads it straight from the published
// image; the assembled release notes and the workstation documentation each
// carry the Claude Code commercial-terms line (Q9's fourth condition).
func TestW86ExportAndTerms(t *testing.T) {
	// the Containerfile exports exactly the host-side set
	raw, err := os.ReadFile(filepath.Join("..", "dist", "workstation", "Containerfile"))
	if err != nil {
		t.Fatal(err)
	}
	var copyLines []string
	inCopy := false
	for _, l := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(l, "COPY "):
			inCopy = true
			copyLines = []string{l}
		case inCopy:
			copyLines = append(copyLines, l)
		}
		if inCopy && strings.HasSuffix(strings.TrimSpace(l), "/opt/polyforge/host-files/") {
			inCopy = false
		}
	}
	if len(copyLines) == 0 {
		t.Fatal("the Containerfile must COPY the host-side files into /opt/polyforge/host-files/ (the Q8 export entry)")
	}
	have := map[string]bool{}
	for _, l := range copyLines {
		for _, f := range strings.Fields(strings.ReplaceAll(strings.TrimSuffix(l, "\\"), "\\", " ")) {
			if strings.HasPrefix(f, "dist/workstation/") {
				have[strings.TrimPrefix(f, "dist/workstation/")] = true
			}
		}
	}
	for _, want := range []string{"compose.yaml", "seccomp.json", "seccomp.json.sha256", "Caddyfile", "smoke.sh"} {
		if !have[want] {
			t.Errorf("the exported host-side set lacks %s (the smoke's own export set, #168 Q3): %v", want, have)
		}
	}
	if len(have) != 5 {
		t.Errorf("the exported host-side set must be exactly the smoke's five files, got %v", have)
	}

	// the assembled release notes carry the workstation section and the
	// Claude Code commercial-terms line
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	if out, err := w.runPinned(nil); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	body, err := os.ReadFile(filepath.Join(w.fakeRel, "v1.2.1", ".body"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Workstation image",
		"ghcr.io/silgrid/polyforge-workstation:v1.2.1",
		"linux/amd64 and linux/arm64",
		"Claude Code 受 Anthropic 商业条款约束",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the assembled release notes lack %q:\n%s", want, body)
		}
	}

	// the workstation documentation carries the terms line (in English - the
	// page is pinned zero-CJK, w254) and the export command
	doc, err := os.ReadFile(filepath.Join("..", "docs", "workstation.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Claude Code is subject to Anthropic's Commercial Terms of Service",
		"/opt/polyforge/host-files",
	} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("docs/workstation.md lacks %q", want)
		}
	}
}
