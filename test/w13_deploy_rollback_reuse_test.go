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
// version's local tag only when the fallback build is needed.
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
	for _, bad := range []string{"server docker build", "server docker tag "} {
		if strings.Contains(dl, bad) {
			t.Errorf("an existing rollback image is kept as is - no %q:\n%s", bad, dl)
		}
	}
	if !strings.Contains(out, "deployed=v2.3.0 previous="+old) {
		t.Errorf("output lacks the deployed= summary\n%s", out)
	}
}
