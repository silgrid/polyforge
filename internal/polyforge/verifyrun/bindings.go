package verifyrun

// bindings.go: resolving e2e-author's bindings into authored-scenario
// outcomes. A bound title passes when every binding of it holds: its
// command ran in its repo (the plan's identical command, else the
// authored run verify-run made for it), its unit did not fail (a flaky
// pass counts, a pre-existing failure does not), and a line of that
// command's output — or of the unit's passing re-run — matches its
// marker. A binding whose command the plan skipped for an unmet
// precondition resolves skipped (legal for the gate, never for the
// automatic merge). A title without a binding is document-only: the step
// judges it.

import (
	"fmt"
	"regexp"
	"strings"
)

// resolveScenarios fills res.BoundScenarios and res.UnboundTitles in the
// order of the authored titles.
func (r *runner) resolveScenarios(res *Result, repos []*repoRun) {
	res.BoundScenarios = []ScenarioResult{}
	res.UnboundTitles = []string{}
	byRepo := map[string]*repoRun{}
	for _, rr := range repos {
		byRepo[rr.name] = rr
	}
	for _, title := range r.facts.ScenarioTitles {
		var bs []Binding
		for _, b := range r.facts.Bindings {
			if b.Title == title {
				bs = append(bs, b)
			}
		}
		if len(bs) == 0 {
			res.UnboundTitles = append(res.UnboundTitles, title)
			continue
		}
		sr := ScenarioResult{Title: title, Status: "pass"}
		var details []string
		for _, b := range bs {
			status, why := resolveBinding(b, byRepo[b.Repo])
			switch {
			case status == "fail":
				sr.Status = "fail"
			case status == "skipped" && sr.Status == "pass":
				sr.Status = "skipped"
			}
			details = append(details, why)
		}
		sr.Detail = strings.Join(details, "; ")
		res.BoundScenarios = append(res.BoundScenarios, sr)
	}
}

// resolveBinding judges one binding against its repo's runs: pass, fail
// or skipped, with the reason.
func resolveBinding(b Binding, rr *repoRun) (string, string) {
	if rr == nil {
		return "fail", fmt.Sprintf("binding repo %q is not a task repo of this capsule", b.Repo)
	}
	want := strings.TrimSpace(b.Command)
	var cr *cmdRun
	for _, c := range append(append([]*cmdRun(nil), rr.runs...), rr.authored...) {
		if strings.TrimSpace(c.cmd) == want {
			cr = c
			break
		}
	}
	if cr == nil {
		return "fail", fmt.Sprintf("`%s` did not run in repos/%s", want, rr.name)
	}
	if cr.skipped != "" {
		return "skipped", fmt.Sprintf("`%s` (repos/%s) was not run: %s", want, rr.name, cr.skipped)
	}
	if cr.notStarted != "" {
		return "fail", fmt.Sprintf("`%s` (repos/%s) %s", want, rr.name, cr.notStarted)
	}
	re, err := regexp.Compile(b.Marker)
	if err != nil {
		return "fail", fmt.Sprintf("marker %q does not compile: %v", b.Marker, err)
	}
	// the unit may exist in several packages: any failing one fails it
	var u *unitRun
	for _, x := range cr.units {
		if x.Name != b.Unit {
			continue
		}
		if x.failing() {
			return "fail", fmt.Sprintf("%s failed in `%s` (repos/%s, check %s): %s", b.Unit, want, rr.name, cr.check, x.Signature)
		}
		if u == nil || x.Flaky != "" {
			u = x
		}
	}
	if cr.exit != 0 && u == nil {
		return "fail", fmt.Sprintf("`%s` (repos/%s) exited %d and %s is not shown passing", want, rr.name, cr.exit, b.Unit)
	}
	logs := []string{cr.logPath}
	if u != nil {
		logs = append(logs, u.PassLogs...)
	}
	for _, lp := range logs {
		for _, line := range outputLines(lp) {
			if re.MatchString(line) {
				s := fmt.Sprintf("%s passed in `%s` (repos/%s, check %s); marker /%s/ matched %q", b.Unit, want, rr.name, cr.check, b.Marker, line)
				if u != nil && u.Flaky != "" {
					s += " (" + u.Flaky + ")"
				}
				return "pass", s
			}
		}
	}
	return "fail", fmt.Sprintf("marker /%s/ matched no output line of `%s` (repos/%s, check %s)", b.Marker, want, rr.name, cr.check)
}
