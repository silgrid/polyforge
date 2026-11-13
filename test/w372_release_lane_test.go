package test

// w372_release_lane_test.go: one regression test per release-lane failure
// class (17 failed_gate + 1 rollback since 2026-10-29; the
// classification table is in docs/reference/release.md). Every test runs the
// real .polyforge/release.sh / deploy.sh against the fake environments of
// release_script_test.go and deploy_image_test.go: no network, no registry,
// no GitHub, nothing is ever published.
//
// Run (targeted): GOWORK=off go test -count=1 -run '^TestW372' ./test/

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w372RepoLog is every repository the fake gh was pointed at (-R or an
// "api repos/<owner>/<repo>/..." path), in call order.
func w372RepoLog(w *relWorld) []string {
	b, _ := os.ReadFile(filepath.Join(w.fakeRel, "repos.log"))
	return strings.Fields(string(b))
}

// w372NameOrigin gives the clone's origin a GitHub identity while every git
// transport still reaches the local bare repo: the configured URL is the
// GitHub one (what release.sh judges), url.<local>.insteadOf rewrites it for
// fetch/push/ls-remote.
func w372NameOrigin(t *testing.T, w *relWorld, githubURL string) {
	t.Helper()
	relGit(t, w.work, "config", "url."+w.origin+".insteadOf", githubURL)
	relGit(t, w.work, "remote", "set-url", "origin", githubURL)
	if got := relGit(t, w.work, "config", "--get", "remote.origin.url"); got != githubURL {
		t.Fatalf("remote.origin.url = %q, want %q", got, githubURL)
	}
}

// ---- class C: wrong default repository (#120 release, #240 deploy, #155's
// first invocation). The default is the internal repo; a tag/release split
// is refused before any work. The other repository is the neutral
// example-org/polyforge, never a real repository name: every copy of these
// tests must keep the two repositories distinct.

// TestW372ReleaseRefusesATagReleaseRepoSplit: #120 pushed the tag to the
// internal repo (origin) and created the release in another one; the release
// failed after the tag push and burned v2.20.10. A GH repo that differs from
// origin's GitHub repo now refuses before anything runs - in release mode and
// in --verify - while the matching repo (any letter case) releases normally.
func TestW372ReleaseRefusesATagReleaseRepoSplit(t *testing.T) {
	for name, env := range map[string][]string{
		"default gh repo":  nil,
		"explicit gh repo": {"RELEASE_GH_REPO=silgrid/polyforge"},
	} {
		t.Run("split refused: "+name, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			w372NameOrigin(t, w, "git@github.com:example-org/polyforge.git")
			var out string
			var err error
			if env == nil {
				out, err = w121RunWithoutGHRepo(t, w, nil)
			} else {
				out, err = w.run(env)
			}
			if err == nil {
				t.Fatalf("a release whose tag goes to example-org/polyforge and whose release goes to silgrid/polyforge must refuse:\n%s", out)
			}
			for _, want := range []string{"example-org/polyforge", "silgrid/polyforge", "a release must live where its tag is", "nothing done"} {
				if !strings.Contains(out, want) {
					t.Errorf("refusal lacks %q:\n%s", want, out)
				}
			}
			if c := w.calls(); len(c) != 0 {
				t.Fatalf("work ran before the split refusal: %v", c)
			}
			if r := w372RepoLog(w); len(r) != 0 {
				t.Fatalf("gh was called before the split refusal: %v", r)
			}
			if got := w.originTags(); strings.Contains(got, "v1.2.1") {
				t.Fatalf("a tag was pushed despite the refusal: %q", got)
			}
		})
	}

	t.Run("split refused in --verify", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		w372NameOrigin(t, w, "https://github.com/example-org/polyforge")
		out, err := w.run(nil, "--verify", "v1.2.0")
		if err == nil || !strings.Contains(out, "a release must live where its tag is") {
			t.Fatalf("--verify must refuse a tag/release split: %v\n%s", err, out)
		}
	})

	t.Run("matching repo releases (case-insensitive)", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		w372NameOrigin(t, w, "https://github.com/acme/WIDGET.git/")
		out, err := w.run([]string{"RELEASE_GH_REPO=Acme/widget"})
		if err != nil {
			t.Fatalf("a gh repo naming origin's GitHub repo must release: %v\n%s", err, out)
		}
		if !strings.Contains(out, "version=v1.2.1\n") {
			t.Fatalf("no v1.2.1:\n%s", out)
		}
	})
}

// ---- class E: PR-only gate logic (#118 genuine owner-direct commits, the
// gate was right; #340 an index-blind lookup, fixed by 5c7ab5b3). What was
// left: a failed GitHub read was reported as "commits entered main without a
// merged PR", the same words as a commit that bypassed review.

// w372MergeAsPR lands file on main the way GitHub's merge button does: a
// topic commit merged --no-ff with GitHub's merge subject naming PR #n. The
// merge commit is NOT put in the fake merged-PR list: the list lags.
func w372MergeAsPR(t *testing.T, w *relWorld, n, file string) string {
	t.Helper()
	topic := "topic-" + file
	relGit(t, w.work, "checkout", "-q", "-b", topic)
	relWrite(t, filepath.Join(w.work, file), file+"\n", 0o644)
	relGit(t, w.work, "add", "-A")
	relGit(t, w.work, "commit", "-q", "-m", "change "+file)
	relGit(t, w.work, "checkout", "-q", "main")
	relGit(t, w.work, "merge", "-q", "--no-ff", "-m", "Merge pull request #"+n+" from acme/"+topic, topic)
	relGit(t, w.work, "branch", "-q", "-D", topic)
	relGit(t, w.work, "push", "-q", "origin", "main")
	return relGit(t, w.work, "rev-parse", "HEAD")
}

// w372SetPull records PR #n in the fake: "merged <sha>" or "unmerged <sha>".
func w372SetPull(t *testing.T, w *relWorld, n, line string) {
	t.Helper()
	relWrite(t, filepath.Join(w.fakeRel, "pull-"+n), line+"\n", 0o644)
}

// TestW372PROnlyLookupFailureIsRetryableNotAMissingPR: a GitHub read that
// fails (the merged-PR list, or the one PR a merge subject names) refuses
// before anything is claimed, says "PR lookup failed ... retryable", carries
// gh's error, and never says "without a merged PR". Red on origin/main, which
// reported a 502 as "commits entered main since v1.2.0 without a merged PR".
func TestW372PROnlyLookupFailureIsRetryableNotAMissingPR(t *testing.T) {
	check := func(t *testing.T, w *relWorld, out string, err error, sha string) {
		t.Helper()
		if err == nil {
			t.Fatalf("a failed PR lookup must refuse (fail closed):\n%s", out)
		}
		for _, want := range []string{"PR lookup failed", "retryable", "not a missing PR", "HTTP 502", sha, "nothing built or published", "no version claimed"} {
			if !strings.Contains(out, want) {
				t.Errorf("refusal lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "without a merged PR") {
			t.Errorf("a failed lookup is reported as a missing PR:\n%s", out)
		}
		if c := w.calls(); len(c) != 0 {
			t.Fatalf("work ran despite the refusal: %v", c)
		}
		if got := w.originTags(); strings.Contains(got, "v1.2.1") {
			t.Fatalf("a version was claimed: %q", got)
		}
	}
	t.Run("the merged-PR list read fails", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		head := relGit(t, w.work, "rev-parse", "HEAD")
		out, err := w.run([]string{"FAKE_GH_API_FAIL=1"})
		check(t, w, out, err, head)
	})
	t.Run("the merge subject's own PR read fails", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		sha := w372MergeAsPR(t, w, "42", "lagging")
		w372SetPull(t, w, "42", "merged "+sha)
		out, err := w.run([]string{"FAKE_GH_PULL_FAIL=1"})
		check(t, w, out, err, sha)
	})
	t.Run("--verify reports the failed lookup as such", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		relGit(t, w.work, "tag", "-a", "-m", "v1.3.0", "v1.3.0")
		relGit(t, w.work, "push", "-q", "origin", "v1.3.0")
		w.publishFake("v1.3.0", w.goodRelease("v1.3.0"))
		out, err := w.run([]string{"FAKE_GH_API_FAIL=1"}, "--verify", "v1.3.0")
		if err == nil {
			t.Fatalf("--verify must fail a failed lookup:\n%s", out)
		}
		ok := false
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "FAIL pr-only") && strings.Contains(l, "PR lookup failed") && strings.Contains(l, "retryable") && !strings.Contains(l, "without a merged PR") {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("want a FAIL pr-only line naming the failed lookup, not a missing PR:\n%s", out)
		}
	})
}

// TestW372PROnlyConfirmsAMergeCommitThroughItsOwnPR: a first-parent merge
// commit the merged-PR list does not (yet) carry, whose subject is GitHub's
// "Merge pull request #<n> from ...", passes exactly when PR #n is merged with
// that very merge_commit_sha. An unmerged PR, another sha, or no such PR (404)
// is still a refusal - and a missing PR, not a lookup failure. The passing
// case is red on origin/main (it read the list only).
func TestW372PROnlyConfirmsAMergeCommitThroughItsOwnPR(t *testing.T) {
	t.Run("merged with this sha passes", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		sha := w372MergeAsPR(t, w, "42", "lagging")
		w372SetPull(t, w, "42", "merged "+sha)
		out, err := w.run(nil)
		if err != nil || !strings.Contains(out, "version=v1.2.1\n") {
			t.Fatalf("PR #42 is merged with merge_commit_sha %s: the release must pass: %v\n%s", sha, err, out)
		}
	})
	for name, pull := range map[string]func(sha string) string{
		"unmerged PR":      func(sha string) string { return "unmerged " + sha },
		"another sha":      func(string) string { return "merged 0123456789012345678901234567890123456789" },
		"no such PR (404)": nil,
	} {
		t.Run(name+" refuses", func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			sha := w372MergeAsPR(t, w, "42", "lagging")
			if pull != nil {
				w372SetPull(t, w, "42", pull(sha))
			}
			out, err := w.run(nil)
			if err == nil {
				t.Fatalf("expected a refusal:\n%s", out)
			}
			if !strings.Contains(out, "without a merged PR") || !strings.Contains(out, sha) {
				t.Fatalf("want a missing-PR refusal naming %s:\n%s", sha, out)
			}
			if strings.Contains(out, "PR lookup failed") {
				t.Fatalf("a definite answer is reported as a failed lookup:\n%s", out)
			}
			if got := w.originTags(); strings.Contains(got, "v1.2.1") {
				t.Fatalf("a version was claimed: %q", got)
			}
		})
	}
}

// ---- class D: a machine prerequisite found in the middle of the run (#144
// node major in the test gate, #265 a stored ghcr.io login that could not
// push - found only after the tag push, #283 ssh BatchMode to the aihub host -
// found at the deploy step, after the release was published and audited).

// w372EmptyDockerConfig points DOCKER_CONFIG at a config without a ghcr.io
// login.
func w372EmptyDockerConfig(t *testing.T, w *relWorld) string {
	t.Helper()
	dir := filepath.Join(w.root, "emptycfg")
	relWrite(t, filepath.Join(dir, "config.json"), `{}`, 0o644)
	return "DOCKER_CONFIG=" + dir
}

// TestW372ReleasePreflightReportsEveryMissingPrerequisiteFirst: a node of the
// wrong major and no usable ghcr.io push credential are BOTH reported, in one
// message, before any gate, build, claim or publish. Red on origin/main,
// which ignored .nvmrc in its own steps and logged in through gh without
// looking at the token's scopes, then released.
func TestW372ReleasePreflightReportsEveryMissingPrerequisiteFirst(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	w.mergeOnMain(".nvmrc", "999\n", "pin node 999", true)
	out, err := w.run([]string{w372EmptyDockerConfig(t, w), "FAKE_GH_SCOPES=gist, read:org, repo"})
	if err == nil {
		t.Fatalf("a release with a wrong node major and no ghcr.io push credential must refuse:\n%s", out)
	}
	for _, want := range []string{
		"preflight failed", "nothing built or published, no version claimed",
		"is not major 999", ".nvmrc",
		"no ghcr.io push credential", "write:packages", "gh auth refresh -h github.com -s write:packages",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preflight message lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "release: preflight failed"); n != 1 {
		t.Errorf("want every problem in ONE message, got %d preflight messages:\n%s", n, out)
	}
	if c := w.calls(); len(c) != 0 {
		t.Fatalf("work ran before the preflight refusal: %v", c)
	}
	if got := w.originTags(); strings.Contains(got, "v1.2.1") {
		t.Fatalf("a version was claimed: %q", got)
	}
}

// TestW372ReleasePreflightRefreshesAStoredGhcrLogin: #265's machine had a
// ghcr.io login stored that could not push, and the old script skipped its
// login. A stored login that cannot even read the image's repository is
// replaced with gh's token (write:packages) in the preflight - before the
// test gate and the tag push; a stored login that reads is kept and the run
// says it cannot prove push (it never replaces a working credential just
// because gh could). Red on origin/main (no login at all with a stored entry).
func TestW372ReleasePreflightRefreshesAStoredGhcrLogin(t *testing.T) {
	t.Run("a stored login that cannot read is replaced before the gates", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		out, err := w.run([]string{"FAKE_GH_SCOPES=gist, read:org, repo, write:packages", "FAKE_REGISTRY_NEEDS_LOGIN=1"})
		if err != nil {
			t.Fatalf("release: %v\n%s", err, out)
		}
		calls := w.calls()
		login, tests, push := relIndex(calls, "docker login"), relIndex(calls, "tests"), relIndex(calls, "git push")
		if login < 0 || tests < 0 || push < 0 || login > tests || login > push {
			t.Fatalf("want docker login (gh's token) before the test gate and the tag push: %v", calls)
		}
		if !strings.Contains(out, "replacing the stored ghcr.io login, which cannot read ghcr.io/silgrid/aihub:v1.2.0") {
			t.Fatalf("the replacement must say why:\n%s", out)
		}
	})
	t.Run("a stored login that reads is kept", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		out, err := w.run([]string{"FAKE_GH_SCOPES=gist, read:org, repo, write:packages"})
		if err != nil {
			t.Fatalf("release: %v\n%s", err, out)
		}
		if relIndex(w.calls(), "docker login") >= 0 {
			t.Fatalf("a stored login that reads must not be replaced: %v", w.calls())
		}
		if !strings.Contains(out, "keeping the ghcr.io login stored") || !strings.Contains(out, "cannot prove it can push") {
			t.Fatalf("the run must say it keeps an unproven login:\n%s", out)
		}
	})
	t.Run("a stored login that cannot read, gh without write:packages", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		out, err := w.run([]string{"FAKE_GH_SCOPES=repo", "FAKE_REGISTRY_NEEDS_LOGIN=1"})
		if err == nil || !strings.Contains(out, "cannot read ghcr.io/silgrid/aihub:v1.2.0") || !strings.Contains(out, "preflight failed") {
			t.Fatalf("want a preflight problem: %v\n%s", err, out)
		}
		if c := w.calls(); len(c) != 0 {
			t.Fatalf("work ran before the preflight refusal: %v", c)
		}
	})
}

// TestW372ReleasePreflightPassesAReadyMachine: the positive controls - the
// running node's own major in .nvmrc passes, and a token without
// write:packages next to a stored ghcr.io login that reads releases, keeping
// that login and saying it cannot prove push (a push failure after the claim
// is resumable, see class A/B).
func TestW372ReleasePreflightPassesAReadyMachine(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	v, err := exec.Command(nodeBin, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	major := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(string(v)), "v"), ".", 2)[0]
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	w.mergeOnMain(".nvmrc", major+"\n", "pin node", true)
	out, err := w.run([]string{"FAKE_GH_SCOPES=repo"})
	if err != nil {
		t.Fatalf("a ready machine must release: %v\n%s", err, out)
	}
	for _, want := range []string{"release: preflight passed", "keeping the ghcr.io login stored", "version=v1.2.1\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestW372DeployPreflightCatchesSSHBatchModeBeforeARelease: deploy.sh
// --preflight is a read-only check the release step runs before the release
// procedure. #283's "ssh ... failed (BatchMode)" - together with any other
// problem - is reported in one message with nothing touched. Red on
// origin/main: --preflight was an unknown flag (exit 2).
func TestW372DeployPreflightCatchesSSHBatchModeBeforeARelease(t *testing.T) {
	t.Run("every problem in one message", func(t *testing.T) {
		d := newDeployWorld(t, "v2.2.1")
		d.env = append(d.env, "FAKE_SSH_DENY=1", "FAKE_GH_LOGGED_OUT=1", "COMPOSE_PROJECT=")
		out, err := d.run("", "--preflight")
		if exitCode(err) != 1 {
			t.Fatalf("want exit 1, got %v\n%s", err, out)
		}
		for _, want := range []string{
			"preflight FAILED", "nothing was touched",
			"ssh to fakehost failed (BatchMode)", "Permission denied (publickey)",
			"gh is not logged in", "required variable(s) unset: COMPOSE_PROJECT",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("preflight lacks %q:\n%s", want, out)
			}
		}
		d.assertNothingMutated("v2.2.1")
		if strings.Contains(d.logOf("log-gh"), "release download") {
			t.Fatalf("the preflight downloaded a release:\n%s", d.logOf("log-gh"))
		}
	})
	t.Run("a ready target passes read-only", func(t *testing.T) {
		d := newDeployWorld(t, "v2.2.1")
		out, err := d.run("", "--preflight")
		if err != nil || !strings.Contains(out, "deploy: preflight OK for component aihub (target fakehost)") {
			t.Fatalf("want preflight OK: %v\n%s", err, out)
		}
		d.assertNothingMutated("v2.2.1")
		for _, ln := range strings.Split(strings.TrimSpace(d.sshLog()), "\n") {
			ok := false
			for _, ro := range []string{"ssh true", "ssh docker compose version", "ssh docker inspect -f {{.State.Status}} aihub"} {
				ok = ok || ln == ro
			}
			if ln != "" && !ok {
				t.Errorf("non-read-only ssh call in the preflight: %q", ln)
			}
		}
	})
	t.Run("takes no version", func(t *testing.T) {
		d := newDeployWorld(t, "v2.2.1")
		if out, err := d.run("", "--preflight", "v2.3.0"); exitCode(err) != 2 {
			t.Fatalf("want a usage error, got %v\n%s", err, out)
		}
	})
}

// TestW372ReleaseStepRunsTheDeployPreflightFirst pins the step half of class
// D: the release step runs the deploy procedure's --preflight before the
// already-released check and before the release procedure, and a failure is
// PF-RELEASE-PREFLIGHT with nothing published.
func TestW372ReleaseStepRunsTheDeployPreflightFirst(t *testing.T) {
	rel := w68Flat(t, "workflows/release/steps/release.md")
	pre := strings.Index(rel, "run `.polyforge/deploy.sh --preflight` in that repo now, before anything below")
	guard := strings.Index(rel, "**Already released? Resume, never re-cut.**")
	run := strings.Index(rel, "## 4. Run the procedure")
	if pre < 0 || guard < 0 || run < 0 || pre > guard || pre > run {
		t.Fatalf("the deploy preflight must come before the already-released check and the procedure (pre=%d guard=%d run=%d)", pre, guard, run)
	}
	for _, want := range []string{"`PF-RELEASE-PREFLIGHT`", "nothing was published and no version was claimed", "grep -q -- '--preflight' .polyforge/deploy.sh"} {
		if !strings.Contains(rel, want) {
			t.Errorf("release.md must state %q", want)
		}
	}
	wf := w68Flat(t, "workflows/release/README.md")
	if !strings.Contains(wf, "`.polyforge/deploy.sh --preflight` (optional)") {
		t.Error("the release lane README.md's procedure contract must name the optional deploy preflight")
	}
}

// ---- classes A and B: a re-run after a partial or finished release.
// A: #267 #268 #285 #305 #306 #308 ended PF-RELEASE-ALREADY_RELEASED - a
// continuation work item (a new capsule) could never pass a head that
// already carried its tag. B: #120 (gh release create failed after the tag
// push) and #265 (docker push denied after the tag push) burned v2.20.10 and
// v2.20.18, because a re-run refused the tagged head. Both are now a resume.

// w372ResetCalls empties the call log between two runs of one world.
func w372ResetCalls(t *testing.T, w *relWorld) {
	t.Helper()
	relWrite(t, w.log, "", 0o644)
}

// w372ReleaseFiles lists the fake GitHub release's asset names (no dotfiles).
func w372ReleaseFiles(t *testing.T, w *relWorld, tag string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(w.fakeRel, tag))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names
}

// w372AssertResumedOnce: the resumed run cut no second version, pushed no
// tag, and the result passes the script's own --verify.
func w372AssertResumedOnce(t *testing.T, w *relWorld, out string) {
	t.Helper()
	if got := strings.Fields(w.originTags()); strings.Join(got, " ") != "v1.2.0 v1.2.1" {
		t.Fatalf("origin tags = %v, want exactly v1.2.0 v1.2.1 (resumed, no second version):\n%s", got, out)
	}
	if relIndex(w.calls(), "git push") >= 0 {
		t.Fatalf("the resume pushed a tag: %v", w.calls())
	}
	head := relGit(t, w.work, "rev-parse", "HEAD")
	for _, want := range []string{"resuming v1.2.1", "version=v1.2.1\n", "commit=" + head + "\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("resume output lacks %q:\n%s", want, out)
		}
	}
	if n := len(w372ReleaseFiles(t, w, "v1.2.1")); n != 7 {
		t.Fatalf("release v1.2.1 has %d assets, want 7: %v", n, w372ReleaseFiles(t, w, "v1.2.1"))
	}
	if vout, err := w.run(nil, "--verify", "v1.2.1"); err != nil {
		t.Fatalf("the resumed release must pass --verify: %v\n%s", err, vout)
	}
}

// TestW372ReleaseResumesAfterAFailureAfterTheClaim (class B): the first run
// claims v1.2.1 and dies - docker push denied (#265) or gh release create
// failing (#120). The next run from the same head resumes v1.2.1: no second
// version, no tag push, only the missing parts published, and the result
// passes --verify. Red on origin/main: the second run refused "HEAD is already
// released as v1.2.1" and the version stayed burned.
func TestW372ReleaseResumesAfterAFailureAfterTheClaim(t *testing.T) {
	t.Run("docker push denied (#265)", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		if out, err := w.run([]string{"FAKE_PUSH_FAIL=1"}); err == nil || !strings.Contains(out, "FAILED AFTER v1.2.1 WAS CLAIMED") {
			t.Fatalf("setup: the first run must fail after the claim: %v\n%s", err, out)
		}
		w372ResetCalls(t, w)
		out, err := w.run([]string{"FAKE_REGISTRY_MISSING=1"})
		if err != nil {
			t.Fatalf("the re-run must resume v1.2.1: %v\n%s", err, out)
		}
		calls := w.calls()
		for _, want := range []string{"tests", "build", "docker build", "docker push", "npm pack", "gh release create v1.2.1"} {
			if relIndex(calls, want) < 0 {
				t.Fatalf("the resume lacks %q (nothing was published before): %v", want, calls)
			}
		}
		w372AssertResumedOnce(t, w, out)
	})

	t.Run("gh release create failed (#120)", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		if out, err := w.run([]string{"FAKE_GH_CREATE_FAIL=1"}); err == nil || !strings.Contains(out, "failing step: gh release create") {
			t.Fatalf("setup: the first run must fail at gh release create: %v\n%s", err, out)
		}
		w372ResetCalls(t, w)
		out, err := w.run(nil) // the image the first run pushed is in the registry
		if err != nil {
			t.Fatalf("the re-run must resume v1.2.1: %v\n%s", err, out)
		}
		calls := w.calls()
		for _, no := range []string{"docker build", "docker push"} {
			if relIndex(calls, no) >= 0 {
				t.Fatalf("the image is in the registry: the resume must not %q: %v", no, calls)
			}
		}
		if relIndex(calls, "gh release create v1.2.1") < 0 || !strings.Contains(out, "already in the registry ("+relFakeDigest+")") {
			t.Fatalf("want the registry image kept and the release created: %v\n%s", calls, out)
		}
		dg, _ := os.ReadFile(filepath.Join(w.fakeRel, "v1.2.1", "aihub-image.digest"))
		if strings.TrimSpace(string(dg)) != relFakeDigest {
			t.Fatalf("aihub-image.digest = %q, want the registry's %s", dg, relFakeDigest)
		}
		w372AssertResumedOnce(t, w, out)
	})
}

// TestW372ReleaseAdoptsACompleteRelease (class A): a release lane filed again
// for a head whose release is already complete - a deploy continuation (#285)
// or a re-filed release (#305 #306 #308) - passes without running anything:
// no gate, build, push or upload, and it reports the version, the commit and
// the release page. Red on origin/main: "HEAD ... is already released".
func TestW372ReleaseAdoptsACompleteRelease(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	if out, err := w.run(nil); err != nil {
		t.Fatalf("setup release: %v\n%s", err, out)
	}
	w372ResetCalls(t, w)
	out, err := w.run(nil)
	if err != nil {
		t.Fatalf("a complete release on HEAD must be adopted, not refused: %v\n%s", err, out)
	}
	if c := w.calls(); len(c) != 0 {
		t.Fatalf("a complete release must run nothing (no gate, build, push or upload): %v", c)
	}
	head := relGit(t, w.work, "rev-parse", "HEAD")
	tail := strings.Split(strings.TrimSpace(out), "\n")
	tail = tail[len(tail)-3:]
	want := []string{"version=v1.2.1", "commit=" + head, "notes=https://github.com/silgrid/polyforge/releases/tag/v1.2.1"}
	for i := range want {
		if tail[i] != want[i] {
			t.Fatalf("tail line %d = %q, want %q\n%s", i, tail[i], want[i], out)
		}
	}
	if !strings.Contains(out, "already published and complete") {
		t.Fatalf("the adoption must say so:\n%s", out)
	}
}

// TestW372ReleaseResumeUploadsOnlyMissingAssets: a draft release holding only
// part of its assets (a run killed mid-upload) is completed: the published
// binaries and digest are kept byte for byte (no binary build, no image
// push), only the missing tarball and checksum files are uploaded (never
// --clobber), and the draft is published.
func TestW372ReleaseResumeUploadsOnlyMissingAssets(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	if out, err := w.run(nil); err != nil {
		t.Fatalf("setup release: %v\n%s", err, out)
	}
	rel := filepath.Join(w.fakeRel, "v1.2.1")
	for _, n := range []string{"silgrid-pi-web-v1.2.1.tgz", "SHA256SUMS", "release-manifest.json"} {
		if err := os.Remove(filepath.Join(rel, n)); err != nil {
			t.Fatal(err)
		}
	}
	relWrite(t, filepath.Join(rel, "pf-linux-amd64"), "pf-bin-as-published\n", 0o644)
	relWrite(t, filepath.Join(rel, ".draft"), "true\n", 0o644)
	w372ResetCalls(t, w)
	out, err := w.run(nil)
	if err != nil {
		t.Fatalf("resume: %v\n%s", err, out)
	}
	calls := w.calls()
	for _, no := range []string{"build", "docker build", "docker push", "gh release create", "gh release upload --clobber"} {
		if relIndex(calls, no) >= 0 {
			t.Fatalf("the resume must not %q: %v", no, calls)
		}
	}
	var uploaded []string
	for _, c := range calls {
		if strings.HasPrefix(c, "gh release upload asset ") {
			uploaded = append(uploaded, strings.TrimPrefix(c, "gh release upload asset "))
		}
	}
	if strings.Join(uploaded, " ") != "silgrid-pi-web-v1.2.1.tgz SHA256SUMS release-manifest.json" {
		t.Fatalf("uploaded %v, want exactly the 3 missing assets", uploaded)
	}
	if relIndex(calls, "gh release edit v1.2.1 --draft=false") < 0 {
		t.Fatalf("the draft was not published: %v", calls)
	}
	if b, _ := os.ReadFile(filepath.Join(rel, "pf-linux-amd64")); string(b) != "pf-bin-as-published\n" {
		t.Fatalf("a published binary was replaced: %q", b)
	}
	w372AssertResumedOnce(t, w, out)
}

// w372Unfinish makes a published release incomplete the way an interrupted
// upload leaves it: no checksum files yet, and payload missing.
func w372Unfinish(payload string) func(t *testing.T, w *relWorld, rel string) {
	return func(t *testing.T, w *relWorld, rel string) {
		for _, n := range []string{"SHA256SUMS", "release-manifest.json", payload} {
			if err := os.Remove(filepath.Join(rel, n)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestW372ReleaseResumeRefusesContradictions: what a resume cannot repair
// without guessing refuses with nothing published, for a human - and an
// unreadable registry is retryable, never read as a missing image.
func TestW372ReleaseResumeRefusesContradictions(t *testing.T) {
	const otherDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cases := []struct {
		name string
		mut  func(t *testing.T, w *relWorld, rel string)
		env  []string
		want string
	}{
		{"checksums published, a payload missing", func(t *testing.T, w *relWorld, rel string) {
			_ = os.Remove(filepath.Join(rel, "pf-darwin-arm64"))
		}, nil, "lacks pf-darwin-arm64"},
		{"published digest is not the registry's", w372Unfinish("silgrid-pi-web-v1.2.1.tgz"), []string{"FAKE_REGISTRY_DIGEST=" + otherDigest}, "is not the registry's digest"},
		// (FAKE_REGISTRY_MISSING would see the setup run's push; a file that
		// never appears keeps the image absent)
		{"published digest, no image in the registry", w372Unfinish("silgrid-pi-web-v1.2.1.tgz"), []string{"FAKE_REGISTRY_APPEARS_ON=/nonexistent/w372"}, "the registry has no"},
		{"an asset this procedure never publishes", func(t *testing.T, w *relWorld, rel string) {
			_ = os.Remove(filepath.Join(rel, "SHA256SUMS"))
			relWrite(t, filepath.Join(rel, "extra.bin"), "x", 0o644)
		}, nil, "never publishes: extra.bin"},
		{"registry unreadable (retryable)", w372Unfinish("aihub-image.digest"), []string{"FAKE_REGISTRY_UNREADABLE=1"}, "cannot read the registry state"},
		{"release unreadable (retryable)", nil, []string{"FAKE_GH_VIEW_FAIL=1"}, "cannot read the GitHub release v1.2.1"},
		// any buildx error but "not found" is unknown, never a fall-through to
		// another reader that might answer differently
		{"buildx error is unknown, not absent", w372Unfinish("aihub-image.digest"), []string{"FAKE_BUILDX_ERROR=failed to resolve: rate limited"}, "cannot read the registry state"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			if out, err := w.run(nil); err != nil {
				t.Fatalf("setup release: %v\n%s", err, out)
			}
			rel := filepath.Join(w.fakeRel, "v1.2.1")
			if c.mut != nil {
				c.mut(t, w, rel)
			}
			before := w372ReleaseFiles(t, w, "v1.2.1")
			w372ResetCalls(t, w)
			out, err := w.run(c.env)
			if err == nil {
				t.Fatalf("expected a refusal:\n%s", out)
			}
			if !strings.Contains(out, c.want) || !strings.Contains(out, "nothing published") {
				t.Fatalf("refusal lacks %q / \"nothing published\":\n%s", c.want, out)
			}
			for _, no := range []string{"docker push", "gh release", "git push", "tests"} {
				if relIndex(w.calls(), no) >= 0 {
					t.Fatalf("%q ran despite the refusal: %v", no, w.calls())
				}
			}
			if after := w372ReleaseFiles(t, w, "v1.2.1"); strings.Join(after, " ") != strings.Join(before, " ") {
				t.Fatalf("the release changed: %v -> %v", before, after)
			}
		})
	}

	t.Run("the head's tag is not the latest", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		relGit(t, w.work, "tag", "-a", "-m", "v1.3.0", "v1.3.0")
		relGit(t, w.work, "push", "-q", "origin", "v1.3.0")
		relGit(t, w.work, "tag", "-a", "-m", "v1.4.0", "v1.4.0", "HEAD~1")
		relGit(t, w.work, "push", "-q", "origin", "v1.4.0")
		out, err := w.run(nil)
		if err == nil || !strings.Contains(out, "not below it: v1.4.0") || len(w.calls()) != 0 {
			t.Fatalf("want a refusal naming v1.4.0 before any work: %v %v\n%s", err, w.calls(), out)
		}
	})
}

// ---- class F: a red test gate (#155 a deterministic test bug, #184 and #353
// flakes; #144's node major is class D). The gate always ran before the
// claim; what was missing is a run that SAYS so and a pin that a red gate
// consumes no version - the flaky tests themselves are not this ticket's.

// TestW372FailingTestGateConsumesNoVersion: a failing test gate stops before
// the build and the claim, leaves no tag on origin or in the clone, and says
// the version is still free; the next run (a flake retried) releases the
// same version. Red on origin/main: the gate exited without a word (set -e),
// so the run never said that nothing was published or claimed.
func TestW372FailingTestGateConsumesNoVersion(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	out, err := w.run([]string{`RELEASE_TEST_CMD=echo tests >> "$CALLLOG"; echo "--- FAIL: TestFlaky (90s)"; exit 2`})
	if err == nil {
		t.Fatalf("a failing test gate must fail the release:\n%s", out)
	}
	for _, want := range []string{"test gate failed", "exited 2", "nothing built or published", "no version claimed (v1.2.1 is still free)", "run the release again"} {
		if !strings.Contains(out, want) {
			t.Errorf("the test-gate failure lacks %q:\n%s", want, out)
		}
	}
	for _, p := range []string{"build", "docker", "npm", "git push", "gh release"} {
		if i := relIndex(w.calls(), p); i >= 0 {
			t.Fatalf("%q ran after the failing test gate: %v", p, w.calls())
		}
	}
	if got := w.originTags(); strings.Contains(got, "v1.2.1") {
		t.Fatalf("a failing test gate left a tag on origin: %q", got)
	}
	if got := relGit(t, w.work, "tag", "-l", "v1.2.1"); got != "" {
		t.Fatalf("a failing test gate left a local tag: %q", got)
	}
	// the retry (a flake that passes now) releases the very same version
	w372ResetCalls(t, w)
	out, err = w.run(nil)
	if err != nil || !strings.Contains(out, "version=v1.2.1\n") {
		t.Fatalf("the retry must release v1.2.1 - the failed gate consumed no version: %v\n%s", err, out)
	}
}

// TestW372FailingTestGateKeepsAResumedVersionResumable: on a resume, a red
// test gate publishes nothing new and the claimed version stays resumable.
func TestW372FailingTestGateKeepsAResumedVersionResumable(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	if out, err := w.run([]string{"FAKE_PUSH_FAIL=1"}); err == nil {
		t.Fatalf("setup: the first run must fail after the claim:\n%s", out)
	}
	out, err := w.run([]string{"FAKE_REGISTRY_MISSING=1", `RELEASE_TEST_CMD=exit 1`})
	if err == nil || !strings.Contains(out, "v1.2.1 stays claimed and resumable") {
		t.Fatalf("want a red gate that keeps v1.2.1 resumable: %v\n%s", err, out)
	}
	if _, serr := os.Stat(filepath.Join(w.fakeRel, "v1.2.1")); serr == nil {
		t.Fatal("a release was created despite the red gate")
	}
	if out, err := w.run([]string{"FAKE_REGISTRY_MISSING=1"}); err != nil || !strings.Contains(out, "version=v1.2.1\n") {
		t.Fatalf("the next run must resume v1.2.1: %v\n%s", err, out)
	}
}

// ---- review follow-up (PR #825): a resume holds no exclusive claim, so it
// must not publish from the picture it took before its gates ran.

// TestW372ResumeRereadsBeforeEachPublishAction: another run publishes V while
// this resume runs its gates - its image appears (a), or its GitHub release
// appears (b). The resume re-reads the registry and the release right before
// each publish action and refuses with nothing (more) pushed. Before the fix
// the resume pushed its own image over the one present by push time (a) and
// exited 0.
func TestW372ResumeRereadsBeforeEachPublishAction(t *testing.T) {
	t.Run("the image appears during the gates: no docker push", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		if out, err := w.run([]string{"FAKE_PUSH_FAIL=1"}); err == nil {
			t.Fatalf("setup: the first run must fail after the claim:\n%s", out)
		}
		w372ResetCalls(t, w)
		flag := filepath.Join(w.root, "other-run-pushed")
		out, err := w.run([]string{"FAKE_REGISTRY_APPEARS_ON=" + flag, `RELEASE_TEST_CMD=echo tests >> "$CALLLOG"; touch ` + flag})
		if err == nil {
			t.Fatalf("the resume must refuse when the image appeared after its first read:\n%s", out)
		}
		for _, want := range []string{"stopped before docker push", "image ghcr.io/silgrid/aihub:v1.2.1: absent -> present " + relFakeDigest, "Published by this run: nothing", "v1.2.1 stays claimed"} {
			if !strings.Contains(out, want) {
				t.Errorf("refusal lacks %q:\n%s", want, out)
			}
		}
		for _, no := range []string{"docker push", "gh release"} {
			if relIndex(w.calls(), no) >= 0 {
				t.Fatalf("%q ran after the published state changed: %v", no, w.calls())
			}
		}
	})
	t.Run("the release appears during the gates: no release update", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		if out, err := w.run([]string{"FAKE_GH_CREATE_FAIL=1"}); err == nil {
			t.Fatalf("setup: the first run must fail at gh release create:\n%s", out)
		}
		w372ResetCalls(t, w)
		rel := filepath.Join(w.fakeRel, "v1.2.1")
		out, err := w.run([]string{`RELEASE_TEST_CMD=echo tests >> "$CALLLOG"; mkdir -p ` + rel + ` && echo x > ` + rel + `/pf-linux-amd64`})
		if err == nil {
			t.Fatalf("the resume must refuse when the release appeared after its first read:\n%s", out)
		}
		if !strings.Contains(out, "stopped before the GitHub release update") || !strings.Contains(out, "GitHub release: absent -> present") {
			t.Fatalf("want a refusal naming the changed release:\n%s", out)
		}
		for _, no := range []string{"docker push", "gh release create", "gh release upload"} {
			if relIndex(w.calls(), no) >= 0 {
				t.Fatalf("%q ran after the published state changed: %v", no, w.calls())
			}
		}
	})
	t.Run("an unchanged state is re-read and passes", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		if out, err := w.run([]string{"FAKE_PUSH_FAIL=1"}); err == nil {
			t.Fatalf("setup:\n%s", out)
		}
		out, err := w.run([]string{"FAKE_REGISTRY_MISSING=1"})
		if err != nil {
			t.Fatalf("resume: %v\n%s", err, out)
		}
		for _, want := range []string{"re-read before docker push - unchanged", "re-read before the GitHub release update - unchanged"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
	})
}

// TestW372ResumeReadsTheRegistryAfterTheLogin: the resume's registry read
// runs after the preflight's docker login, so a stored login that cannot read
// is replaced first and the read and the push use one identity. With the read
// before the login every such resume died "cannot read the registry state".
func TestW372ResumeReadsTheRegistryAfterTheLogin(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	if out, err := w.run([]string{"FAKE_PUSH_FAIL=1"}); err == nil {
		t.Fatalf("setup:\n%s", out)
	}
	w372ResetCalls(t, w)
	out, err := w.run([]string{"FAKE_REGISTRY_NEEDS_LOGIN=1", "FAKE_REGISTRY_MISSING=1", "FAKE_GH_SCOPES=repo, write:packages"})
	if err != nil {
		t.Fatalf("the resume must log in, then read the registry, then publish: %v\n%s", err, out)
	}
	calls := w.calls()
	if login, push := relIndex(calls, "docker login"), relIndex(calls, "docker push"); login < 0 || push < 0 || login > push {
		t.Fatalf("want docker login before docker push: %v", calls)
	}
	w372AssertResumedOnce(t, w, out)
}

// TestW372RepoGuardReadsPushURLAndFailsClosed (class C, review follow-up):
// the tag goes where REMOTE's pushurl points, so a pushurl naming another
// repo refuses; a remote that is not a github.com URL (an ssh host alias, a
// path) refuses unless RELEASE_REMOTE_IS states the repo.
func TestW372RepoGuardReadsPushURLAndFailsClosed(t *testing.T) {
	without := func(env []string, key string) []string {
		var kept []string
		for _, kv := range env {
			if !strings.HasPrefix(kv, key+"=") {
				kept = append(kept, kv)
			}
		}
		return kept
	}
	run := func(w *relWorld, extra ...string) (string, error) {
		c := exec.Command("sh", w.script)
		c.Dir = w.work
		c.Env = append(without(w.env(), "RELEASE_REMOTE_IS"), extra...)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	t.Run("pushurl names another repo", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		w372NameOrigin(t, w, "git@github.com:silgrid/polyforge.git")
		relGit(t, w.work, "config", "remote.origin.pushurl", "git@github.com:example-org/polyforge.git")
		out, err := run(w)
		if err == nil || !strings.Contains(out, "example-org/polyforge") || !strings.Contains(out, "a release must live where its tag is") || len(w.calls()) != 0 {
			t.Fatalf("want a refusal naming the pushurl's repo before any work: %v %v\n%s", err, w.calls(), out)
		}
	})
	for name, url := range map[string]string{"an ssh host alias": "git@github.com-work:silgrid/polyforge.git", "a local path": ""} {
		t.Run(name+" refuses without RELEASE_REMOTE_IS", func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			if url != "" {
				w372NameOrigin(t, w, url)
			}
			out, err := run(w)
			if err == nil || !strings.Contains(out, "cannot tell which GitHub repo origin is") || !strings.Contains(out, "RELEASE_REMOTE_IS") || len(w.calls()) != 0 {
				t.Fatalf("want a fail-closed refusal: %v %v\n%s", err, w.calls(), out)
			}
			if out, err := run(w, "RELEASE_REMOTE_IS=example-org/polyforge"); err == nil || !strings.Contains(out, "RELEASE_REMOTE_IS=example-org/polyforge") {
				t.Fatalf("a stated repo that is not the gh repo must refuse: %v\n%s", err, out)
			}
			if out, err := run(w, "RELEASE_REMOTE_IS=silgrid/polyforge"); err != nil || !strings.Contains(out, "version=v1.2.1\n") {
				t.Fatalf("the stated repo matching the gh repo must release: %v\n%s", err, out)
			}
		})
	}
}

// TestW372DeployRefusesAGHRepoThatIsNotOrigins (class C, review follow-up):
// an ambient GH_REPO (an operator env file kept for the public channel) or an
// origin naming another repo refuses a deploy before any work; no origin
// refuses unless DEPLOY_REMOTE_IS states the repo; --preflight reports it.
// Before, deploy.sh followed whatever GH_REPO said.
func TestW372DeployRefusesAGHRepoThatIsNotOrigins(t *testing.T) {
	strip := func(env []string, keys ...string) []string {
		var kept []string
	next:
		for _, kv := range env {
			for _, k := range keys {
				if strings.HasPrefix(kv, k+"=") {
					continue next
				}
			}
			kept = append(kept, kv)
		}
		return kept
	}
	t.Run("ambient GH_REPO against origin", func(t *testing.T) {
		d := newReadyWorld(t)
		d.git("remote", "add", "origin", "git@github.com:silgrid/polyforge.git")
		d.env = append(strip(d.env, "DEPLOY_REMOTE_IS"), "GH_REPO=example-org/polyforge")
		out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
		if exitCode(err) != 1 || !strings.Contains(out, "this checkout's origin is silgrid/polyforge") || !strings.Contains(out, "Nothing was touched") {
			t.Fatalf("want a refusal before any work: %v\n%s", err, out)
		}
		if d.logOf("log-gh") != "" || d.logOf("log-ssh") != "" {
			t.Fatalf("work ran before the refusal:\n%s%s", d.logOf("log-gh"), d.logOf("log-ssh"))
		}
		pout, perr := d.run("", "--preflight")
		if exitCode(perr) != 1 || !strings.Contains(pout, "this checkout's origin is silgrid/polyforge") {
			t.Fatalf("--preflight must report the mismatch: %v\n%s", perr, pout)
		}
	})
	t.Run("no origin and nothing stated", func(t *testing.T) {
		d := newReadyWorld(t)
		d.env = strip(d.env, "DEPLOY_REMOTE_IS")
		out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
		if exitCode(err) != 1 || !strings.Contains(out, "cannot tell which GitHub repo this checkout's origin is") {
			t.Fatalf("want a fail-closed refusal: %v\n%s", err, out)
		}
	})
	t.Run("origin matching the default deploys", func(t *testing.T) {
		d := newReadyWorld(t)
		d.git("remote", "add", "origin", "https://github.com/silgrid/polyforge.git")
		d.env = strip(d.env, "DEPLOY_REMOTE_IS", "GH_REPO")
		if out, err := d.run("", "v2.3.0", "--dry-run", "--confirm", "v2.3.0"); err != nil {
			t.Fatalf("a matching origin must pass the check: %v\n%s", err, out)
		}
	})
}
