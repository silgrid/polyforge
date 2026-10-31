package verifyrun

// report.go: rendering report-mechanical.md, the mechanical section the
// step merges into artifacts/verification-report.md — the merges, the
// plans, every check, the pre-existing failures with their base sha, the
// flaky units, the test bar as run and the bound scenarios. Excerpts and
// paths only: the full command logs stay local under runtime-state/verify.

import (
	"fmt"
	"strings"
)

// renderReport renders res as Markdown.
func renderReport(res *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Mechanical run (`pf verify-run`, run %d)\n\n", res.Run)
	if res.Incomplete != "" {
		fmt.Fprintf(&b, "**Incomplete — no merged-tree proof could be made:** %s\n\n", res.Incomplete)
	} else {
		fmt.Fprintf(&b, "Mechanical verdict: **%s**. Tier: requested %s, effective %s.\n\n", res.MechanicalVerdict, res.Tier.Requested, res.Tier.Effective)
	}
	fmt.Fprintf(&b, "Command logs (local, not part of the capsule chain): `%s/`. Whole-run deadline: %s", res.LogDir, res.Deadline)
	if res.DeadlineClampedFrom != "" {
		fmt.Fprintf(&b, " (clamped from the requested %s, above the step's tool-window bound)", res.DeadlineClampedFrom)
	}
	b.WriteString(".\n\n")

	b.WriteString("### Merge of the latest default branch\n\n")
	b.WriteString("| repo | default | outcome | HEAD | base |\n| --- | --- | --- | --- | --- |\n")
	for _, name := range sortedKeys(res.MergeOutcomes) {
		m := res.MergeOutcomes[name]
		outcome := m.Outcome
		if m.SelfHealed {
			outcome += " (an interrupted merge was aborted first)"
		}
		if !m.Touched {
			outcome += ", untouched"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", name, m.DefaultBranch, outcome, short(m.Head), short(m.Base))
	}
	b.WriteString("\n")

	if len(res.Repos) > 0 {
		b.WriteString("### Plans\n\n")
		for _, rp := range res.Repos {
			switch {
			case !rp.Touched:
				fmt.Fprintf(&b, "- `%s`: untouched — no bar.\n", rp.Name)
			case rp.Plan != PlanPlanned:
				fmt.Fprintf(&b, "- `%s`: **unplanned** (%s): %s\n", rp.Name, rp.Plan, rp.PlanReason)
			default:
				fmt.Fprintf(&b, "- `%s`: stack %s, conf %s, mode %s, %d command(s) — `%s`\n", rp.Name, rp.Stack, rp.Conf, rp.Mode, len(rp.Commands), rp.EngineRef)
			}
		}
		b.WriteString("\n")
	}

	if len(res.Checks) > 0 {
		b.WriteString("### Checks\n\n| check | status | detail |\n| --- | --- | --- |\n")
		for _, c := range res.Checks {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", c.Name, c.Status, cell(c.Detail))
		}
		b.WriteString("\n")
	}

	var flaky, pre []UnitReport
	for _, u := range res.Units {
		switch u.Status {
		case "flaky":
			flaky = append(flaky, u)
		case "pre_existing":
			pre = append(pre, u)
		}
	}
	if len(pre) > 0 {
		b.WriteString("### Pre-existing failures (reported, never this change's fault)\n\n")
		for _, u := range pre {
			fmt.Fprintf(&b, "- `%s: %s` (%s) — %s\n", u.File, u.Name, u.Check, u.Detail)
		}
		b.WriteString("\n")
	}
	if len(flaky) > 0 {
		b.WriteString("### Flaky units (failed, then passed on a re-run at HEAD)\n\n")
		for _, u := range flaky {
			fmt.Fprintf(&b, "- `%s` (%s) — %s; first failure: %s\n", u.Name, u.Check, u.Detail, u.Signature)
		}
		b.WriteString("\n")
	}

	if len(res.TestBar) > 0 {
		b.WriteString("### Test bar (as run)\n\n")
		for _, t := range res.TestBar {
			fmt.Fprintf(&b, "- `%s`: `%s`\n", t.Repo, t.Command)
		}
		for _, n := range res.TestBarNotes {
			fmt.Fprintf(&b, "- note: %s\n", n)
		}
		b.WriteString("\n")
	}

	if len(res.BoundScenarios) > 0 || len(res.UnboundTitles) > 0 {
		b.WriteString("### Authored scenarios resolved by their bindings\n\n")
		for _, s := range res.BoundScenarios {
			fmt.Fprintf(&b, "- %s — **%s**: %s\n", s.Title, s.Status, s.Detail)
		}
		for _, t := range res.UnboundTitles {
			fmt.Fprintf(&b, "- %s — no binding (document-only: the step judges it)\n", t)
		}
		b.WriteString("\n")
	}
	if len(res.UnplannedRepos) > 0 {
		fmt.Fprintf(&b, "Unplanned repos (their bar is the step's own): %s.\n", strings.Join(res.UnplannedRepos, ", "))
	}
	return b.String()
}

// cell keeps a table cell on one line.
func cell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "|", `\|`)
}
