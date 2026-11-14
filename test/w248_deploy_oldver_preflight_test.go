package test

// w248_deploy_oldver_preflight_test.go: a bug repro.
//
// The legacy rollback image (a running version with no GHCR image) is built
// from the OLD release's deploy/Dockerfile, which names the binary that
// release shipped. The defective deploy.sh read that file from the
// running version's tag and, when the tag was not in the local repo, fell
// back silently to the working tree's consolidated Dockerfile (it copies
// `pf`) and planned a build that cannot work. The fix reads it from the tag
// only and refuses at step 0 when the tag is missing, naming `git fetch
// --tags` — before anything on the server changes.
//
// polyforge#13 adjusted the scenario: a verified running image is now
// REUSED, so the old tag is required only when the fallback build is
// needed - the running image must be unavailable for reuse here. The
// world's FAKE_RUNIMG_VER makes the image probed by ID answer a different
// version than the container's exec does (a docker-cp patched container
// shape), so the fallback build is needed and its step-0 tag requirement
// still applies.
//
// TestW248DeployLegacyRollbackNeedsTheRunningTag is red on the defective
// tree (the deploy marches on with the working-tree Dockerfile).

import (
	"strings"
	"testing"
)

func TestW248DeployLegacyRollbackNeedsTheRunningTag(t *testing.T) {
	for _, dry := range []bool{false, true} {
		name := "deploy"
		if dry {
			name = "dry-run"
		}
		t.Run(name, func(t *testing.T) {
			d := newDeployWorld(t, "v2.2.0")
			d.legacy["v2.2.0"] = true
			d.release("v2.2.0", false, "", "") // no GHCR image: the legacy rollback path
			d.release("v2.3.0", true, digNew, digNew)
			// the running image is unavailable for reuse (its probed version
			// differs from the container's answer), so the fallback build
			// needs the local tag
			d.env = append(d.env, "FAKE_RUNIMG_VER=v9.9.9")
			d.git("tag", "-d", "v2.2.0") // the deployer clone never fetched the running version's tag
			args := []string{"v2.3.0", "--confirm", "v2.3.0"}
			if dry {
				args = append(args, "--dry-run")
			}
			out, err := d.run("", args...)
			if exitCode(err) != 1 {
				t.Fatalf("want exit 1 at step 0, got %v\n%s", err, out)
			}
			for _, want := range []string{"FAILED at step 0", "tag v2.2.0 (the running version) not in the local repo", "git fetch --tags"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q\n%s", want, out)
				}
			}
			d.assertNothingMutated("v2.2.0")
			if !d.lockFree() {
				t.Error("the deploy lock is still held after the step-0 refusal")
			}
		})
	}
}
