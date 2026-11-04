package driver

// fast_lane.go: the machine boundary of a verification-less delivery lane
// (the shipped `micro` bundle,). Such a lane has a `deliver`
// step and no `verification` step, so its merge credential is the review
// pass plus the delivery's own re-run of the repository's fast evidence
// (deliver_facts.go). That narrowed credential is only sound for a change
// the repository itself declared text-only, so before the step after the
// build step is dispatched the driver judges the build step's diff:
//
//   - per task repo with commits beyond its merge-base with the fetched
//     origin default branch, the governing declaration is the
//     .polyforge/verify-scope.conf at that base (`git show`), never the
//     working tree's — a change cannot widen its own whitelist. Its
//     micro_path= lines are the opt-in; a repo that declares none, an
//     unreadable base, conf or diff, all refuse (fail closed);
//   - every changed path, both sides of a rename or copy, must match one
//     declared micro_path (microGlobMatch: `*` within one segment, `**`
//     across `/`, no wildcard = that exact path); AGENTS.md and CLAUDE.md
//     (basename, any directory) are out of bounds whatever matches them;
//   - each repo is judged against its own declaration (no union);
//   - the credential plan is the build step's reported evidence for the
//     repo (its `evidence: [{repo, command}]`, in order), followed by every
//     declared micro_check= it omitted. An empty plan refuses.
//
// The verdict is written by one transition (TransFastLane) onto the build
// step's output under fastLaneKey — an idempotent overwrite recomputed from
// git on every pass, so a crash between the facts and the write simply
// recomputes them. A refused diff ends the workflow failed_gate with
// Execution.TerminationReason naming the re-file lane: no rewind, no merge,
// no human question. No repository, path or stack is special-cased: the
// only inputs are the workflow's shape and each repository's own conf.

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// fastLaneKey is the driver-written key of the build step's output on a
// verification-less delivery lane: the out-of-bounds verdict and the
// delivery's credential plan (fastLanePlan).
const fastLaneKey = "fast_lane"

// microPathKey / microCheckKey are the two conf keys the driver reads
// itself (the ci_evidence precedent): the opt-in whitelist and the extra
// evidence commands.
const (
	microPathKey  = "micro_path"
	microCheckKey = "micro_check"
)

// fastLaneRefileLane is the lane an out-of-bounds change is re-filed on.
const fastLaneRefileLane = "autopilot"

// microHardExcluded are the agent-contract basenames no declaration can
// admit: the project rules every agent reads.
var microHardExcluded = []string{"AGENTS.md", "CLAUDE.md"}

// fastLaneRepo is one changed repo's judged facts and its credential plan.
type fastLaneRepo struct {
	Name          string       `json:"name"`
	Base          string       `json:"base"`
	ChangedPaths  []string     `json:"changed_paths"`
	DeclaredPaths []string     `json:"declared_paths"`
	Commands      []testBarCmd `json:"commands"`
}

// fastLanePlan is the recorded verdict (fastLaneKey).
type fastLanePlan struct {
	InBounds bool           `json:"in_bounds"`
	Reason   string         `json:"reason,omitempty"`
	Repos    []fastLaneRepo `json:"repos"`
}

// verificationLess answers whether w is a verification-less delivery lane:
// it delivers (a `deliver` step) and has no `verification` step. The
// shape, never a bundle name, selects the fast-lane guard and merge rule.
func verificationLess(w *Workflow) bool {
	return w != nil && w.step(deliverStepID) != nil && w.step(verificationStepID) == nil
}

// microDecls parses the two micro keys of a conf (the engine's line
// rules: leading whitespace and `#` comments ignored, a value runs to the
// end of its line). A micro_path that is absolute, `./`-prefixed, blank or
// `..`-escaping contributes nothing, as does an empty micro_check or one
// that begins with a command substitution (the engine's lint).
func microDecls(conf string) (paths, checks []string) {
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimLeft(line, " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case microPathKey:
			val = strings.TrimRight(val, " \t\r")
			if !microPathValid(val) || slices.Contains(paths, val) {
				continue
			}
			paths = append(paths, val)
		case microCheckKey:
			val = strings.TrimRight(val, "\r")
			if strings.TrimSpace(val) == "" || strings.HasPrefix(val, "$(") || strings.HasPrefix(val, "`") {
				continue
			}
			checks = append(checks, val)
		}
	}
	return paths, checks
}

// microPathValid: a repository-relative pattern (not absolute, not
// `./`-prefixed, not blank, never escaping with `..`).
func microPathValid(p string) bool {
	if strings.TrimSpace(p) == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "./") {
		return false
	}
	return !slices.Contains(strings.Split(p, "/"), "..")
}

// microGlobMatch matches a repository-relative, slash-separated path
// against a micro_path pattern, segment by segment: `**` is zero or more
// whole segments, any other segment is a path.Match pattern (so `*` never
// crosses `/`); a pattern without wildcards matches exactly that path.
func microGlobMatch(pattern, p string) bool {
	return globSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func globSegments(ps, xs []string) bool {
	if len(ps) == 0 {
		return len(xs) == 0
	}
	if ps[0] == "**" {
		if globSegments(ps[1:], xs) {
			return true
		}
		return len(xs) > 0 && globSegments(ps, xs[1:])
	}
	if len(xs) == 0 {
		return false
	}
	ok, err := path.Match(ps[0], xs[0])
	return err == nil && ok && globSegments(ps[1:], xs[1:])
}

// microHardExcludedPath: the path's basename is an agent-contract file.
func microHardExcludedPath(p string) bool {
	return slices.Contains(microHardExcluded, path.Base(p))
}

// changedPathsZ parses `git diff --name-status -z` output into the
// changed paths, both sides of a rename or copy, in order, unique.
func changedPathsZ(out string) []string {
	f := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var paths []string
	add := func(p string) {
		if p != "" && !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	for i := 0; i < len(f); i++ {
		status := f[i]
		if status == "" {
			continue
		}
		n := 1
		if status[0] == 'R' || status[0] == 'C' {
			n = 2
		}
		for j := 0; j < n && i+1 < len(f); j++ {
			i++
			add(f[i])
		}
	}
	return paths
}

// fastLaneEvidence reads the build step's reported evidence list
// (`evidence: [{repo, command}]`); anything else is no evidence.
func fastLaneEvidence(out map[string]any) []testBarCmd {
	var ev []testBarCmd
	if raw, err := json.Marshal(out["evidence"]); err == nil {
		_ = json.Unmarshal(raw, &ev)
	}
	var kept []testBarCmd
	for _, e := range ev {
		e.Repo, e.Command = strings.TrimSpace(e.Repo), strings.TrimSpace(e.Command)
		if e.Repo != "" && e.Command != "" {
			kept = append(kept, e)
		}
	}
	return kept
}

// fastLaneOf reads the recorded plan back from the build step's output
// (in memory or reloaded from execution.json); nil when none is recorded.
func fastLaneOf(out map[string]any) *fastLanePlan {
	v, ok := out[fastLaneKey]
	if !ok {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var p fastLanePlan
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return &p
}

// fastLaneVerdict judges every task repo under reposDir (git reads only;
// the caller holds no lock) and assembles the credential plan from the
// build step's reported evidence.
func fastLaneVerdict(reposDir string, evidence []testBarCmd) fastLanePlan {
	plan := fastLanePlan{Repos: []fastLaneRepo{}}
	var problems []string
	refuse := func(repo, why string) { problems = append(problems, "repos/"+repo+": "+why) }
	if reposDir == "" {
		problems = append(problems, "the capsule's task repos are unknown")
	}
	for _, name := range taskRepoNames(reposDir) {
		dir := filepath.Join(reposDir, name)
		def := originDefaultRef(dir)
		if def == "" {
			refuse(name, "the fetched origin default branch is unknown, so the diff cannot be read")
			continue
		}
		baseOut, err := repoGit(dir, "merge-base", "HEAD", def)
		if err != nil {
			refuse(name, "the diff base (merge-base HEAD "+strings.TrimPrefix(def, "refs/remotes/")+") is unreadable")
			continue
		}
		base := strings.TrimSpace(baseOut)
		commits, err := repoGit(dir, "rev-list", base+"..HEAD")
		if err != nil {
			refuse(name, "the history beyond the diff base is unreadable")
			continue
		}
		if strings.TrimSpace(commits) == "" {
			continue // not touched by this change
		}
		diff, err := repoGit(dir, "diff", "--name-status", "-z", "-M", base+"..HEAD")
		if err != nil {
			refuse(name, "the diff "+shortSHA(base)+"..HEAD is unreadable")
			continue
		}
		changed := changedPathsZ(diff)
		conf, err := repoGit(dir, "show", base+":"+verifyScopeConf)
		if err != nil {
			refuse(name, "no "+verifyScopeConf+" is readable at the diff base "+shortSHA(base)+", so the repository declares no micro_path (it is not opted in)")
			continue
		}
		declared, checks := microDecls(conf)
		if len(declared) == 0 {
			refuse(name, "its "+verifyScopeConf+" at the diff base declares no micro_path (the repository is not opted in)")
			continue
		}
		var out []string
		for _, p := range changed {
			switch {
			case microHardExcludedPath(p):
				out = append(out, p+" (an agent-contract file, never in bounds)")
			case !slices.ContainsFunc(declared, func(g string) bool { return microGlobMatch(g, p) }):
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			refuse(name, "changed path(s) outside its declared micro_path "+fmt.Sprintf("%q", declared)+": "+strings.Join(out, ", "))
			continue
		}
		var cmds []testBarCmd
		for _, e := range evidence {
			if e.Repo == name {
				cmds = append(cmds, e)
			}
		}
		for _, c := range checks {
			if !slices.ContainsFunc(cmds, func(e testBarCmd) bool { return e.Command == strings.TrimSpace(c) }) {
				cmds = append(cmds, testBarCmd{Repo: name, Command: strings.TrimSpace(c)})
			}
		}
		if len(cmds) == 0 {
			refuse(name, "no fast evidence command is reported or declared for it, so the delivery has nothing to re-run")
			continue
		}
		plan.Repos = append(plan.Repos, fastLaneRepo{Name: name, Base: base, ChangedPaths: changed, DeclaredPaths: declared, Commands: cmds})
	}
	if len(problems) == 0 && len(plan.Repos) == 0 {
		problems = append(problems, "the change touches no task repository")
	}
	if len(problems) > 0 {
		plan.Repos = []fastLaneRepo{}
		plan.Reason = "out of bounds for this verification-less lane: " + strings.Join(problems, "; ") +
			". The run ends here without a rewind or a merge; re-file the work on the " + fastLaneRefileLane + " lane."
		return plan
	}
	plan.InBounds = true
	return plan
}

// fastLanePlanCommands flattens a recorded in-bounds plan into the
// delivery's credential commands.
func fastLanePlanCommands(p *fastLanePlan) []testBarCmd {
	if p == nil || !p.InBounds {
		return nil
	}
	var out []testBarCmd
	for _, r := range p.Repos {
		out = append(out, r.Commands...)
	}
	return out
}
