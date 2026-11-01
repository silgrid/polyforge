package test

// deploy.sh's version probes take a version only when the
// word they read has the release-tag shape, fall back to `/aihub -version`
// whenever `/pf aihub -version` did not yield one (for every caller), and,
// when no attempt yields one, quote every attempt's raw stdout in the error.
// The repro is the dual-binary running container whose old /pf prints a
// usage error ending in "directory" to stdout (layout "dual" of the fake
// docker in deploy_image_test.go).

import (
	"strings"
	"testing"
)

// TestW243DualLayoutContainerDeploys: a running container whose /pf answers
// a usage error (non-empty, not a version) is read through /aihub and the
// deploy completes.
func TestW243DualLayoutContainerDeploys(t *testing.T) {
	d := newDeployWorld(t, "v2.2.0")
	d.setRunningLayout("dual")
	d.legacy["v2.2.0"] = true
	d.tagLegacy("v2.2.0")              // its deploy/Dockerfile copies the binary named aihub
	d.release("v2.2.0", false, "", "") // no GHCR image: the rollback image is built from the saved binary
	d.release("v2.3.0", true, digNew, digNew)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	dl := d.logOf("log-docker")
	for _, want := range []string{
		"server docker exec aihub /pf aihub -version",
		"server docker exec aihub /aihub -version",
		"server docker cp aihub:/aihub ",
	} {
		if !strings.Contains(dl, want) {
			t.Errorf("docker log lacks %q\n%s", want, dl)
		}
	}
	for _, want := range []string{
		"container: running, version v2.2.0 (/aihub -version) -> v2.3.0",
		"deployed=v2.3.0 previous=v2.2.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	for _, bad := range []string{"directory", "is not a release tag"} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains %q: the /pf usage error was taken as a version\n%s", bad, out)
		}
	}
}

// TestW243ProbeReadFailureCarriesRawOutput: when neither layout prints a
// release tag, step 0 refuses before any server change and the error names
// both attempts with their raw stdout.
func TestW243ProbeReadFailureCarriesRawOutput(t *testing.T) {
	d := newDeployWorld(t, "v2.2.1")
	d.setRunningLayout("junk")
	d.release("v2.2.1", true, digOld, digOld)
	d.release("v2.3.0", true, digNew, digNew)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err == nil {
		t.Fatalf("deploy must fail when the running version cannot be read\n%s", out)
	}
	for _, want := range []string{
		"FAILED at step 0",
		"could not read the running version",
		`[pf] dk exec aihub /pf aihub -version -> "Usage of pf:   -data-dir directory"`,
		`[aihub] dk exec aihub /aihub -version -> "Usage of aihub:   -data-dir directory"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	for _, bad := range []string{"version directory", `"directory" is not a release tag`} {
		if strings.Contains(out, bad) {
			t.Errorf("output presents the usage error's last word as a version (%q)\n%s", bad, out)
		}
	}
	d.assertNothingMutated("v2.2.1")
}

// TestW243OldAihubLayoutPreflight: a pure pre-consolidation running container
// (only /aihub) is read through the fallback and preflight completes.
func TestW243OldAihubLayoutPreflight(t *testing.T) {
	d := newDeployWorld(t, "v2.2.1")
	d.setRunningLayout("aihub")
	d.release("v2.2.1", true, digOld, digOld)
	d.release("v2.3.0", true, digNew, digNew)
	out, err := d.run("", "v2.3.0", "--dry-run", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"container: running, version v2.2.1 (/aihub -version) -> v2.3.0",
		"dry-run: steps 0-1 ran read-only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	d.assertNothingMutated("v2.2.1")
}
