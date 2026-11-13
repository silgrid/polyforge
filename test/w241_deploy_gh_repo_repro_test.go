package test

// w241_deploy_gh_repo_repro_test.go: repro for.
//
// Reported failure (#240): the release lane's deploy step runs
// `.polyforge/deploy.sh <TAG> --confirm <TAG>` with no GH_REPO, and the
// script's documented default was still the public mirror silgrid/polyforge,
// so step 0 died with `gh release download <TAG> -R silgrid/polyforge ->
// release not found` (every release asset is published to the internal repo).
// The fix moves the default to the internal release repo; an explicit GH_REPO
// environment value still wins. The projection follows the
// ruling: the internal literal stays under the existing REWRITE row, which maps
// it back to silgrid/polyforge — never under an EXCLUDE/ALLOWED/OVERRIDE row.
//
// TestW241DeployGHRepoDefault is red on the defective tree (the fake gh log
// carries `-R silgrid/polyforge`, and the header names the public default).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// w241InternalRepo is the internal release repo every asset the deployer
	// consumes is published to — the default the script must use.
	w241InternalRepo = "silgrid/polyforge"
	// w241PublicRepo is the public mirror the defective default named.
	w241PublicRepo = "silgrid/polyforge"
)

// w241RunDryRun runs a dry deploy of v2.3.0 with GH_REPO removed from the
// world's environment (the defect's precondition: the release lane passes
// none), then appends extra. HOME is the world's temp dir, so no operator
// deploy.env is read.
func w241RunDryRun(t *testing.T, d *deployWorld, extra ...string) string {
	t.Helper()
	kept := make([]string, 0, len(d.env)+len(extra))
	for _, kv := range d.env {
		if strings.HasPrefix(kv, "GH_REPO=") {
			continue
		}
		kept = append(kept, kv)
	}
	d.env = append(kept, extra...)
	out, err := d.run("", "v2.3.0", "--dry-run", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	return out
}

// w241DownloadRepos returns the -R value of every `gh release download` call
// the fake gh logged.
func w241DownloadRepos(t *testing.T, d *deployWorld) []string {
	t.Helper()
	var repos []string
	for _, ln := range strings.Split(d.logOf("log-gh"), "\n") {
		if !strings.HasPrefix(ln, "gh release download ") {
			continue
		}
		f := strings.Fields(ln)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "-R" {
				repos = append(repos, f[i+1])
			}
		}
	}
	if len(repos) == 0 {
		t.Fatalf("no `gh release download ... -R <repo>` call was logged:\n%s", d.logOf("log-gh"))
	}
	return repos
}

// TestW241DeployGHRepoDefault pins the new default: with GH_REPO unset, the
// release assets are downloaded from the internal repo, never the public
// mirror, and the script header documents the same default.
func TestW241DeployGHRepoDefault(t *testing.T) {
	d := newReadyWorld(t)
	w241RunDryRun(t, d)
	for _, r := range w241DownloadRepos(t, d) {
		if r != w241InternalRepo {
			t.Errorf("with GH_REPO unset the release download must use -R %s, got -R %s:\n%s", w241InternalRepo, r, d.logOf("log-gh"))
		}
	}
	// In the public projection REWRITE maps both constants to the same repo,
	// so this check only means something where they differ.
	if w241PublicRepo != w241InternalRepo && strings.Contains(d.logOf("log-gh"), "-R "+w241PublicRepo) {
		t.Errorf("a gh call still targets the public mirror %s:\n%s", w241PublicRepo, d.logOf("log-gh"))
	}
	src, err := os.ReadFile(filepath.Join("..", ".polyforge", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "#   GH_REPO="+w241InternalRepo+" ") {
		t.Errorf("the deploy.sh header's documented defaults must name GH_REPO=%s", w241InternalRepo)
	}
}

// TestW241DeployGHRepoExplicitOverride pins the preserved ability: an
// explicit GH_REPO still wins over the default.
func TestW241DeployGHRepoExplicitOverride(t *testing.T) {
	const fork = "acme/fork"
	d := newReadyWorld(t)
	w241RunDryRun(t, d, "GH_REPO="+fork, "DEPLOY_REMOTE_IS="+fork) // the deployer repo's remote is the fork
	for _, r := range w241DownloadRepos(t, d) {
		if r != fork {
			t.Errorf("an explicit GH_REPO=%s must win, got -R %s:\n%s", fork, r, d.logOf("log-gh"))
		}
	}
	if strings.Contains(d.logOf("log-gh"), w241InternalRepo) {
		t.Errorf("the default %s leaked past an explicit GH_REPO:\n%s", w241InternalRepo, d.logOf("log-gh"))
	}
}

// TestW241DeployGHRepoStaysOnTheRewritePath pins the projection half: neither
// deploy.sh nor this file may be hidden under EXCLUDE/ALLOWED/OVERRIDE, and
// the REWRITE rows (applied exactly like tools/sync/project.sh — one `sed -E`
// program per row, in order) must map the internal repo to the public one.
func TestW241DeployGHRepoStaysOnTheRewritePath(t *testing.T) {
	root := v1Root(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "sync", "desensitize.md"))
	if os.IsNotExist(err) {
		t.Skip("docs/sync/desensitize.md is internal-only; the public projection carries no sync list")
	}
	if err != nil {
		t.Fatal(err)
	}

	paths := []string{".polyforge/deploy.sh", "test/w241_deploy_gh_repo_repro_test.go"}

	var rewrite []string
	exempt := map[string]string{}
	inSection, inComment := "", false
	for _, line := range strings.Split(string(raw), "\n") {
		if inComment {
			if strings.Contains(line, "-->") {
				inComment = false
			}
			continue
		}
		if strings.Contains(line, "<!--") {
			if !strings.Contains(line, "-->") {
				inComment = true
			}
			continue
		}
		if strings.HasPrefix(line, "## ") {
			fields := strings.Fields(line)
			inSection = ""
			if len(fields) > 1 {
				inSection = fields[1]
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		switch inSection {
		case "REWRITE":
			rewrite = append(rewrite, line)
		case "EXCLUDE", "ALLOWED", "OVERRIDE":
			listed := strings.TrimSpace(strings.SplitN(line, "\t", 2)[0])
			for _, p := range paths {
				if listed == p {
					exempt[p] = inSection
				}
			}
		}
	}
	if len(rewrite) == 0 {
		t.Fatal("docs/sync/desensitize.md's REWRITE section is empty")
	}

	for _, p := range paths {
		if sec, ok := exempt[p]; ok {
			t.Errorf("%s is listed under %s, which hides it from REWRITE and the identifier invariant; %s must stay mapped to %s", p, sec, w241InternalRepo, w241PublicRepo)
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), w241InternalRepo) {
			t.Fatalf("%s does not name %s, so the REWRITE mapping is not exercised", p, w241InternalRepo)
		}
		projected := string(src)
		for _, prog := range rewrite {
			cmd := exec.Command("sed", "-E", prog)
			cmd.Stdin = strings.NewReader(projected)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("applying the REWRITE rule %q to %s failed: %v\n%s", prog, p, err, out)
			}
			projected = string(out)
		}
		if strings.Contains(projected, w241InternalRepo) {
			t.Errorf("the REWRITE rules leave %s in %s", w241InternalRepo, p)
		}
		if !strings.Contains(projected, w241PublicRepo) {
			t.Errorf("the REWRITE rules must map %s to %s in %s", w241InternalRepo, w241PublicRepo, p)
		}
	}
}
