package driver

// lightrecheck.go: the mechanical fast lane's light side (polyforge#260) —
// the light recheck's PREPARATION (the bound coverage validation and the
// deterministic repair-diff measurement, both pure functions over
// execution.json state plus git reads that never run under a lock), the
// pre-dispatch escalation fallback, and the light-pass settlement's pure
// judgments. The pending light recheck marker
// (steps.verification.pending_light_recheck) is the mode switch on both
// sides: its presence makes the verification step's next dispatch the
// light recheck (the prompt carries the light section, the light output
// schema validates) and its settlement the light one (the mechanical_pass
// record, no verified_heads, the carried test_bar).
//
// The driver classifies nothing and judges staticness nowhere (spec
// non-goal): the classification is the verification step agent's own
// verdict field (mechanical_bound), and the driver mechanically enforces
// only the bound's coverage and the repair diff's shape — deterministic
// checks, fail-closed into the one fallback (in-place escalation to the
// existing full verification).

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The hard-coded repair caps (spec non-goal: no configuration — they are
// driver constants, never knobs).
const (
	// maxMechanicalFixLines bounds the repair diff's changed lines
	// (added + deleted), summed across repos.
	maxMechanicalFixLines = 40
	// maxMechanicalFixFiles bounds the repair diff's changed files,
	// summed across repos.
	maxMechanicalFixFiles = 5
)

// lightDiff is the measured repair diff: changed files and changed lines
// (added + deleted), summed across repos.
type lightDiff struct {
	Files int
	Lines int
}

// mechBound is one mechanical bound entry: per failed check its repo,
// its exact command and the files it named (repo-relative).
type mechBound struct {
	Check   string   `json:"check"`
	Repo    string   `json:"repo"`
	Command string   `json:"command"`
	Files   []string `json:"files"`
}

// parseMechBound answers the marker's bound as parsed entries. A bound
// that is absent, empty, not a list, or not a list of well-shaped
// objects is malformed — the caller escalates (the classification is
// the agent's own; the driver only judges its shape).
func parseMechBound(raw json.RawMessage) ([]mechBound, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("the mechanical bound is absent")
	}
	var bound []mechBound
	if err := json.Unmarshal(raw, &bound); err != nil {
		return nil, fmt.Errorf("the mechanical bound is not a list of {check, repo, command, files} objects: %v", err)
	}
	if len(bound) == 0 {
		return nil, fmt.Errorf("the mechanical bound is empty")
	}
	for i, b := range bound {
		switch {
		case strings.TrimSpace(b.Check) == "":
			return nil, fmt.Errorf("mechanical_bound[%d] carries no check", i)
		case strings.TrimSpace(b.Repo) == "":
			return nil, fmt.Errorf("mechanical_bound[%d] carries no repo", i)
		case strings.TrimSpace(b.Command) == "":
			return nil, fmt.Errorf("mechanical_bound[%d] carries no command", i)
		case len(b.Files) == 0:
			return nil, fmt.Errorf("mechanical_bound[%d] carries no files", i)
		}
	}
	return bound, nil
}

// validateBoundCoverage answers whether the FAIL verdict's mechanical
// bound covers its failed checks exactly: every check the gate's recorded
// FAIL verdict marks `fail` has exactly one bound entry naming it, every
// bound entry names a fail check, and each entry's repo is a declared
// task repo. A failure answers the escalation reason; "" = covered.
// (Each entry's command and files are non-empty — parseMechBound's
// shape check; the driver judges nothing else about the classification.)
func validateBoundCoverage(bound []mechBound, failOut map[string]any, reposDir string) string {
	var fails []string
	items, ok := failOut["checks"].([]any)
	if !ok || len(items) == 0 {
		return "the FAIL verdict records no checks for the bound to cover"
	}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := m["status"].(string); s == "fail" {
			name, _ := m["name"].(string)
			if strings.TrimSpace(name) == "" {
				return "the FAIL verdict records a failed check without a name"
			}
			fails = append(fails, name)
		}
	}
	if len(fails) == 0 {
		return "the FAIL verdict records no failed check for the bound to cover"
	}
	repos := taskRepoNames(reposDir)
	seen := map[string]int{}
	for i, b := range bound {
		seen[b.Check]++
		if !slices.Contains(fails, b.Check) {
			return fmt.Sprintf("mechanical_bound[%d] names check %q, which the FAIL verdict did not record fail", i, b.Check)
		}
		if !slices.Contains(repos, b.Repo) {
			return fmt.Sprintf("mechanical_bound[%d] names repo %q, which is not a declared task repo (repos/ children)", i, b.Repo)
		}
	}
	for _, f := range fails {
		switch n := seen[f]; {
		case n == 0:
			return fmt.Sprintf("the FAIL verdict's failed check %q has no mechanical_bound entry covering it", f)
		case n > 1:
			return fmt.Sprintf("the failed check %q is covered by %d mechanical_bound entries, want exactly one", f, n)
		}
	}
	return ""
}

// measureRepairDiff measures the repair diff deterministically against
// the bound: per task repo in the marker's diff base (each repo's HEAD at
// the FAIL verdict's settlement), every changed path between that base
// and the repo's HEAD now must be a MODIFICATION (status M) of a
// bound-named file of that repo — any add, delete, rename or copy breaks
// it, and so does any path outside the bound-named set — and the numstat
// added+deleted lines and file counts sum across repos against the
// hard-coded caps. Every BOUND repo must carry a diff base entry: a repo
// whose HEAD was unreadable at the FAIL verdict's settlement is never
// measured — fail closed, never open. It answers the measured diff and
// the escalation reason ("" = the bound holds). Deterministic: the same
// base, HEAD and bound always measure the same — running it twice
// (pre-dispatch and at settlement) yields the same verdict.
func measureRepairDiff(bound []mechBound, base map[string]string, reposDir string) (lightDiff, string) {
	var diff lightDiff
	boundFiles := map[string]map[string]bool{}
	for _, b := range bound {
		if boundFiles[b.Repo] == nil {
			boundFiles[b.Repo] = map[string]bool{}
		}
		for _, f := range b.Files {
			boundFiles[b.Repo][f] = true
		}
	}
	// every bound repo must carry a diff base: a repo whose HEAD was
	// unreadable at the FAIL verdict's settlement is never measured —
	// fail closed, never open (the bound cannot be judged held)
	for _, repo := range slices.Sorted(maps.Keys(boundFiles)) {
		if _, ok := base[repo]; !ok {
			return diff, fmt.Sprintf("repos/%s carries no repair-diff base (its HEAD was not recorded at the FAIL verdict's settlement); the bound cannot be measured", repo)
		}
	}
	for _, repo := range slices.Sorted(maps.Keys(base)) {
		dir := filepath.Join(reposDir, repo)
		out, err := repoGit(dir, "diff", "--name-status", base[repo], "HEAD")
		if err != nil {
			return diff, fmt.Sprintf("the repair diff of repos/%s is unreadable: %v", repo, err)
		}
		var files int
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			f := strings.Split(line, "\t")
			if len(f) < 2 || f[0] != "M" {
				// any add, delete, rename, copy or unmerged change breaks
				// the bound: modifications only
				return diff, fmt.Sprintf("repos/%s: the repair diff changes %q with status %q, want a modification (M) of a bound-named file only",
					repo, f[len(f)-1], firstOr(f, "?"))
			}
			path := f[len(f)-1]
			if !boundFiles[repo][path] {
				return diff, fmt.Sprintf("repos/%s: the repair diff touches %q, which the bound does not name", repo, path)
			}
			files++
		}
		diff.Files += files
	}
	if diff.Files > maxMechanicalFixFiles {
		return diff, fmt.Sprintf("the repair diff touches %d files, want at most %d (summed across repos)", diff.Files, maxMechanicalFixFiles)
	}
	for _, repo := range slices.Sorted(maps.Keys(base)) {
		dir := filepath.Join(reposDir, repo)
		out, err := repoGit(dir, "diff", "--numstat", base[repo], "HEAD")
		if err != nil {
			return diff, fmt.Sprintf("the repair diff of repos/%s is unreadable: %v", repo, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			f := strings.Fields(line)
			if len(f) < 2 {
				continue // a binary or unparseable row: the file count above already judged the path
			}
			add, aerr := strconv.Atoi(f[0])
			del, derr := strconv.Atoi(f[1])
			if aerr != nil || derr != nil {
				continue // "-" (binary): not line-countable
			}
			diff.Lines += add + del
		}
	}
	if diff.Lines > maxMechanicalFixLines {
		return diff, fmt.Sprintf("the repair diff changes %d lines, want at most %d (added + deleted, summed across repos)", diff.Lines, maxMechanicalFixLines)
	}
	return diff, ""
}

func firstOr(f []string, or string) string {
	if len(f) > 0 {
		return f[0]
	}
	return or
}

// lightRecheckVerdictHolds answers whether the light recheck's own
// output records a passing recheck: verdict pass and every recorded
// check pass (a skipped or failed check is not a passed recheck — the
// bound commands must all have run green). Anything else answers the
// escalation reason; "" = the verdict holds.
func lightRecheckVerdictHolds(out map[string]any) string {
	v, _ := out["verdict"].(string)
	if !strings.EqualFold(strings.TrimSpace(v), "pass") {
		return fmt.Sprintf("the light recheck's verdict is %q, want pass", v)
	}
	items, ok := out["checks"].([]any)
	if !ok || len(items) == 0 {
		return "the light recheck records no checks"
	}
	for i, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			return fmt.Sprintf("the light recheck's checks[%d] is not an object", i)
		}
		if s, _ := m["status"].(string); s != "pass" {
			name, _ := m["name"].(string)
			return fmt.Sprintf("the light recheck's check %q is recorded %q, want pass (a skipped or failed check is not a passed recheck)", name, s)
		}
	}
	return ""
}

// lightRecheckEvidence answers whether the light recheck's own command
// evidence shows every bound command run with exit 0 in its repo (the
// testBarRerun machinery: evidenceEvents + barKey, a leading `cd`
// setting the repo — mechanical, never semantic; the key uses the same
// barSegments canonicalisation as the test bar,), and
// builds the recheck record entries for the light-pass audit. A missing
// match answers the escalation reason; ("", "") = every bound command
// ran.
func lightRecheckEvidence(bound []mechBound, raw json.RawMessage, capsuleRoot string) ([]map[string]any, string) {
	ev, err := decodeCommandEvidence(raw)
	if err != nil {
		return nil, "the light recheck's command evidence is unreadable: " + err.Error()
	}
	if len(ev) == 0 {
		return nil, "the light recheck carries no command evidence"
	}
	events := evidenceEvents(ev, capsuleRoot)
	var recheck []map[string]any
	for _, b := range bound {
		at := 0
		for _, seg := range barSegments(b.Command) {
			words := commandWords(seg)
			if words == nil || words[0] == "cd" {
				continue // the repo is the entry's own
			}
			key := barKey(seg)
			found := false
			for at < len(events) {
				e := events[at]
				at++
				if e.repo == b.Repo && e.exit == 0 && e.key == key {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Sprintf("the bound command %q is not shown run with exit 0 in repos/%s by the light recheck's command evidence", b.Command, b.Repo)
			}
		}
		recheck = append(recheck, map[string]any{"repo": b.Repo, "command": b.Command, "exit_code": 0})
	}
	return recheck, ""
}

// lightRecheckFailReason answers the escalation reason for a light
// recheck that delivered a FAIL verdict: its own fail_reason (a bound
// command still red, or a semantic confirmation the agent cannot make).
func lightRecheckFailReason(out map[string]any) string {
	if r, _ := out["fail_reason"].(string); strings.TrimSpace(r) != "" {
		return "the light recheck did not pass: " + strings.TrimSpace(r)
	}
	return "the light recheck did not pass: its verdict is fail"
}

// lightSettleFacts carries the light recheck's pre-read settlement facts
// (git I/O outside the transition lock): the repair diff re-measured at
// settlement (deterministic — the same measurement the pre-dispatch phase
// made; nothing moves between) with its escalation reason, and the task
// repos' HEADs (the recheck heads the light-pass record carries). A
// gate_failed light recheck's facts carry only the re-measured diff — the
// escalation audit's fact (the recheck's own FAIL verdict escalates
// regardless of the measurement). The pure judgments (verdict pass, every
// check pass, every bound command shown run with exit 0) are derived
// inside the settlement write from the request's own data.
type lightSettleFacts struct {
	diff         lightDiff
	diffMeasured bool   // the bound parsed and the diff was measured against it
	diffBroken   string // the re-measurement's escalation reason ("" = the bound holds)
	heads        map[string]string
}

// mechanicalPassKey is the driver-written output key of the light-pass
// record on the verification step's done output (polyforge#260): the
// bound, the measured diff, the recheck commands and results, the
// recheck heads and the escalation flag — the whole fast-lane audit
// trail in the one atomic write.
const mechanicalPassKey = "mechanical_pass"
