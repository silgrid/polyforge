package verifyrun

// gitrepo.go: the merged-tree phase of one task repo — the MERGE_HEAD
// self-heal, `git fetch origin`, the default-branch resolution
// (origin/HEAD, falling back to main, then master; the remote prefix
// stripped), `git merge --no-edit origin/<default>` with a conflict
// aborted and the tree confirmed restored, the untouched judgment and the
// pre-existing base — plus the untracked-test-file listing.

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The merge outcomes.
const (
	MergeUpToDate        = "up-to-date"
	MergeFastForward     = "fast-forward"
	MergeCommit          = "merge-commit"
	MergeConflictAborted = "conflict-aborted"
)

// MergeOutcome is one repo's verification-time merge.
type MergeOutcome struct {
	DefaultBranch string `json:"default_branch"`
	Outcome       string `json:"outcome"`
	PreHead       string `json:"pre_head"`
	Head          string `json:"head"`
	// SelfHealed: a merge left in progress by an earlier, interrupted
	// dispatch (MERGE_HEAD present at start) was aborted first.
	SelfHealed bool `json:"self_healed,omitempty"`
	// Touched: the merged HEAD carries commits beyond origin/<default>.
	Touched bool `json:"touched"`
	// Base is the pre-existing/engine base (merge-base HEAD
	// origin/<default>, or the --base override).
	Base   string `json:"base"`
	Detail string `json:"detail,omitempty"`
}

// repoRun is one task repo's run state.
type repoRun struct {
	name   string
	dir    string // absolute repos/<name>
	cwdRel string // "repos/<name>"
	logDir string

	mu       sync.Mutex
	k        int
	evidence []Evidence

	merge   *MergeOutcome
	touched bool

	plan     *repoPlan
	runs     []*cmdRun // plan commands, in plan order
	authored []*cmdRun // binding commands the plan did not already run

	untrackedBefore untrackedScan
	untrackedAfter  untrackedScan

	// goHarness is the repo conf's declared Go acceptance harness
	// (`go_harness=<path>`, repo-relative; "" = none).
	goHarness string
}

// readGoHarness reads the repo conf's `go_harness=` declaration (the
// last one wins; "" = none).
func readGoHarness(repoDir string) string {
	raw, err := os.ReadFile(filepath.Join(repoDir, ".polyforge", "verify-scope.conf"))
	if err != nil {
		return ""
	}
	h := ""
	for _, l := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "go_harness="); ok {
			h = cleanRel(strings.TrimSpace(v))
		}
	}
	return h
}

// isHarness reports whether cmd runs the declared harness directly:
// assignments, then `sh` or `bash`, then exactly the harness path —
// no `-c`, no wrapper script, no further argument.
func (rr *repoRun) isHarness(cmd string) bool {
	if rr.goHarness == "" {
		return false
	}
	words, simple := shellWords(cmd)
	cw, ci := commandWord(words)
	return simple && (cw == "sh" || cw == "bash") && len(words) == ci+2 && cleanRel(words[ci+1]) == rr.goHarness
}

// untrackedScan is one `git status` listing of untracked test files.
type untrackedScan struct {
	Files []string
	Err   string
}

// mergePhase brings rr onto the merged tree. It answers the incomplete
// reason ("" = the bar may run).
func (r *runner) mergePhase(ctx context.Context, rr *repoRun) string {
	m := &MergeOutcome{}
	rr.merge = m
	git := func(args ...string) execResult {
		return r.exec(ctx, rr, execSpec{Argv: append([]string{"git"}, args...), Dir: rr.dir, Cwd: rr.cwdRel, Timeout: gitStepTimeout})
	}
	fail := func(format string, a ...any) string {
		rr.merge = nil
		return fmt.Sprintf("repos/%s: ", rr.name) + fmt.Sprintf(format, a...)
	}
	if r.remaining() < time.Second {
		return fail("%s passed before the merge could start", r.deadlineNote())
	}
	// self-heal: an interrupted earlier dispatch's merge in progress
	if mergeInProgress(ctx, rr.dir) {
		if res := git("merge", "--abort"); res.Exit != 0 {
			return fail("a merge left in progress (MERGE_HEAD) could not be aborted (exit %d): %s", res.Exit, logTail(res.LogPath))
		}
		m.SelfHealed = true
	}
	if res := git("fetch", "origin"); res.Exit != 0 {
		return fail("`git fetch origin` failed (exit %d): %s — without the fetch there is no latest default branch to merge, so no merged-tree proof can exist", res.Exit, logTail(res.LogPath))
	}
	def := resolveDefault(ctx, rr.dir)
	if def == "" {
		return fail("the origin default branch is unresolvable (no refs/remotes/origin/HEAD, origin/main or origin/master after the fetch)")
	}
	m.DefaultBranch = def
	pre, err := revParse(ctx, rr.dir, "HEAD")
	if err != nil {
		return fail("HEAD is unreadable: %v", err)
	}
	m.PreHead = pre
	tip, err := revParse(ctx, rr.dir, "refs/remotes/origin/"+def)
	if err != nil {
		return fail("origin/%s is unreadable after the fetch: %v", def, err)
	}
	// the tracked-file state before the merge: a conflict's abort must
	// restore exactly it
	before, _ := gitQuiet(ctx, rr.dir, "status", "--porcelain", "--untracked-files=no")
	res := git("merge", "--no-edit", "origin/"+def)
	switch {
	case res.Exit == 0:
		head, err := revParse(ctx, rr.dir, "HEAD")
		if err != nil {
			return fail("HEAD is unreadable after the merge: %v", err)
		}
		m.Head = head
		switch head {
		case pre:
			m.Outcome = MergeUpToDate
		case tip:
			// HEAD moved onto the default tip itself (which may be a
			// merge commit of its own): nothing was merged
			m.Outcome = MergeFastForward
		default:
			m.Outcome = MergeCommit
		}
	case mergeInProgress(ctx, rr.dir):
		if ab := git("merge", "--abort"); ab.Exit != 0 {
			return fail("the conflicted merge could not be aborted (exit %d): %s", ab.Exit, logTail(ab.LogPath))
		}
		head, _ := revParse(ctx, rr.dir, "HEAD")
		after, _ := gitQuiet(ctx, rr.dir, "status", "--porcelain", "--untracked-files=no")
		if head != pre || after != before || mergeInProgress(ctx, rr.dir) {
			return fail("the conflicted merge was aborted but the pre-merge tree is not restored (HEAD %s, want %s; tracked changes %q, before %q)", short(head), short(pre), strings.TrimSpace(after), strings.TrimSpace(before))
		}
		m.Head = head
		m.Outcome = MergeConflictAborted
		m.Detail = "merging origin/" + def + " conflicts: aborted, the bar runs on the unmerged tree (the conflict is deliver's to handle)"
	default:
		return fail("`git merge --no-edit origin/%s` failed without a conflict (exit %d): %s", def, res.Exit, logTail(res.LogPath))
	}
	ahead, err := gitQuiet(ctx, rr.dir, "rev-list", "--count", "origin/"+def+"..HEAD")
	if err != nil {
		return fail("cannot count the commits beyond origin/%s: %v", def, err)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(ahead))
	m.Touched = n > 0
	rr.touched = m.Touched
	if r.o.Base != "" {
		b, err := revParse(ctx, rr.dir, r.o.Base+"^{commit}")
		if err != nil {
			return fail("--base %s is not a commit here: %v", r.o.Base, err)
		}
		m.Base = b
	} else if out, err := gitQuiet(ctx, rr.dir, "merge-base", "HEAD", "origin/"+def); err == nil {
		m.Base = strings.TrimSpace(out)
	}
	return ""
}

// mergeInProgress reports whether MERGE_HEAD exists.
func mergeInProgress(ctx context.Context, dir string) bool {
	_, err := gitQuiet(ctx, dir, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	return err == nil
}

// resolveDefault answers the origin default branch name (no remote
// prefix): what refs/remotes/origin/HEAD names — the prompt's
// `git symbolic-ref --short refs/remotes/origin/HEAD | sed 's|^origin/||'`
// — else main, else master; "" when none exists.
func resolveDefault(ctx context.Context, dir string) string {
	if out, err := gitQuiet(ctx, dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); b != "" {
			return b
		}
	}
	for _, b := range []string{"main", "master"} {
		if _, err := gitQuiet(ctx, dir, "rev-parse", "-q", "--verify", "refs/remotes/origin/"+b); err == nil {
			return b
		}
	}
	return ""
}

func revParse(ctx context.Context, dir, rev string) (string, error) {
	out, err := gitQuiet(ctx, dir, "rev-parse", "--verify", "-q", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// short abbreviates a sha for messages.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "(none)"
	}
	return sha
}

// logTail answers the last few output lines of a log (its two header
// lines skipped), joined for a one-line reason.
func logTail(logPath string) string {
	lines := readLines(logPath)
	if len(lines) > 2 {
		lines = lines[2:]
	} else {
		lines = nil
	}
	var keep []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	if len(keep) > 4 {
		keep = keep[len(keep)-4:]
	}
	if len(keep) == 0 {
		return "(no output)"
	}
	return strings.Join(keep, " | ")
}

// untrackedTestFiles lists rr's untracked files that are test files by
// the generic conventions or the repo conf's test_glob lines.
func (r *runner) untrackedTestFiles(ctx context.Context, rr *repoRun) untrackedScan {
	out, err := gitQuiet(ctx, rr.dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return untrackedScan{Err: err.Error()}
	}
	var globs []string
	if rr.plan != nil && rr.plan.report != nil {
		globs = rr.plan.report.TestGlob
	}
	var files []string
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		if e[0] == 'R' || e[0] == 'C' {
			i++ // the rename/copy source path follows
			continue
		}
		if strings.HasPrefix(e, "?? ") && isTestFile(e[3:], globs) {
			files = append(files, e[3:])
		}
	}
	sort.Strings(files)
	return untrackedScan{Files: files}
}

// genericTestBase are the stack-generic test-file name patterns.
var genericTestBase = []string{"*_test.go", "*.test.*", "*.spec.*", "test_*.py", "*_test.py"}

// isTestFile judges one repo-relative path a test file: a generic name
// pattern, a path under a test/ or tests/ directory, or a conf test_glob
// (matched like the engine's globs: against the path and its base name,
// `*` crossing `/`).
func isTestFile(p string, globs []string) bool {
	p = strings.TrimPrefix(path.Clean(p), "./")
	base := path.Base(p)
	for _, g := range genericTestBase {
		if globMatch(g, base) {
			return true
		}
	}
	parts := strings.Split(p, "/")
	for _, d := range parts[:len(parts)-1] {
		if d == "test" || d == "tests" {
			return true
		}
	}
	for _, g := range globs {
		if globMatch(g, p) || globMatch(g, base) {
			return true
		}
	}
	return false
}

// globMatch is shell `case` matching: `*` matches any run (slashes
// included), `?` one character, `[...]` a class.
func globMatch(pattern, s string) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := strings.IndexByte(pattern[i+1:], ']')
			if j < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := pattern[i+1 : i+1+j]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i += j + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(s)
}
