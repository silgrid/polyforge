// testfiles.go: the committed-test-files gate (polyforge#156).
//
// A step that authors test files (feature-full's e2e-author, bug-fix's
// repro — any step whose output declares test_files) owes them as a
// commit in their task repo. The prose duty alone was not enough: a
// resume re-clones the capsule and restores each task repo only at its
// carrier ref (refs/pf-task/<name>, which records HEAD), so an authored
// test left uncommitted is silently lost and the next step loops on a
// suite that no longer exists (pi#102). The driver therefore refuses a
// completed envelope whose declared test_files are not committed at
// HEAD — through the ordinary envelope-rejection retry lane, the same
// one the V4 command-evidence gate uses.
//
// The task repos are agent-controlled and agents are untrusted, so the
// gate only ever runs `git ls-tree` (no worktree scan: no fsmonitor, no
// clean/smudge filter, no hook), with repo-local fsmonitor and hooks
// disabled on the command line, system config ignored, inherited GIT_*
// stripped and a hard timeout.
package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// testFilesGitTimeout bounds one git invocation of the gate.
const testFilesGitTimeout = 10 * time.Second

// declaredTestFiles reads the top-level output.test_files string list.
// An absent, empty or non-list value declares nothing (the declared
// schema lane stays the authority on its shape).
func declaredTestFiles(output json.RawMessage) []string {
	if len(output) == 0 {
		return nil
	}
	var probe struct {
		TestFiles []any `json:"test_files"`
	}
	if json.Unmarshal(output, &probe) != nil {
		return nil
	}
	var out []string
	for _, v := range probe.TestFiles {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// checkTestFilesCommitted verifies the paths the output declares in
// test_files against the task repos under reposDir (<capsule>/repos).
//
// The rule (the step schemas' shape): every path is repo-relative
// (cleaned, not absolute, no ".."); it must exist on disk in at least
// one task repo; and in EVERY task repo where it exists on disk it must
// be committed at HEAD — the only state a resume restores. The error
// names each failing path and the repo(s) where it is not committed.
func checkTestFilesCommitted(reposDir string, output json.RawMessage) error {
	files := declaredTestFiles(output)
	if len(files) == 0 {
		return nil
	}
	repos := taskRepoNames(reposDir)
	var problems []string
	for _, f := range files {
		if p := testFileProblem(reposDir, repos, f); p != "" {
			problems = append(problems, fmt.Sprintf("%s (%s)", f, p))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("uncommitted output.test_files: %s — commit every declared test file in its task repo (one dedicated commit; `git add -f` a path under a .gitignore'd directory) BEFORE writing the result envelope (the step process is torn down once the envelope appears); uncommitted files are lost when the work item is resumed",
		strings.Join(problems, "; "))
}

// taskRepoNames lists the task repos under reposDir (the same inventory
// the carrier-ref push walks: non-hidden directories).
func taskRepoNames(reposDir string) []string {
	ents, err := os.ReadDir(reposDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// testFileProblem answers "" when declared passes the rule, else a short
// description of what is wrong.
func testFileProblem(reposDir string, repos []string, declared string) string {
	rel := path.Clean(filepath.ToSlash(declared))
	if path.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "not a repo-relative path"
	}
	var present, uncommitted []string
	for _, name := range repos {
		dir := filepath.Join(reposDir, name)
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			continue
		}
		present = append(present, name)
		if !committedAtHead(dir, rel) {
			uncommitted = append(uncommitted, "repos/"+name)
		}
	}
	if len(present) == 0 {
		return "does not exist in any task repo"
	}
	if len(uncommitted) > 0 {
		return "not committed at HEAD in " + strings.Join(uncommitted, ", ")
	}
	return ""
}

// committedAtHead reports whether rel is part of HEAD's tree in repoDir.
// An unborn HEAD, a non-repo or a git failure all answer false.
func committedAtHead(repoDir, rel string) bool {
	out, err := repoGit(repoDir, "ls-tree", "--name-only", "HEAD", "--", rel)
	return err == nil && strings.TrimSpace(out) != ""
}

// repoGit runs one read-only git command inside an (untrusted) task repo:
// its own git-dir (inherited GIT_* stripped), system config ignored,
// fsmonitor and hooks disabled, literal pathspecs, no optional locks and
// a hard timeout.
func repoGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), testFilesGitTimeout)
	defer cancel()
	argv := append([]string{
		"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null",
		"--no-optional-locks", "--literal-pathspecs",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Dir = dir
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "LC_ALL=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// ---------------------------------------------------------------------------
// The authored-tests immutability gate (polyforge#164 H4).
//
// The tests an authoring step committed (feature-full's e2e-author,
// bug-fix's repro: any step whose accepted output declares test_files)
// are the acceptance bar the later steps are judged by, so a later step
// may not change them: code "fixing" a red scenario by weakening its
// assertion would otherwise pass every gate. When an authoring step's
// completed result is accepted, the driver stamps its output with
// testFilesCommitKey — each task repo's HEAD at that moment (the
// authored commit). Every later completed step is then checked
// mechanically: the declared files must be identical between that
// commit and HEAD — `git diff <author-commit>..HEAD -- <test_files>`,
// computed as two `git ls-tree -r` listings so the untrusted repo runs
// no diff driver, filter or hook — except where the difference came only
// through machine-made merges of the default branch (mergedFromDefault:
// verification merges the latest default branch before its bar, and an
// edit upstream made to an authored file is not the agent's). A
// remaining difference rejects the envelope
// (the ordinary retry lane) unless a human answered a question of the
// step's current attempt chain: a step that believes an authored test
// is wrong must say so through needs_input, and the approved change
// becomes the new baseline (the authoring steps' stamps are replaced
// with the new HEADs, so the approval survives a rewind of the step).

// EvAuthoredTestsRebaselined is the durable event kind recording a
// human-approved change to authored test files (the files it names
// become the new baseline).
const EvAuthoredTestsRebaselined = "authored_tests_rebaselined"

// testFilesCommitKey is the driver-written output key carrying the
// authored-tests baseline: task repo name -> commit sha.
const testFilesCommitKey = "test_files_commit"

// AuthoredTests is the authored-tests baseline a later step is judged
// against: the declared files and, per task repo, the commit they must
// be unchanged from.
type AuthoredTests struct {
	Files    []string
	Baseline map[string]string
}

// authoredTestsBefore folds the baseline from the done steps that
// precede stepID in workflow order: files are the union of their
// declared test_files, and each repo's baseline is the latest stamped
// commit (a later stamp — an approved change — supersedes an earlier).
func authoredTestsBefore(wf *Workflow, steps map[string]map[string]any, stepID string) *AuthoredTests {
	at := &AuthoredTests{Baseline: map[string]string{}}
	seen := map[string]bool{}
	for _, s := range wf.Steps {
		if s.ID == stepID {
			break
		}
		out, ok := steps[s.ID]
		if !ok {
			continue
		}
		if list, ok := out["test_files"].([]any); ok {
			for _, v := range list {
				if f, ok := v.(string); ok && strings.TrimSpace(f) != "" && !seen[strings.TrimSpace(f)] {
					seen[strings.TrimSpace(f)] = true
					at.Files = append(at.Files, strings.TrimSpace(f))
				}
			}
		}
		switch stamp := out[testFilesCommitKey].(type) {
		case map[string]any:
			for repo, sha := range stamp {
				if s, ok := sha.(string); ok && s != "" {
					at.Baseline[repo] = s
				}
			}
		case map[string]string:
			for repo, sha := range stamp {
				if sha != "" {
					at.Baseline[repo] = sha
				}
			}
		}
	}
	if len(at.Files) == 0 || len(at.Baseline) == 0 {
		return nil
	}
	return at
}

// changedAuthoredTests answers the authored test files that differ
// between each repo's baseline commit and its HEAD ("repos/<name>:
// <path>" entries), less those whose difference came only through
// machine-made merges of the default branch (mergedFromDefault). An
// unreadable baseline or HEAD is reported as a change — fail closed: a
// rewritten history cannot hide an edit.
func changedAuthoredTests(reposDir string, at *AuthoredTests) []string {
	if at == nil {
		return nil
	}
	repos := make([]string, 0, len(at.Baseline))
	for repo := range at.Baseline {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	var changed []string
	for _, repo := range repos {
		dir := filepath.Join(reposDir, repo)
		base, berr := repoGit(dir, lsTreeArgs(at.Baseline[repo], at.Files)...)
		head, herr := repoGit(dir, lsTreeArgs("HEAD", at.Files)...)
		if berr != nil || herr != nil {
			changed = append(changed, "repos/"+repo+": baseline "+shortSHA(at.Baseline[repo])+" or HEAD unreadable")
			continue
		}
		bm, hm := lsTreeByPath(base), lsTreeByPath(head)
		var paths []string
		for p := range bm {
			paths = append(paths, p)
		}
		for p := range hm {
			if _, ok := bm[p]; !ok {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		var differ []string
		for _, p := range paths {
			if bm[p] != hm[p] {
				differ = append(differ, p)
			}
		}
		merged := mergedFromDefault(dir, at.Baseline[repo], differ)
		for _, p := range differ {
			if !merged[p] {
				changed = append(changed, "repos/"+repo+": "+p)
			}
		}
	}
	return changed
}

// mergedFromDefault answers which of the differing paths changed since
// baseline ONLY through machine-made merges of the default branch
// verification runs `git merge --no-edit
// origin/<default>` before its bar, so an authored test upstream also
// edited reaches HEAD with a new blob the agent never wrote — rejecting
// it would block a correct pass, and restoring the author blob would
// undo upstream's edit.
//
// The proof walks the first-parent history baseline..HEAD (baseline must
// be an ancestor of HEAD): every merge must be a two-parent merge git
// reproduces without conflict (mergeReproduced — the proof deliver's
// handMadeHistory uses) whose second parent is on the fetched origin
// default branch (so a side branch merged in cannot launder an edit), and
// a path is explained only when no non-merge first-parent commit touched
// it. Anything else — a hand-made merge, an octopus, a root commit, an
// unknown default branch, an unreadable listing — explains nothing (fail
// closed: the paths stay changed). An agent edit later reverted still
// counts as touched; the gate then rejects as it did before.
func mergedFromDefault(dir, baseline string, paths []string) map[string]bool {
	if len(paths) == 0 {
		return nil
	}
	if _, err := repoGit(dir, "merge-base", "--is-ancestor", baseline, "HEAD"); err != nil {
		return nil
	}
	def := originDefaultRef(dir)
	if def == "" {
		return nil
	}
	out, err := repoGit(dir, "rev-list", "--first-parent", "--parents", baseline+"..HEAD")
	if err != nil {
		return nil
	}
	listings := map[string]map[string]string{}
	listing := func(rev string) (map[string]string, bool) {
		if m, ok := listings[rev]; ok {
			return m, true
		}
		raw, err := repoGit(dir, lsTreeArgs(rev, paths)...)
		if err != nil {
			return nil, false
		}
		listings[rev] = lsTreeByPath(raw)
		return listings[rev], true
	}
	touched := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		switch len(f) {
		case 0:
			continue
		case 2: // a non-merge commit: does it touch a path?
			cm, cok := listing(f[0])
			pm, pok := listing(f[1])
			if !cok || !pok {
				return nil
			}
			for _, p := range paths {
				if cm[p] != pm[p] {
					touched[p] = true
				}
			}
		case 3: // a merge: machine-made, of the default branch
			if mergeReproduced(dir, f[0], f[1], f[2]) != "" {
				return nil
			}
			if _, err := repoGit(dir, "merge-base", "--is-ancestor", f[2], def); err != nil {
				return nil
			}
		default:
			return nil
		}
	}
	merged := map[string]bool{}
	for _, p := range paths {
		if !touched[p] {
			merged[p] = true
		}
	}
	return merged
}

// lsTreeArgs is `git ls-tree -r --full-tree <rev> -- <files...>`.
func lsTreeArgs(rev string, files []string) []string {
	return append([]string{"ls-tree", "-r", "--full-tree", rev, "--"}, files...)
}

// lsTreeByPath indexes `git ls-tree -r` output: path -> "mode type sha".
func lsTreeByPath(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		meta, p, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		m[p] = meta
	}
	return m
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// checkAuthoredTestsUnchanged is the gate's verdict for one completed
// step: nothing when no authored file changed; the changed files with a
// nil error when the change is human-approved (the caller re-baselines);
// an error otherwise.
func checkAuthoredTestsUnchanged(reposDir string, at *AuthoredTests, humanAnswers int) (changed []string, err error) {
	diff := changedAuthoredTests(reposDir, at)
	if len(diff) == 0 {
		return nil, nil
	}
	if humanAnswers > 0 {
		return diff, nil
	}
	return diff, fmt.Errorf("changed authored test files: %s — the tests e2e-author/repro committed are the acceptance bar and later steps may not edit them (`git diff <author-commit>..HEAD -- <test_files>` must be empty). Restore them (`git checkout <author-commit> -- <path>` and commit); if you believe an authored test is itself wrong, return needs_input asking the owner instead of changing it",
		strings.Join(diff, "; "))
}

// repoHeads answers each task repo's HEAD commit (repos with an unborn
// or unreadable HEAD are omitted).
func repoHeads(reposDir string) map[string]any {
	heads := map[string]any{}
	for _, name := range taskRepoNames(reposDir) {
		out, err := repoGit(filepath.Join(reposDir, name), "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
		if sha := strings.TrimSpace(out); err == nil && sha != "" {
			heads[name] = sha
		}
	}
	return heads
}
