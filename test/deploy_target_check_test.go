// carries: .polyforge/deploy.sh
package test

// deploy_target_check_test.go (polyforge#75): a deploy a step process
// started reaches the AIHub of the work item's Cell — the deploy target
// (the merged AIHUB_HOST, its user part dropped, any ssh alias resolved
// LOCALLY through ssh -G) must be that Cell's server (PF_CELL_SERVER), or
// the deploy REFUSES before any network work, both components' entry
// point alike: the same incident that put a public release on the
// internal hub must not repeat. The refusal names both sides. A run
// without the Cell facts (a human running the script by hand) skips the
// check and says so in one line; --preflight reports a mismatch as one of
// its problems, so a release lane fails before a version is claimed.

import (
	"path/filepath"
	"strings"
	"testing"
)

// cellWorld answers a ready deploy world whose step env states the Cell:
// PF_CELL and PF_CELL_SERVER (the Cell facts the daemon injects; the
// world's own env never leaks them in, deploy_image_test.go).
func cellWorld(t *testing.T, cellServer string, extra ...string) *deployWorld {
	t.Helper()
	d := newReadyWorld(t)
	d.env = append(d.env, "PF_CELL=public", "PF_CELL_SERVER="+cellServer)
	d.env = append(d.env, extra...)
	return d
}

func TestDeployCellTargetCheckRefusesMismatch(t *testing.T) {
	for name, args := range map[string][]string{
		"real deploy": {"v2.3.0", "--confirm", "v2.3.0"},
		"dry-run":     {"v2.3.0", "--dry-run"},
	} {
		t.Run(name, func(t *testing.T) {
			d := cellWorld(t, "http://other.example:8080")
			out, err := d.run("", args...)
			if exitCode(err) != 1 || !strings.Contains(out, "REFUSED") {
				t.Fatalf("want exit 1 + REFUSED, got %v\n%s", err, out)
			}
			// the refusal names both sides: the deploy target (fakehost,
			// the world's AIHUB_HOST) and the Cell's server host
			for _, want := range []string{"fakehost", "other.example"} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal must name both sides, missing %q:\n%s", want, out)
				}
			}
			if got := d.logOf("log-ssh") + d.logOf("log-gh") + d.logOf("log-docker"); got != "" {
				t.Errorf("network/tool work happened before the refusal:\n%s", got)
			}
			d.assertNothingMutated("v2.2.1")
		})
	}
}

// TestDeployCellTargetCheckResolvesAlias: the comparison is against the
// ssh alias RESOLVED LOCALLY (ssh -G), so an operator's ssh alias for the
// Cell's server passes the check, and a plain hostname match passes too.
func TestDeployCellTargetCheckResolvesAlias(t *testing.T) {
	for name, host := range map[string]string{
		"plain hostname": "fakehost",
		"ssh alias":      "deploy-alias",
	} {
		t.Run(name, func(t *testing.T) {
			d := cellWorld(t, "http://fakehost:8080",
				"FAKE_SSH_G_HOSTNAME=fakehost", "AIHUB_HOST=root@"+host)
			out, err := d.run("", "v2.3.0", "--dry-run")
			if err != nil || !strings.Contains(out, "dry-run: steps 0-1 ran read-only") {
				t.Fatalf("a matching Cell server must let the dry-run run: %v\n%s", err, out)
			}
			d.assertNothingMutated("v2.2.1")
		})
	}
}

// TestDeployCellTargetCheckPreflight: --preflight runs the same check as
// one reported problem (exit 1 with both sides named, nothing touched); a
// matching target keeps the preflight OK.
func TestDeployCellTargetCheckPreflight(t *testing.T) {
	bad := cellWorld(t, "http://other.example:8080")
	out, err := bad.run("", "--preflight")
	if exitCode(err) != 1 || !strings.Contains(out, "preflight FAILED") ||
		!strings.Contains(out, "fakehost") || !strings.Contains(out, "other.example") {
		t.Fatalf("a mismatched target must fail the preflight naming both sides, got %v\n%s", err, out)
	}
	if got := bad.logOf("log-ssh") + bad.logOf("log-gh") + bad.logOf("log-docker"); got != "" {
		t.Errorf("network/tool work happened before the preflight refusal:\n%s", got)
	}
	bad.assertNothingMutated("v2.2.1")

	good := cellWorld(t, "http://fakehost:8080")
	out, err = good.run("", "--preflight")
	if err != nil || !strings.Contains(out, "preflight OK for component aihub (target fakehost)") {
		t.Fatalf("a matching target must keep the preflight OK, got %v\n%s", err, out)
	}
}

// TestDeployCellTargetCheckFileSourcedTarget (review r1#3): the target a
// step-driven deploy of a named Cell reaches comes from that Cell's own
// deploy configuration - the PF_DEPLOY_ENV file's AIHUB_HOST - while the
// daemon states the server (PF_CELL_SERVER): the check compares THAT
// value (the #60 path: the configuration file carried the wrong host),
// and the environment still wins over the file, so the comparison is on
// the merged value.
func TestDeployCellTargetCheckFileSourcedTarget(t *testing.T) {
	// worldDeployFile answers a ready world whose AIHUB_HOST comes from a
	// PF_DEPLOY_ENV file only (removed from the environment), the rest of
	// the topology staying in the environment.
	worldDeployFile := func(host string) *deployWorld {
		d := newReadyWorld(t)
		d.env = withoutEnv(d.env, "AIHUB_HOST")
		cellEnv := filepath.Join(d.w, "cfg", "deploy-cell.env")
		wf(t, cellEnv, "AIHUB_HOST="+host+"\n", 0o600)
		d.env = append(d.env, "PF_DEPLOY_ENV="+cellEnv, "PF_CELL=public")
		return d
	}
	t.Run("file-sourced mismatch refuses", func(t *testing.T) {
		for name, args := range map[string][]string{
			"real deploy": {"v2.3.0", "--confirm", "v2.3.0"},
			"dry-run":     {"v2.3.0", "--dry-run"},
		} {
			d := worldDeployFile("root@fakehost")
			d.env = append(d.env, "PF_CELL_SERVER=http://other.example:8080")
			out, err := d.run("", args...)
			if exitCode(err) != 1 || !strings.Contains(out, "REFUSED") {
				t.Fatalf("%s: want exit 1 + REFUSED, got %v\n%s", name, err, out)
			}
			for _, want := range []string{"fakehost", "other.example"} {
				if !strings.Contains(out, want) {
					t.Errorf("%s: the refusal must name both sides, missing %q:\n%s", name, want, out)
				}
			}
			if got := d.logOf("log-ssh") + d.logOf("log-gh") + d.logOf("log-docker"); got != "" {
				t.Errorf("%s: network/tool work happened before the refusal:\n%s", name, got)
			}
			d.assertNothingMutated("v2.2.1")
		}
	})
	t.Run("file-sourced match lets the dry-run run", func(t *testing.T) {
		d := worldDeployFile("root@fakehost")
		d.env = append(d.env, "PF_CELL_SERVER=http://fakehost:8080")
		out, err := d.run("", "v2.3.0", "--dry-run")
		if err != nil || !strings.Contains(out, "dry-run: steps 0-1 ran read-only") {
			t.Fatalf("a matching Cell file must let the dry-run run: %v\n%s", err, out)
		}
		d.assertNothingMutated("v2.2.1")
	})
	t.Run("the environment still wins over the file", func(t *testing.T) {
		d := worldDeployFile("root@fakehost")
		d.env = append(d.env, "PF_CELL_SERVER=http://fakehost:8080", "AIHUB_HOST=root@mismatch.example")
		out, err := d.run("", "v2.3.0", "--dry-run")
		if exitCode(err) != 1 || !strings.Contains(out, "REFUSED") || !strings.Contains(out, "mismatch.example") {
			t.Fatalf("the merged AIHUB_HOST (the environment's) is what the check compares, got %v\n%s", err, out)
		}
		if !strings.Contains(out, "fakehost") {
			t.Errorf("the refusal names the Cell's server side too:\n%s", out)
		}
		d.assertNothingMutated("v2.2.1")
	})
}

// TestDeployCellTargetCheckSkippedWithoutCell: without the Cell facts (a
// human running the script by hand) the check is skipped and the log says
// so in one line - the manual behavior is unchanged.
func TestDeployCellTargetCheckSkippedWithoutCell(t *testing.T) {
	for name, args := range map[string][]string{
		"dry-run":   {"v2.3.0", "--dry-run"},
		"preflight": {"--preflight"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newReadyWorld(t)
			out, err := d.run("", args...)
			if err != nil {
				t.Fatalf("a manual run must be unaffected: %v\n%s", err, out)
			}
			if !strings.Contains(out, "target check: skipped (no Cell stated)") {
				t.Errorf("a manual run must say the check was skipped:\n%s", out)
			}
		})
	}
}
