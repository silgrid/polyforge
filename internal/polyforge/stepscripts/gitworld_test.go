package stepscripts

// gitworld_test.go: the world sync.py, merge.py and revert.py (the step
// scripts, py_scripts_test.go) and gate.py run in — per
// repo a bare origin whose main holds an append-only upgrade log and its
// pin (main's .polyforge/integrate.conf declares both) and some code, a
// capsule with each repo cloned on the work item's branch, and the fake
// gh (testdata/fake-gh.sh: PRs as files under $FAKE_GH_STATE, a merge a
// real --no-ff merge into the bare origin) first on the step's PATH.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type gitWorld struct {
	t                           *testing.T
	root, origins, ghState, bin string
	repos                       []string
	seeds                       map[string]string
	capsule, branch             string
}

func pinOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// upgradeAppend answers the files that append section to dir's upgrade
// log and move its pin.
func upgradeAppend(t *testing.T, dir, section string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "docs", "upgrade.md"))
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw) + "\n## " + section + "\n"
	return map[string]string{"docs/upgrade.md": up, "pin.txt": pinOf(up) + "\n"}
}

func newGitWorld(t *testing.T, repos ...string) *gitWorld {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	w := &gitWorld{t: t, root: root, origins: filepath.Join(root, "origins"), ghState: filepath.Join(root, "gh"), bin: filepath.Join(root, "bin"),
		repos: repos, seeds: map[string]string{}, capsule: filepath.Join(root, "capsule"), branch: "polyforge/wi"}
	for _, d := range []string{w.origins, w.ghState, w.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gh, err := os.ReadFile(filepath.Join("testdata", "fake-gh.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.bin, "gh"), gh, 0o755); err != nil {
		t.Fatal(err)
	}
	heads := map[string]string{}
	for _, r := range repos {
		origin := filepath.Join(w.origins, r+".git")
		gitT(t, root, "init", "-q", "--bare", "-b", "main", origin)
		seed := filepath.Join(root, "seed-"+r)
		gitT(t, root, "clone", "-q", origin, seed)
		gitT(t, seed, "checkout", "-q", "-B", "main")
		up := "# upgrade\n"
		for rel, body := range map[string]string{
			"README": r + "\n", "docs/upgrade.md": up, "pin.txt": pinOf(up) + "\n",
			".polyforge/integrate.conf": "append_only=docs/upgrade.md\npin=docs/upgrade.md pin.txt\n",
			"pkg/code.go":               "package pkg\n\nfunc F() int { return 1 }\n",
		} {
			writeFile(t, seed, rel, body)
		}
		commitAll(t, seed, "base")
		gitT(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")
		w.seeds[r] = seed
		dir := w.dir(r)
		gitT(t, root, "clone", "-q", origin, dir)
		gitT(t, dir, "checkout", "-q", "-b", w.branch)
		heads[r] = w.head(r)
	}
	raw, _ := json.Marshal(map[string]any{"work_item_id": "wi_gw", "bundle_name": "develop", "repos": heads})
	writeFile(t, w.capsule, "capsule.json", string(raw))
	return w
}

// env is the step environment the commands run under: the fake gh first
// on the PATH, the fake's state.
func (w *gitWorld) env(extra ...string) []string {
	return withPath(stepEnv(append([]string{
		"FAKE_GH_STATE=" + w.ghState, "FAKE_GH_ORIGINS=" + w.origins,
	}, extra...)...), w.bin)
}

func (w *gitWorld) dir(repo string) string { return filepath.Join(w.capsule, "repos", repo) }

func (w *gitWorld) head(repo string) string { return gitT(w.t, w.dir(repo), "rev-parse", "HEAD") }

func (w *gitWorld) originMain(repo string) string {
	return gitT(w.t, w.root, "--git-dir="+filepath.Join(w.origins, repo+".git"), "rev-parse", "refs/heads/main")
}

func (w *gitWorld) originBranch(repo, branch string) string {
	return gitT(w.t, w.root, "--git-dir="+filepath.Join(w.origins, repo+".git"), "rev-parse", "refs/heads/"+branch)
}

// mainMoves lands a commit on origin's main of repo: files, plus an
// upgrade section appended when section is not empty.
func (w *gitWorld) mainMoves(repo string, files map[string]string, section string) string {
	t := w.t
	seed := w.seeds[repo]
	gitT(t, seed, "fetch", "-q", "origin")
	gitT(t, seed, "checkout", "-q", "-B", "main", "origin/main")
	if section != "" {
		if files == nil {
			files = map[string]string{}
		}
		for k, v := range upgradeAppend(t, seed, section) {
			files[k] = v
		}
	}
	for rel, body := range files {
		writeFile(t, seed, rel, body)
	}
	sha := commitAll(t, seed, "main: "+orText(section, "moves"))
	gitT(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	return sha
}

// change commits files (and an upgrade section, when not empty) on the
// item's branch in the capsule's clone of repo.
func (w *gitWorld) change(repo string, files map[string]string, section string) string {
	t := w.t
	dir := w.dir(repo)
	if section != "" {
		if files == nil {
			files = map[string]string{}
		}
		for k, v := range upgradeAppend(t, dir, section) {
			files[k] = v
		}
	}
	for rel, body := range files {
		writeFile(t, dir, rel, body)
	}
	return commitAll(t, dir, "change: "+orText(section, "code"))
}

// prDir answers the fake gh's directory of PR n of repo.
func (w *gitWorld) prDir(repo string, n int) string {
	return filepath.Join(w.ghState, repo, "prs", fmt.Sprint(n))
}

func (w *gitWorld) prURL(repo string, n int) string {
	return fmt.Sprintf("https://github.com/fake/%s/pull/%d", repo, n)
}

// openPR registers an open PR of branch into main at the fake gh and
// answers its URL (n = one past the repo's PRs).
func (w *gitWorld) openPR(repo, branch string) (string, int) {
	t := w.t
	n := 1
	if ents, err := os.ReadDir(filepath.Join(w.ghState, repo, "prs")); err == nil {
		n = len(ents) + 1
	}
	d := w.prDir(repo, n)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"head": branch, "base": "main", "state": "OPEN", "url": w.prURL(repo, n)} {
		if err := os.WriteFile(filepath.Join(d, k), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return w.prURL(repo, n), n
}

func (w *gitWorld) prFile(repo string, n int, name string) string {
	raw, _ := os.ReadFile(filepath.Join(w.prDir(repo, n), name))
	return strings.TrimSpace(string(raw))
}

// prCount answers how many PRs the fake gh holds for repo.
func (w *gitWorld) prCount(repo string) int {
	ents, _ := os.ReadDir(filepath.Join(w.ghState, repo, "prs"))
	return len(ents)
}

// gh runs the fake gh in dir with the world's environment.
func (w *gitWorld) gh(dir string, args ...string) (string, error) {
	c := exec.Command(filepath.Join(w.bin, "gh"), args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "FAKE_GH_STATE="+w.ghState, "FAKE_GH_ORIGINS="+w.origins)
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// publish pushes the capsule branch of repo to origin and opens its PR:
// the publish artifact's entry.
func (w *gitWorld) publish(repo string) publishEntry {
	gitT(w.t, w.dir(repo), "push", "-q", "origin", "HEAD:refs/heads/"+w.branch)
	url, _ := w.openPR(repo, w.branch)
	return publishEntry{Name: repo, Branch: w.branch, PRURL: url, Head: w.head(repo)}
}

// writePublish writes the publish artifact at path.
func (w *gitWorld) writePublish(path string, entries ...publishEntry) {
	raw, _ := json.MarshalIndent(publishFile{Repos: entries}, "", "  ")
	writeFile(w.t, filepath.Dir(path), filepath.Base(path), string(raw))
}

// writeVerified writes a verification result at path carrying heads.
func (w *gitWorld) writeVerified(path string, heads map[string]string) {
	if err := WriteResult(path, &Result{Schema: Schema, Command: "verify.py", Verdict: VerdictPass, Heads: heads, Details: map[string]any{}, Logs: []string{}}); err != nil {
		w.t.Fatal(err)
	}
}

// land merges a branch carrying files (and an upgrade section) into
// origin's main of repo through the fake gh (a work item's landing) and
// answers the merge commit.
func (w *gitWorld) land(repo, branch string, files map[string]string, section string) string {
	t := w.t
	seed := w.seeds[repo]
	gitT(t, seed, "fetch", "-q", "origin")
	gitT(t, seed, "checkout", "-q", "-B", branch, "origin/main")
	if section != "" {
		if files == nil {
			files = map[string]string{}
		}
		for k, v := range upgradeAppend(t, seed, section) {
			files[k] = v
		}
	}
	for rel, body := range files {
		writeFile(t, seed, rel, body)
	}
	head := commitAll(t, seed, branch)
	gitT(t, seed, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
	url, n := w.openPR(repo, branch)
	if out, err := w.gh(seed, "pr", "merge", url, "--merge", "--match-head-commit", head); err != nil {
		t.Fatalf("landing %s in %s: %v\n%s", branch, repo, err, out)
	}
	return w.prFile(repo, n, "merge")
}

// writeBatch writes a collect result at path: the pins and the items.
func (w *gitWorld) writeBatch(path string, heads map[string]string, items []batchItem) {
	gated := make([]string, 0, len(heads))
	for repo := range heads {
		gated = append(gated, repo)
	}
	sort.Strings(gated)
	if err := WriteResult(path, &Result{Schema: Schema, Command: NameIntegrate, Verdict: VerdictPass, Heads: heads,
		Details: map[string]any{"items": items, "gated": gated}, Logs: []string{}}); err != nil {
		w.t.Fatal(err)
	}
}

// parents answers a commit's parents in dir.
func parents(t *testing.T, dir, rev string) []string {
	t.Helper()
	return strings.Fields(gitT(t, dir, "log", "-1", "--format=%P", rev))
}

// fileIn answers the file's content in dir.
func fileIn(t *testing.T, dir, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
