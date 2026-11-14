package test

// w13_deploy_rollback_reuse_test.go: deploy.sh's rollback image for a
// running version without a GHCR image (polyforge#13).
//
// The old path always built the rollback image on the server, naming the
// saved old binary `aihub` in the build context - while every release since
// the consolidation ships a deploy/Dockerfile whose COPY source is `pf`, so
// the build failed with "file not found in build context: pf". The running
// container's image is the rollback image that is actually wanted: it is
// reused (docker tag <image ID> polyforge/aihub:<old>) when its probed
// version equals the running version, and only when that check fails does
// the legacy fallback build run. The step-0 preflight asks for the old
// version's local tag only when the fallback build is needed. The
// fallback build names the context file after the old release's Dockerfile
// (its unique COPY source), never a hardcoded name, and an image whose
// probed version differs from the running version is never tagged.
//
// carries: .polyforge/deploy.sh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestW13ReuseRunningImageWhenNoGHCR: the reported defect, end to end - a
// server running v0.2.0-candidate.40c886f0 (pf layout, no GHCR image for
// that release) deploys v0.2.0 successfully, reusing the running image.
// Red on the unfixed tree: the legacy build names the saved binary `aihub`
// while the old release's Dockerfile copies `pf` - "file not found in build
// context: pf".
func TestW13ReuseRunningImageWhenNoGHCR(t *testing.T) {
	const old = "v0.2.0-candidate.40c886f0"
	d := newDeployWorld(t, old)
	d.setRunningLayout("pf")      // the running image is consolidated (only /pf)
	d.release(old, false, "", "") // the running version has no GHCR image
	d.release("v0.2.0", true, digNew, digNew)
	out, err := d.run("", "v0.2.0", "--confirm", "v0.2.0")
	if err != nil {
		t.Fatalf("deploy of v0.2.0 over a running %s without a GHCR image failed:\n%v\n%s", old, err, out)
	}
	for _, want := range []string{
		"deployed=v0.2.0 previous=" + old,
		"rollback image: " + old + " has no GHCR image",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	dl := d.logOf("log-docker")
	rid := "sha256:old" + old
	// the reuse key is the running container's image ID ({{.Image}}), never
	// a tag name, and the found image is tagged only after verifying
	if !strings.Contains(dl, "server docker inspect -f {{.Image}} aihub") {
		t.Errorf("the running container's image ID was never read with {{.Image}}\n%s", dl)
	}
	if !strings.Contains(dl, "server docker tag "+rid+" polyforge/aihub:"+old) {
		t.Errorf("the running image must be reused by tagging its ID %s:\n%s", rid, dl)
	}
	if strings.Contains(dl, "server docker build") {
		t.Errorf("no image may be built on the server when the running image verifies:\n%s", dl)
	}
	if !strings.Contains(out, "reused the running container's image "+rid) {
		t.Errorf("output lacks the reuse note naming the image ID\n%s", out)
	}
	// the rollback image IS the running image: same ID, same version
	rb, rerr := os.ReadFile(filepath.Join(d.w, "dk-server", "img", "polyforge_aihub_"+old))
	if rerr != nil || !strings.HasPrefix(string(rb), rid+"\n") {
		t.Errorf("rollback image polyforge/aihub:%s is not the reused running image %s: %q %v", old, rid, rb, rerr)
	}
}

// TestW13ReuseNeedsNoLocalOldTag: when the running image verifies, the
// deployer needs no local tag of the running version - the fallback build
// (the only consumer of the old tag's deploy/Dockerfile) never runs. The
// unfixed tree refuses this deploy at step 0.
func TestW13ReuseNeedsNoLocalOldTag(t *testing.T) {
	const old = "v2.2.0"
	d := newDeployWorld(t, old)
	d.setRunningLayout("pf")
	d.release(old, false, "", "") // no GHCR image for the running version
	d.release("v2.3.0", true, digNew, digNew)
	d.git("tag", "-d", old) // the deployer clone never fetched the running version's tag
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("a verified running image is enough for the rollback - the local tag of %s must not be required:\n%s\n%s", old, err, out)
	}
	for _, bad := range []string{"not in the local repo", "git fetch --tags"} {
		if strings.Contains(out, bad) {
			t.Errorf("output still demands the old version's local tag (%q)\n%s", bad, out)
		}
	}
	dl := d.logOf("log-docker")
	if !strings.Contains(dl, "server docker tag sha256:old"+old+" polyforge/aihub:"+old) {
		t.Errorf("the running image must be reused by ID:\n%s", dl)
	}
	if strings.Contains(dl, "server docker build") {
		t.Errorf("nothing may be built when the running image verifies:\n%s", dl)
	}
	if !strings.Contains(out, "deployed=v2.3.0 previous="+old) {
		t.Errorf("output lacks the deployed= summary\n%s", out)
	}
}

// TestW13ExistingRollbackImagePrintsBothIDs: a rollback image that already
// exists on the server while its image ID differs from the running
// container's is kept - both IDs are printed, the existing tag's version is
// still verified per the existing rules - and nothing is built or re-tagged.
func TestW13ExistingRollbackImagePrintsBothIDs(t *testing.T) {
	const old = "v2.2.1"
	const exid = "sha256:alreadyexistingrollbackimageid000000000000000000000"
	d := newDeployWorld(t, old)
	d.setRunningLayout("pf")
	d.release(old, false, "", "") // no GHCR image for the running version
	d.release("v2.3.0", true, digNew, digNew)
	// the server already holds polyforge/aihub:<old>: a DIFFERENT image than
	// the running container's
	wf(t, filepath.Join(d.w, "dk-server", "img", "polyforge_aihub_"+old), exid+"\n"+old+"\n\npf\n", 0o644)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy failed:\n%s\n%s", err, out)
	}
	if !strings.Contains(out, exid) || !strings.Contains(out, "sha256:old"+old) {
		t.Errorf("both image IDs (the existing rollback image's %s and the running container's %s) must be printed:\n%s", exid, "sha256:old"+old, out)
	}
	// the existing tag still wins and is verified per the existing rules
	dl := d.logOf("log-docker")
	if !strings.Contains(dl, "server docker run --rm --entrypoint /pf polyforge/aihub:"+old+" aihub -version") {
		t.Errorf("the existing rollback image's version must still be verified:\n%s", dl)
	}
	for _, bad := range []string{"server docker build", "server docker tag sha256:"} {
		if strings.Contains(dl, bad) {
			t.Errorf("an existing rollback image is kept as is - the running image's ID must never be tagged (%q):\n%s", bad, dl)
		}
	}
	if !strings.Contains(out, "deployed=v2.3.0 previous="+old) {
		t.Errorf("output lacks the deployed= summary\n%s", out)
	}
}

// tagWithDockerfile tags tag in the deployer repo on a commit whose
// deploy/Dockerfile is body, then restores the current file on main (the
// same dance as tagLegacy: a tag must be read from the tree it names).
func (d *deployWorld) tagWithDockerfile(tag, body string) {
	df := filepath.Join(d.repo, "deploy", "Dockerfile")
	cur, err := os.ReadFile(df)
	if err != nil {
		d.t.Fatal(err)
	}
	wf(d.t, df, body, 0o644)
	d.git("commit", "-q", "-am", "dockerfile "+tag)
	d.git("tag", tag)
	wf(d.t, df, string(cur), 0o644)
	d.git("commit", "-q", "-am", "restore")
}

// TestW13FallbackBuildUsesTheOldDockerfilesCopyName: when the running image
// does not verify, the fallback build names the context file after the old
// release's Dockerfile - its unique COPY source - so both layouts build.
func TestW13FallbackBuildUsesTheOldDockerfilesCopyName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		old    string
		layout string // the layout the built image must carry
		legacy bool
		target string
	}{
		// consolidated old release: its Dockerfile copies `pf`
		{"pf-dockerfile", "v0.2.0-candidate.40c886f0", "pf", false, "v0.2.0"},
		// pre-consolidation old release: its Dockerfile copies `aihub`
		{"aihub-dockerfile", "v2.2.0", "aihub", true, "v2.3.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newDeployWorld(t, tc.old)
			d.legacy[tc.old] = tc.legacy
			if tc.legacy {
				d.setRunningLayout("aihub")
				d.tagLegacy(tc.old)
			} else {
				d.setRunningLayout("pf")
				d.git("tag", tc.old) // its deploy/Dockerfile is the working tree's (COPY pf)
			}
			d.release(tc.old, false, "", "") // no GHCR image for the running version
			d.release(tc.target, true, digNew, digNew)
			// the running image does not verify - the fallback build runs
			d.env = append(d.env, "FAKE_RUNIMG_VER=v9.9.9")
			out, err := d.run("", tc.target, "--confirm", tc.target)
			if err != nil {
				t.Fatalf("the fallback build must name its context file after the Dockerfile's COPY source %q:\n%v\n%s", tc.layout, err, out)
			}
			dl := d.logOf("log-docker")
			if !strings.Contains(dl, "server docker build -t polyforge/aihub:"+tc.old) {
				t.Errorf("the fallback image must be built on the server:\n%s", dl)
			}
			if !strings.Contains(out, "image polyforge/aihub:"+tc.old+" built; -version ("+tc.layout+" layout) = "+tc.old) {
				t.Errorf("the built image must verify through its %q layout:\n%s", tc.layout, out)
			}
			b, rerr := os.ReadFile(filepath.Join(d.w, "dk-server", "img", "polyforge_aihub_"+tc.old))
			if rerr != nil || !strings.HasSuffix(string(b), "\n"+tc.layout+"\n") {
				t.Errorf("the built image is not the %q layout: %q %v", tc.layout, b, rerr)
			}
			if !strings.Contains(out, "deployed="+tc.target+" previous="+tc.old) {
				t.Errorf("output lacks the deployed= summary\n%s", out)
			}
		})
	}
}

// TestW13FallbackRefusesDockerfileWithoutUniqueCopy: an old Dockerfile with
// no unique COPY source cannot name the build context file - the deploy
// refuses (exit 1) instead of guessing, and nothing on the server changes.
func TestW13FallbackRefusesDockerfileWithoutUniqueCopy(t *testing.T) {
	for name, body := range map[string]string{
		"no-copy":    "FROM debian:bookworm-slim\nENTRYPOINT [\"/pf\", \"aihub\"]\n",
		"two-copies": "FROM debian:bookworm-slim\nCOPY pf /pf\nCOPY aihub /aihub\n",
	} {
		t.Run(name, func(t *testing.T) {
			d := newDeployWorld(t, "v2.2.0")
			d.setRunningLayout("aihub")
			d.tagWithDockerfile("v2.2.0", body)
			d.release("v2.2.0", false, "", "") // no GHCR image: the legacy rollback path
			d.release("v2.3.0", true, digNew, digNew)
			d.env = append(d.env, "FAKE_RUNIMG_VER=v9.9.9")
			out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
			if exitCode(err) != 1 {
				t.Fatalf("want exit 1, got %v\n%s", err, out)
			}
			if !strings.Contains(out, "no unique COPY source") {
				t.Errorf("the refusal must name the missing unique COPY source:\n%s", out)
			}
			// the refusal is at step 2, so the lock and the saved binary are
			// legitimate; what must NOT have happened: any server docker
			// mutation, any compose edit, a lock left behind
			if m := d.serverDockerMutations(); len(m) != 0 {
				t.Errorf("server docker was changed: %v", m)
			}
			if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.2.0") {
				t.Errorf("compose file changed:\n%s", d.composeText())
			}
			if !d.lockFree() {
				t.Error("the deploy lock is still held after the refusal")
			}
		})
	}
}

// TestW13FallbackHasNoHardcodedContextName: the fallback build's context
// file name comes from the old Dockerfile, not from the script - the
// hardcoded `ctx/aihub` that broke the consolidated releases is gone, and
// the fallback states when it is to be deleted (the repo's compat rule).
func TestW13FallbackHasNoHardcodedContextName(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", ".polyforge", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "ctx/aihub") {
		t.Errorf("deploy.sh still hardcodes the legacy build's context file name ctx/aihub")
	}
	for _, want := range []string{
		"unique COPY source",
		"Delete this function",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the legacy fallback build must state %q (its deletion condition)", want)
		}
	}
}

// TestW13MismatchedRunningImageIsNeverTagged: an image whose probed version
// is not the running version is never tagged as the rollback image - the
// deploy falls back to the build instead.
func TestW13MismatchedRunningImageIsNeverTagged(t *testing.T) {
	const old = "v0.2.0-candidate.40c886f0"
	d := newDeployWorld(t, old)
	d.setRunningLayout("pf")
	d.git("tag", old) // its deploy/Dockerfile copies pf - the fallback build can run
	d.release(old, false, "", "")
	d.release("v0.2.0", true, digNew, digNew)
	d.env = append(d.env, "FAKE_RUNIMG_VER=v9.9.9") // the image by ID answers v9.9.9
	out, err := d.run("", "v0.2.0", "--confirm", "v0.2.0")
	if err != nil {
		t.Fatalf("a mismatched running image must fall back to the build, not fail:\n%v\n%s", err, out)
	}
	dl := d.logOf("log-docker")
	if strings.Contains(dl, "server docker tag sha256:") {
		t.Errorf("the running image's ID must never be tagged when its version does not verify:\n%s", dl)
	}
	if !strings.Contains(dl, "server docker build -t polyforge/aihub:"+old) {
		t.Errorf("the deploy must fall back to the legacy build:\n%s", dl)
	}
	if !strings.Contains(out, "does not verify as "+old) {
		t.Errorf("the output must say why the running image was not reused:\n%s", out)
	}
	if !strings.Contains(out, "deployed=v0.2.0 previous="+old) {
		t.Errorf("output lacks the deployed= summary\n%s", out)
	}
}
