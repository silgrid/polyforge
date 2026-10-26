package driver

// deliver.go: the delivery binding (polyforge#166). The `deliver` step
// that ends feature-full, bug-fix, autopilot and chore pushes the
// feature branch, opens or reuses the PR and merges it. GitHub branch
// protection is not enforced on the owner's repos, so "never push the
// default branch" rests on three layers: the step contract, the work
// machine's global pre-push hook (core.hooksPath, installed outside this
// repo; it refuses main/master pushes to GitHub unless
// PF_ALLOW_MAIN_PUSH=1), and THIS mechanical check over the step's
// recorded command evidence. Like the V4 evidence gate it judges what
// the step reports — it catches a step that broke the rule and said so,
// not a lying agent. It reads `git push` and `gh pr merge` in command
// position only (a PR title that mentions them is not a command); a
// merge through `gh api` is outside what it reads.
//
// The binding has two halves:
//
//  1. command rule (every delivered outcome that carries evidence): a
//     recorded `git push` must name an explicit refspec whose
//     destination is the work item's feature branch
//     polyforge/<capsule directory name> — no implicit/default-branch
//     push, no force, no deletion, no --all/--mirror/--tags, no tag
//     refspec, no --no-verify or core.hooksPath override — and no
//     command may set PF_ALLOW_MAIN_PUSH (the pre-push hook's human
//     override). A recorded `gh pr merge` must use
//     --merge (never --squash/--rebase/--admin): release.sh's PR-only
//     check expects PR merge commits on the default branch.
//  2. merge rule (a completed pass): every task repo under repos/ has
//     an entry; every delivered (not skipped) repo names its
//     branch and PR and is either merged (with its merge commit) or not
//     (with the reason). A merge is automatic only when the independent
//     review passed with no blocking finding (ResultSpec.ReviewPassed),
//     the repo's tests passed and no conflict was LLM-resolved; any
//     other merge, and every repo left unmerged, is a human's decision:
//     the completed result is refused unless a human answered in this
//     attempt chain (the conditional form of the #138 human-gate
//     guarantee: like it, it counts the deliver step's own answers since
//     its last rewind, not which question they answered).
//
// The binding keys on the step id `deliver` (and reads the step named
// `review`), the same convention as `verification` and the release
// bundle's `release`/`deploy`.

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// deliverStepID is the step id of the delivery step the four delivery
// bundles end with.
const deliverStepID = "deliver"

// deliverBranchPrefix is the feature-branch namespace the deliver step
// pushes to: polyforge/<project>-<seq> (the capsule directory name).
const deliverBranchPrefix = "polyforge/"

// deliverBranch answers the work item's feature branch, derived from
// the capsule directory (<capsule>/repos is ReposDir); "" when the
// capsule is unknown (the check then accepts any polyforge/* branch).
func deliverBranch(spec ResultSpec) string {
	if spec.ReposDir == "" {
		return ""
	}
	name := filepath.Base(filepath.Dir(spec.ReposDir))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	return deliverBranchPrefix + name
}

// deliverBinding judges one deliver envelope (any outcome): the command
// rule on its decoded evidence, then — for a completed pass — the merge
// rule.
func deliverBinding(res *StepResult, evidence []CommandEvidence, delivered Outcome, spec ResultSpec) error {
	branch := deliverBranch(spec)
	if err := deliverCommandRule(evidence, branch); err != nil {
		return fmt.Errorf("driver: step %q recorded a forbidden delivery command: %w", spec.StepID, err)
	}
	if delivered != OutcomeCompleted || res.Outcome != OutcomeCompleted {
		return nil
	}
	return deliverMergeRule(res, evidence, branch, spec)
}

// deliverRepo is the per-repo record of the deliver output.
type deliverRepo struct {
	Name                string `json:"name"`
	SkippedReason       string `json:"skipped_reason"`
	Branch              string `json:"branch"`
	PRURL               string `json:"pr_url"`
	Merged              *bool  `json:"merged"`
	MergeCommit         string `json:"merge_commit"`
	NotMergedReason     string `json:"not_merged_reason"`
	LLMResolvedConflict bool   `json:"llm_resolved_conflict"`
	TestsPassed         *bool  `json:"tests_passed"`
}

func deliverMergeRule(res *StepResult, evidence []CommandEvidence, branch string, spec ResultSpec) error {
	var out struct {
		Verdict any           `json:"verdict"`
		Repos   []deliverRepo `json:"repos"`
	}
	if len(res.Output) == 0 || json.Unmarshal(res.Output, &out) != nil {
		return fmt.Errorf("driver: deliver output must be an object with verdict and repos")
	}
	if v, _ := out.Verdict.(string); strings.ToLower(strings.TrimSpace(v)) != "pass" {
		return fmt.Errorf("driver: a completed deliver owes verdict \"pass\" (got %v) — a human's \"back to code\" is gate_failed with verdict fail", out.Verdict)
	}
	seen := map[string]bool{}
	for _, r := range out.Repos {
		seen[strings.TrimSpace(r.Name)] = true
	}
	for _, name := range taskRepoNames(spec.ReposDir) { // testfiles.go: the carrier inventory
		if !seen[name] {
			return fmt.Errorf("driver: deliver output has no repos entry for declared repo %q — one entry per repos/<name> (skipped_reason when it has nothing to deliver)", name)
		}
	}
	var human []string
	for i, r := range out.Repos {
		label := fmt.Sprintf("repos[%d] (%s)", i, r.Name)
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("driver: deliver output repos[%d] has no name", i)
		}
		if strings.TrimSpace(r.SkippedReason) != "" {
			if err := checkNothingToDeliver(spec.ReposDir, r.Name); err != nil {
				return fmt.Errorf("driver: deliver output %s is skipped but %w", label, err)
			}
			continue
		}
		if err := checkDeliverBranch(r.Branch, branch); err != nil {
			return fmt.Errorf("driver: deliver output %s: %w", label, err)
		}
		if r.Merged == nil {
			return fmt.Errorf("driver: deliver output %s must say merged true or false", label)
		}
		if !*r.Merged {
			if strings.TrimSpace(r.NotMergedReason) == "" {
				return fmt.Errorf("driver: deliver output %s is not merged and owes a not_merged_reason", label)
			}
			human = append(human, r.Name+": left unmerged")
			continue
		}
		if strings.TrimSpace(r.PRURL) == "" || strings.TrimSpace(r.MergeCommit) == "" {
			return fmt.Errorf("driver: deliver output %s is merged and owes its pr_url and merge_commit", label)
		}
		if why := mergeBlocker(spec, r, branch, evidence); why != "" {
			human = append(human, r.Name+": merged although "+why)
		}
	}
	if len(human) > 0 && spec.HumanAnswers <= 0 {
		return fmt.Errorf("driver: step %q needs a human decision (%s): return needs_input with the PR links and act on the human's answer — a completed result is refused until a human has answered in this attempt",
			spec.StepID, strings.Join(human, "; "))
	}
	return nil
}

// checkDeliverBranch: dst must be the feature branch (exactly, when the
// capsule names it; else any polyforge/* branch that is not a reserved
// default-branch name).
func checkDeliverBranch(dst, branch string) error {
	b := strings.TrimPrefix(strings.TrimSpace(dst), "refs/heads/")
	if branch != "" {
		if b != branch {
			return fmt.Errorf("branch %q is not the work item's feature branch %q", dst, branch)
		}
		return nil
	}
	if !strings.HasPrefix(b, deliverBranchPrefix) || len(b) == len(deliverBranchPrefix) {
		return fmt.Errorf("branch %q is not a %s* feature branch", dst, deliverBranchPrefix)
	}
	return nil
}

// deliverCommandRule judges every recorded command for git push and gh
// pr merge invocations and the main-push override. An argv list is one
// simple command (exec'd as is); a one-word argv is a command line.
func deliverCommandRule(ev []CommandEvidence, branch string) error {
	for i, e := range ev {
		var err error
		if len(e.Argv) == 1 {
			err = checkScript(e.Argv[0], branch, 0)
		} else {
			err = checkSegment(e.Argv, branch, 0)
		}
		if err != nil {
			return fmt.Errorf("command_evidence[%d]: %w", i, err)
		}
	}
	return nil
}

// checkScript judges every simple command of one command line.
func checkScript(script, branch string, depth int) error {
	for _, seg := range shellSegments(script) {
		if err := checkSegment(seg, branch, depth); err != nil {
			return err
		}
	}
	return nil
}

// shellSegments splits a command line into simple-command word lists:
// quotes are stripped, and ; & | newline ( ) end a segment. It is a
// scanner for the two command shapes the rule judges, not a shell.
func shellSegments(s string) [][]string {
	var segs [][]string
	var cur []string
	var word strings.Builder
	inWord := false
	var quote rune
	flushWord := func() {
		if inWord {
			cur = append(cur, word.String())
			word.Reset()
			inWord = false
		}
	}
	flushSeg := func() {
		flushWord()
		if len(cur) > 0 {
			segs = append(segs, cur)
			cur = nil
		}
	}
	rs := []rune(s)
	for k, r := range rs {
		switch {
		case r == '&' && quote == 0 && (k > 0 && (rs[k-1] == '>' || rs[k-1] == '<') || k+1 < len(rs) && rs[k+1] == '>'):
			word.WriteRune(r) // a redirection (2>&1, &>file), not a separator
			inWord = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			flushWord()
		case r == ';' || r == '&' || r == '|' || r == '\n' || r == '(' || r == ')' || r == '`':
			flushSeg()
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	flushSeg()
	return segs
}

// mainPushOverride is the human-only override of the machine's global
// pre-push hook (core.hooksPath, installed outside this repo on the
// owner's work machine), which refuses main/master pushes to GitHub.
const mainPushOverride = "PF_ALLOW_MAIN_PUSH"

// commandWrappers run their arguments as the command (their own flags
// are skipped); shellKeywords open a compound command.
var (
	commandWrappers = map[string]bool{"env": true, "command": true, "exec": true, "sudo": true, "nohup": true, "time": true, "timeout": true}
	shellKeywords   = map[string]bool{"if": true, "then": true, "else": true, "elif": true, "do": true, "while": true, "until": true, "!": true, "{": true}
	shells          = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true}
)

// isAssignment answers whether w is a NAME=value word.
func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// checkSegment judges one simple command by its command word (after
// NAME=value assignments, shell keywords and wrappers such as env):
// git push, gh pr merge, the main-push override, and the script of a
// shell -c wrapper (a few levels deep).
func checkSegment(seg []string, branch string, depth int) error {
	seg = dropRedirections(seg)
	i := 0
	var gitEnv []string
	for i < len(seg) {
		w := seg[i]
		switch {
		case isAssignment(w):
			if strings.HasPrefix(w, mainPushOverride+"=") {
				return fmt.Errorf("(%s): sets %s (the pre-push hook's human-only override)", strings.Join(seg, " "), mainPushOverride)
			}
			if strings.HasPrefix(w, "GIT_") && !allowedPushEnv[strings.SplitN(w, "=", 2)[0]] {
				gitEnv = append(gitEnv, w)
			}
			i++
		case shellKeywords[w] || commandWrappers[filepath.Base(w)]:
			i++
			if commandWrappers[filepath.Base(w)] {
				for i < len(seg) && strings.HasPrefix(seg[i], "-") {
					i++
				}
			}
		default:
			return checkCommand(seg, i, branch, depth, gitEnv)
		}
	}
	return nil
}

// dropRedirections removes shell redirections (>file, 2>&1, &>file,
// < file, and the target word of a bare operator) from a simple command.
func dropRedirections(seg []string) []string {
	out := make([]string, 0, len(seg))
	for k := 0; k < len(seg); k++ {
		w := seg[k]
		op := strings.TrimLeft(w, "0123456789")
		if strings.HasPrefix(w, "&>") {
			op = w
		}
		if !strings.HasPrefix(op, ">") && !strings.HasPrefix(op, "<") && !strings.HasPrefix(op, "&>") {
			out = append(out, w)
			continue
		}
		if strings.Trim(op, "<>&") == "" && k+1 < len(seg) {
			k++ // bare operator: its target is the next word
		}
	}
	return out
}

func checkCommand(seg []string, i int, branch string, depth int, gitEnv []string) error {
	where := func(err error) error { return fmt.Errorf("(%s): %w", strings.Join(seg, " "), err) }
	args := seg[i+1:]
	switch filepath.Base(seg[i]) {
	case "export":
		for _, a := range args {
			if a == mainPushOverride || strings.HasPrefix(a, mainPushOverride+"=") {
				return where(fmt.Errorf("exports %s (the pre-push hook's human-only override)", mainPushOverride))
			}
		}
	case "git":
		j, opts := gitSubcommand(args)
		hooksOff := false
		for _, o := range opts {
			hooksOff = hooksOff || o[0] == "-c" && strings.HasPrefix(strings.ToLower(o[1]), "core.hookspath")
		}
		if j < len(args) && args[j] == "push" {
			if len(gitEnv) > 0 {
				return where(fmt.Errorf("git push under %s (git configuration must not change for the push)", strings.Join(gitEnv, " ")))
			}
			if hooksOff {
				return where(errors.New("git push with core.hooksPath overridden (it skips the pre-push hook)"))
			}
			if err := checkPush(args[j+1:], branch); err != nil {
				return where(err)
			}
		}
	case "gh":
		if mergeArgs, ok := ghPRMergeArgs(args); ok {
			if err := checkGHMerge(mergeArgs); err != nil {
				return where(err)
			}
		}
		j := ghSubcommand(args)
		if j+1 < len(args) && args[j] == "repo" && args[j+1] == "sync" {
			return where(errors.New("gh repo sync is forbidden in delivery (it can move the default branch)"))
		}
	default:
		if !shells[filepath.Base(seg[i])] || depth >= 3 {
			return nil
		}
		for j, a := range args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") && j+1 < len(args) {
				return checkScript(args[j+1], branch, depth+1)
			}
		}
	}
	return nil
}

// ghSubcommand answers the index of the subcommand in gh's arguments
// (the words after `gh`), past its global flags.
func ghSubcommand(args []string) int {
	j := 0
	for j < len(args) && strings.HasPrefix(args[j], "-") {
		if args[j] == "-R" || args[j] == "--repo" {
			j++
		}
		j++
	}
	return j
}

// ghPRMergeArgs answers whether gh's arguments are `pr merge`, and the
// arguments after it.
func ghPRMergeArgs(args []string) ([]string, bool) {
	j := ghSubcommand(args)
	if j+1 < len(args) && args[j] == "pr" && args[j+1] == "merge" {
		return args[j+2:], true
	}
	return nil, false
}

// gitSubcommand answers the index of the subcommand in git's arguments
// (the words after `git`) and the global options before it that take a
// separate value, as {option, value} pairs in order.
func gitSubcommand(args []string) (int, [][2]string) {
	j := 0
	var opts [][2]string
	for j < len(args) && strings.HasPrefix(args[j], "-") {
		switch args[j] {
		case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--config-env":
			if j+1 < len(args) {
				opts = append(opts, [2]string{args[j], args[j+1]})
			}
			j++
		}
		j++
	}
	return j, opts
}

// checkPush: an explicit refspec to the feature branch, nothing that
// forces, deletes or pushes other refs.
func checkPush(args []string, branch string) error {
	var pos []string
	for k := 0; k < len(args); k++ {
		a := args[k]
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		name, _, _ := strings.Cut(a, "=")
		if f := forbiddenPushOption(name); f != "" {
			return fmt.Errorf("git push %s (%s) is forbidden in delivery", name, f)
		}
		switch name {
		case "-o", "--push-option":
			if !strings.Contains(a, "=") {
				k++ // the option's value
			}
			continue
		}
		if !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], "fd") {
			return fmt.Errorf("git push %s (force/delete) is forbidden in delivery", a)
		}
	}
	if len(pos) < 2 {
		return errors.New("git push without an explicit refspec — push exactly HEAD:refs/heads/" + orPrefix(branch))
	}
	for _, rs := range pos[1:] {
		if strings.HasPrefix(rs, "+") {
			return fmt.Errorf("forced refspec %q", rs)
		}
		src, dst, colon := strings.Cut(rs, ":")
		if !colon {
			dst = src
		} else if src == "" {
			return fmt.Errorf("deleting refspec %q", rs)
		}
		if err := checkDeliverBranch(dst, branch); err != nil {
			return fmt.Errorf("refspec %q: %w", rs, err)
		}
	}
	return nil
}

// allowedPushEnv are the GIT_* assignments a push line may carry (they
// change how git talks to the remote, not what it pushes or which
// configuration and hooks apply); every other GIT_* assignment is refused.
var allowedPushEnv = map[string]bool{"GIT_TERMINAL_PROMPT": true, "GIT_SSH_COMMAND": true}

// forbiddenPushOptions are refused by name or by any abbreviation git
// would accept for them (git takes unambiguous long-option prefixes).
var forbiddenPushOptions = []string{"--force", "--force-with-lease", "--force-if-includes", "--all", "--branches",
	"--mirror", "--tags", "--follow-tags", "--delete", "--prune", "--no-verify", "--repo", "--receive-pack", "--exec"}

func forbiddenPushOption(name string) string {
	if !strings.HasPrefix(name, "--") || len(name) < 3 {
		return ""
	}
	for _, f := range forbiddenPushOptions {
		if strings.HasPrefix(f, name) {
			return f
		}
	}
	return ""
}

func orPrefix(branch string) string {
	if branch != "" {
		return branch
	}
	return deliverBranchPrefix + "<project>-<seq>"
}

// checkGHMerge: the PR merges with a merge commit, never squash, rebase
// or an admin bypass.
func checkGHMerge(args []string) error {
	merge := false
	for _, a := range args {
		name, val, hasVal := strings.Cut(a, "=")
		if hasVal && (strings.EqualFold(val, "false") || val == "0") {
			continue // --flag=false turns the flag off
		}
		switch name {
		case "--merge", "-m":
			merge = true
		case "--squash", "-s", "--rebase", "-r", "--admin", "--auto":
			return fmt.Errorf("gh pr merge %s is forbidden — merge with --merge", a)
		}
	}
	if !merge {
		return errors.New("gh pr merge without --merge — merge with --merge")
	}
	return nil
}

// reviewStepID is the conventional id of the independent review gate
// (feature-full, bug-fix, chore; autopilot has none).
const reviewStepID = "review"

// reviewPassed answers whether the review step is done with a pass
// verdict and no finding of severity blocking (nil step: no review).
func reviewPassed(st *capsule.Step) bool {
	if st == nil || st.Status != capsule.StepDone {
		return false
	}
	v, _ := st.Output["verdict"].(string)
	if strings.ToLower(strings.TrimSpace(v)) != "pass" {
		return false
	}
	findings, _ := st.Output["findings"].([]any)
	for _, f := range findings {
		if m, ok := f.(map[string]any); ok {
			if sev, _ := m["severity"].(string); strings.EqualFold(strings.TrimSpace(sev), "blocking") {
				return false
			}
		}
	}
	return true
}
