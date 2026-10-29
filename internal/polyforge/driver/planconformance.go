// planconformance.go: the two-way plan-conformance facts.
//
// The plan step declares the files it writes and, for each, the coupled
// obligations that must change with it (planobligations.go). Once code
// has run, the driver compares that declared set with the delivered diff
// in both directions and hands the differences to the judging gates as
// computed facts — never left for a gate to reconstruct:
//
//   - changed but undeclared (and not cited): blocking;
//   - a declared coupled obligation left unchanged: blocking;
//   - a declared plain write left unchanged: non-blocking.
//
// A review-driven code round may extend the declared set by citing the
// review finding that requires the file: the build step's
// `declared_extensions` [{path, finding_id}] output, finding_id being the
// verdict's positional `s<n>#<k>` reference. A citation counts only
// after a review→build rewind exists in the ledger and only when it is
// well formed; anything else stays undeclared. The plan is never re-run
// and is not a rewind target. The citations are cumulative over the build
// rounds since the plan: a fresh rewind resets the build
// step's live output, so the fold reads every build round archived since
// the plan's acceptance as well as the live output — a later round need
// not re-cite — and a citation read from an archived round renders marked
// as carried over.
//
// The test files an earlier authoring step declared (`output.test_files`)
// and committed — feature-full's e2e-author, bug-fix's repro; any step
// whose accepted output declares them — are declared by that step's own
// output (the plan is told not to list them), so they count as declared
// here and are never reported as changed-but-undeclared. The fold reuses
// the authored-tests gate's own fold (testfiles.go), keyed on the output
// field, never on a step name.
//
// The facts ride a `# Plan conformance (driver-computed facts)` prompt
// section on `review` (any lane with a done plan step) and on a
// `verification` whose step entry declares `plan_conformance: true`.
// The diff is git I/O, so it is read outside d.mu (the verifiedHeads
// pattern); a base that cannot be resolved or a git failure renders the
// named reason instead of a fabricated empty set (fail-open, said so).
package driver

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// declaredExtensionsKey is the build step's output field listing the
// files it added to the declared set, each citing a review finding.
const declaredExtensionsKey = "declared_extensions"

// findingRefRe is the positional review-finding reference: s<round>#<k>.
var findingRefRe = regexp.MustCompile(`^s[1-9][0-9]*#[1-9][0-9]*$`)

// PlanConformance is the driver-computed fact set one gate dispatch
// receives. Paths are repo-relative; a multi-repo work item prefixes the
// changed ones with their repo (`<repo>/<path>`).
type PlanConformance struct {
	// Unavailable names why the diff could not be read (no task repo,
	// no resolvable base, a git failure); when set, no set difference is
	// computed.
	Unavailable string
	// Obligations maps every declared write to its coupled obligations
	// (a none entry renders its reason).
	Obligations []declaredWrite
	// Undeclared is the changed-but-undeclared, uncited set (blocking).
	Undeclared []string
	// UnchangedObligations lists the declared coupled obligations the
	// diff leaves unchanged (blocking).
	UnchangedObligations []string
	// UnchangedWrites lists the declared plain writes the diff leaves
	// unchanged (non-blocking).
	UnchangedWrites []string
	// Cited lists the extensions counted as declared.
	Cited []declaredExtension
	// Ignored lists the extensions not counted, each with the reason.
	Ignored []string
}

// declaredWrite is one plan write and its coupled obligations.
type declaredWrite struct {
	Path        string
	Obligations []obligation
	NoneReason  string
}

type obligation struct {
	Path string
	Note string
}

type declaredExtension struct {
	Path      string
	FindingID string
	// Carried marks a citation read from an archived build round since
	// the plan, not from the live output.
	Carried bool
}

// planConformanceInputs is the lock-held snapshot the facts derive from.
type planConformanceInputs struct {
	plan  map[string]any
	build map[string]any // the build step's live output
	// buildRounds are the build step's outputs archived since the plan's
	// acceptance, oldest first: their citations stay counted.
	buildRounds []map[string]any
	reviewRound bool     // a review→build rewind exists in the ledger
	authored    []string // test_files an earlier authoring step declared and committed
}

// planConformanceInputsLocked snapshots, under d.mu, what stepID's
// dispatch needs for its facts; ok=false when stepID receives none:
// neither review nor a plan_conformance verification, or no done plan.
func (d *Driver) planConformanceInputsLocked(stepID string) (planConformanceInputs, bool) {
	if d.workflow == nil || d.exec == nil {
		return planConformanceInputs{}, false
	}
	spec := d.workflow.step(stepID)
	if spec == nil || !(stepID == reviewStepID || (stepID == verificationStepID && spec.PlanConformance)) {
		return planConformanceInputs{}, false
	}
	pst := d.exec.Steps[planStepID]
	if pst == nil || pst.Status != capsule.StepDone {
		return planConformanceInputs{}, false
	}
	in := planConformanceInputs{plan: pst.Output}
	build := d.workflow.buildStep()
	if bst := d.exec.Steps[build]; bst != nil {
		in.build = bst.Output
		in.buildRounds = archivedOutputsSince(bst.Attempts, acceptedAt(pst.Attempts))
	}
	for _, r := range d.exec.RewindStats.Rewinds {
		if r.Gate == reviewStepID && r.ToStep == build {
			in.reviewRound = true
		}
	}
	// The authored test files count as declared: an authoring step
	// declares them in its own output.test_files (the plan is told not
	// to list them), so the same fold the authored-tests gate uses
	// (testfiles.go — keyed on the output field, never a step name)
	// answers them for this dispatch.
	outputs := make(map[string]map[string]any, len(d.exec.Steps))
	for id, st := range d.exec.Steps {
		if st != nil && st.Status == capsule.StepDone {
			outputs[id] = st.Output
		}
	}
	if at := authoredTestsBefore(d.workflow, outputs, stepID); at != nil {
		in.authored = at.Files
	}
	return in, true
}

// acceptedAt answers when a done step's current acceptance was archived:
// its newest `accepted` attempt. The zero time when none is recorded or
// its stamp is unreadable — then no earlier era is provable and every
// archive counts.
func acceptedAt(attempts []capsule.Attempt) time.Time {
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].Cause != "accepted" {
			continue
		}
		t, _ := time.Parse(time.RFC3339Nano, attempts[i].ArchivedAt)
		return t
	}
	return time.Time{}
}

// archivedOutputsSince answers, oldest first, the outputs a step's attempt
// ledger archived at or after since: every attempt whose Result holds an
// output object (a rewind archive — the round a gate FAIL sent back, a
// superseded round, a light re-entry's self-report). An archive stamped
// before since belongs to an earlier era, and one whose stamp cannot be
// read proves no era, so neither counts.
func archivedOutputsSince(attempts []capsule.Attempt, since time.Time) []map[string]any {
	var out []map[string]any
	for _, a := range attempts {
		if len(a.Result) == 0 {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, a.ArchivedAt)
		if err != nil || at.Before(since) {
			continue
		}
		var m map[string]any
		if json.Unmarshal(a.Result, &m) != nil || m == nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

// planConformance computes stepID's facts: the snapshot under d.mu,
// the git reads outside it. Nil when stepID receives none.
func (d *Driver) planConformance(stepID string) *PlanConformance {
	d.mu.Lock()
	in, ok := d.planConformanceInputsLocked(stepID)
	d.mu.Unlock()
	if !ok {
		return nil
	}
	reposDir := ""
	if d.wi.CapsuleDir != "" {
		reposDir = filepath.Join(d.wi.CapsuleDir, capsule.DirRepos)
	}
	changed, reason := changedFiles(reposDir)
	return foldPlanConformance(in, changed, reason)
}

// changedFiles reads every task repo's committed diff against its
// origin default branch: repo name → changed repo-relative paths. A
// non-empty reason means the set could not be read.
func changedFiles(reposDir string) (map[string][]string, string) {
	names := []string(nil)
	if reposDir != "" {
		names = taskRepoNames(reposDir)
	}
	if len(names) == 0 {
		return nil, "no task repo is materialized under repos/"
	}
	out := map[string][]string{}
	for _, name := range names {
		dir := filepath.Join(reposDir, name)
		def := originDefaultRef(dir)
		if def == "" {
			return nil, fmt.Sprintf("repo %s: the fetched origin default branch (origin/HEAD, origin/main, origin/master) is unknown, so the diff base cannot be resolved", name)
		}
		base, err := repoGit(dir, "merge-base", "HEAD", def)
		if err != nil {
			return nil, fmt.Sprintf("repo %s: git merge-base HEAD %s failed: %v", name, def, err)
		}
		diff, err := repoGit(dir, "diff", "--name-only", "--no-renames", strings.TrimSpace(base)+"..HEAD")
		if err != nil {
			return nil, fmt.Sprintf("repo %s: git diff --name-only failed: %v", name, err)
		}
		for _, p := range strings.Split(diff, "\n") {
			if p = strings.TrimSpace(p); p != "" {
				out[name] = append(out[name], p)
			}
		}
	}
	return out, ""
}

// foldPlanConformance is the pure fold: declared set (plan writes, their
// obligations, the authored test files, counted extensions) against the
// changed files.
func foldPlanConformance(in planConformanceInputs, changed map[string][]string, unavailable string) *PlanConformance {
	pc := &PlanConformance{Unavailable: unavailable}
	declared := map[string]bool{}
	var writes []string
	var obligations []string
	files, _ := in.plan["touched_files"].([]any)
	for _, it := range files {
		f, _ := it.(map[string]any)
		if intent, _ := f["intent"].(string); intent != "write" {
			continue
		}
		p := cleanRepoPath(f["path"])
		if p == "" {
			continue
		}
		dw := declaredWrite{Path: p}
		list, _ := f["coupled_obligations"].([]any)
		for _, o := range list {
			om, _ := o.(map[string]any)
			if none, _ := om["none"].(bool); none {
				dw.NoneReason, _ = om["reason"].(string)
				continue
			}
			if op := cleanRepoPath(om["path"]); op != "" {
				note, _ := om["note"].(string)
				dw.Obligations = append(dw.Obligations, obligation{Path: op, Note: note})
				if !declared[op] {
					obligations = append(obligations, op)
				}
				declared[op] = true
			}
		}
		pc.Obligations = append(pc.Obligations, dw)
		if !declared[p] {
			writes = append(writes, p)
		}
		declared[p] = true
	}
	// An authoring step's committed test_files are declared by that
	// step's own output, not the plan — and they are changed by
	// construction (the committed-tests gate enforces exactly that) —
	// so they count as plain declared writes: never undeclared, never
	// an unchanged-write fact.
	for _, f := range in.authored {
		if p := cleanRepoPath(f); p != "" {
			declared[p] = true
		}
	}
	foldExtensions(pc, in, declared)
	if unavailable != "" {
		return pc
	}
	repos := make([]string, 0, len(changed))
	for name := range changed {
		repos = append(repos, name)
	}
	sort.Strings(repos)
	touched := map[string]bool{}
	for _, name := range repos {
		for _, p := range changed[name] {
			touched[p] = true
			touched[name+"/"+p] = true
			if declared[p] || declared[name+"/"+p] {
				continue
			}
			if len(repos) > 1 {
				p = name + "/" + p
			}
			pc.Undeclared = append(pc.Undeclared, p)
		}
	}
	for _, p := range obligations {
		if !touched[p] {
			pc.UnchangedObligations = append(pc.UnchangedObligations, p)
		}
	}
	for _, p := range writes {
		if !touched[p] {
			pc.UnchangedWrites = append(pc.UnchangedWrites, p)
		}
	}
	return pc
}

// foldExtensions counts the build step's finding-cited extensions into
// declared: the archived rounds since the plan first (oldest first), then
// the live output, so a later entry for the same path replaces an earlier
// one and the live citation wins. A path counts once any of its entries
// is valid; an entry that never counts is listed in Ignored (once per
// path, its latest reason), marked when it comes from an archived round.
func foldExtensions(pc *PlanConformance, in planConformanceInputs, declared map[string]bool) {
	type entry struct {
		path, fid string
		carried   bool
	}
	var entries []entry
	collect := func(out map[string]any, carried bool) {
		exts, _ := out[declaredExtensionsKey].([]any)
		for _, it := range exts {
			e, _ := it.(map[string]any)
			fid, _ := e["finding_id"].(string)
			entries = append(entries, entry{cleanRepoPath(e["path"]), strings.TrimSpace(fid), carried})
		}
	}
	for _, out := range in.buildRounds {
		collect(out, true)
	}
	collect(in.build, false)
	cited := map[string]int{} // path → its index in pc.Cited
	ignored := map[string]string{}
	var ignoredOrder []string
	for _, e := range entries {
		reason := ""
		switch {
		case e.path == "":
			reason = fmt.Sprintf("an entry without a path (finding_id %q)", e.fid)
		case !in.reviewRound:
			reason = fmt.Sprintf("`%s` (finding_id %q): no review FAIL has rewound to the build step, so there is no finding to cite", e.path, e.fid)
		case !findingRefRe.MatchString(e.fid):
			reason = fmt.Sprintf("`%s` (finding_id %q): not a well-formed review finding reference s<n>#<k>", e.path, e.fid)
		}
		if reason != "" {
			if _, ok := cited[e.path]; ok && e.path != "" {
				continue // the path already counts
			}
			if e.carried {
				reason += " (an earlier build round's entry)"
			}
			key := e.path
			if key == "" {
				key = reason // pathless entries dedupe by their text
			}
			if _, seen := ignored[key]; !seen {
				ignoredOrder = append(ignoredOrder, key)
			}
			ignored[key] = reason
			continue
		}
		ext := declaredExtension{Path: e.path, FindingID: e.fid, Carried: e.carried}
		if i, ok := cited[e.path]; ok {
			pc.Cited[i] = ext
		} else {
			cited[e.path] = len(pc.Cited)
			pc.Cited = append(pc.Cited, ext)
		}
		delete(ignored, e.path)
		declared[e.path] = true
	}
	for _, k := range ignoredOrder {
		if r, ok := ignored[k]; ok {
			pc.Ignored = append(pc.Ignored, r)
		}
	}
}

// cleanRepoPath normalizes a declared path to the repo-relative form the
// diff names ("./x" and "repos/<repo>/x" spellings reduce to "x" or
// "<repo>/x"); "" for a non-string or empty value.
func cleanRepoPath(v any) string {
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = filepath.ToSlash(filepath.Clean(s))
	s = strings.TrimPrefix(s, "repos/")
	return s
}

// planConformanceHeading is the prompt section's heading.
const planConformanceHeading = "# Plan conformance (driver-computed facts)"

// renderPlanConformance renders the facts as the prompt section ("" for
// nil). Every set-difference line carries its severity.
func renderPlanConformance(pc *PlanConformance) string {
	if pc == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n" + planConformanceHeading + "\n\n")
	b.WriteString("The driver compared the plan's declared set (its `touched_files` writes, their `coupled_obligations`, the build step's finding-cited `declared_extensions`, cumulative over its rounds since the plan, and the test files an earlier authoring step declared and committed) with the delivered diff (each task repo's `git diff --name-only $(git merge-base HEAD <origin default>)..HEAD`). These are facts, not suggestions; the severities are the owner's rule and hold in every round. Whether the listed obligations are the RIGHT ones, and whether a citation really requires its file, stays your judgment.\n")
	if pc.Unavailable != "" {
		fmt.Fprintf(&b, "\nThe diff could not be read, so no set difference was computed: %s. Judge conformance from the diff yourself and say so in your verdict.\n", pc.Unavailable)
	} else {
		b.WriteString("\n## Changed but not declared (blocking unless a finding is cited)\n\n")
		writeFactList(&b, pc.Undeclared, "blocking: changed but undeclared, no review-finding citation")
		b.WriteString("\n## Declared coupled obligations left unchanged (blocking)\n\n")
		writeFactList(&b, pc.UnchangedObligations, "blocking: declared coupled obligation left unchanged")
		b.WriteString("\n## Declared plain writes left unchanged (non-blocking)\n\n")
		writeFactList(&b, pc.UnchangedWrites, "non-blocking: declared write left unchanged")
	}
	b.WriteString("\n## Finding-cited extensions (count as declared)\n\n")
	if len(pc.Cited) == 0 {
		b.WriteString("- none\n")
	}
	for _, e := range pc.Cited {
		carried := ""
		if e.Carried {
			carried = " (carried over: cited in an earlier build round since the plan, not re-cited in this one)"
		}
		fmt.Fprintf(&b, "- `%s` — cites review finding %s%s\n", e.Path, e.FindingID, carried)
	}
	if len(pc.Ignored) > 0 {
		b.WriteString("\nExtensions NOT counted (treated as not declared):\n\n")
		for _, s := range pc.Ignored {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	b.WriteString("\n## Declared writes and their coupled obligations\n\n")
	if len(pc.Obligations) == 0 {
		b.WriteString("- none\n")
	}
	for _, w := range pc.Obligations {
		switch {
		case len(w.Obligations) > 0:
			parts := make([]string, 0, len(w.Obligations))
			for _, o := range w.Obligations {
				if o.Note != "" {
					parts = append(parts, fmt.Sprintf("`%s` (%s)", o.Path, o.Note))
				} else {
					parts = append(parts, fmt.Sprintf("`%s`", o.Path))
				}
			}
			fmt.Fprintf(&b, "- `%s` → %s\n", w.Path, strings.Join(parts, ", "))
		case w.NoneReason != "":
			fmt.Fprintf(&b, "- `%s` → none: %s\n", w.Path, w.NoneReason)
		default:
			fmt.Fprintf(&b, "- `%s` → no obligation entry\n", w.Path)
		}
	}
	return b.String()
}

func writeFactList(b *strings.Builder, paths []string, label string) {
	if len(paths) == 0 {
		b.WriteString("- none\n")
		return
	}
	for _, p := range paths {
		fmt.Fprintf(b, "- `%s` — %s\n", p, label)
	}
}
