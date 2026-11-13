// carries: docs/reference/workflow-contract.md, docs/reference/config.md, docs/reference/commands.md, docs/bundles.md, skills/polyforge/SKILL.md, skills/polyforge/verbs/doctor.md, workflows/develop/README.md

package test

// w521_docs_test.go ( u14): the documents teach a project's
// skip of a step its lane declares skippable — the lane key, the
// workspace yaml switch and its write, the claim fixing the skipped set,
// the skipped execution status and its event, the wrap attrs a skipped
// tail hands to the last running step and published.py.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW521DocsTeachTheProjectSkip$' -count=1 -v

import (
	"regexp"
	"strings"
	"testing"
)

// w521Section answers the body of the first heading of page whose text
// starts with heading, up to the next heading of the same or a higher
// level ("" when absent).
func w521Section(page, heading string) string {
	lines := strings.Split(page, "\n")
	for i, l := range lines {
		m := w254HeadingRe.FindStringSubmatch(l)
		if m == nil || !strings.HasPrefix(m[2], heading) {
			continue
		}
		level := len(m[1])
		var b strings.Builder
		for _, l2 := range lines[i+1:] {
			if m2 := w254HeadingRe.FindStringSubmatch(l2); m2 != nil && len(m2[1]) <= level {
				break
			}
			b.WriteString(l2)
			b.WriteByte('\n')
		}
		return b.String()
	}
	return ""
}

func TestW521DocsTeachTheProjectSkip(t *testing.T) {
	need := func(path, body string, needles ...string) {
		t.Helper()
		norm := w254Norm(body)
		for _, n := range needles {
			if !strings.Contains(norm, n) {
				t.Errorf("%s does not say %q", path, n)
			}
		}
	}

	// The workflow contract: the key, the status, the event, the claim's
	// fixed set kept across a re-claim and a resume, the skipped tail's
	// wrap attrs, the shared script.
	contract := w254Read(t, "docs/reference/workflow-contract.md")
	need("docs/reference/workflow-contract.md", contract,
		"| `skippable` |",
		"`skipped`",
		"`step_skipped`",
		"`runtime-state/skipped-steps.json`",
		"pass to the last step that runs",
		"`published.py`",
	)
	if s := w254Norm(contract); !regexp.MustCompile(`skipped-steps\.json[^|]*re-claim[^|]*pf resume`).MatchString(s) {
		t.Errorf("docs/reference/workflow-contract.md does not say the skipped set fixed at the first claim is kept by a re-claim and `pf resume`")
	}

	// The config reference: the key, its class, the refusal, the ignored
	// hand-written skip, the per-lane reach — and never the retired phrase.
	config := w254Read(t, "docs/reference/config.md")
	need("docs/reference/config.md", config,
		"steps.<project>.<step-id>.skip",
		"next-claim",
		"`skippable: true`",
		"ignored",
		"micro",
	)
	if strings.Contains(config, "next claim") {
		t.Errorf("docs/reference/config.md says \"next claim\"")
	}
	skipSec := w521Section(config, "`steps.<project>.<step-id>.skip`")
	if skipSec == "" {
		t.Fatalf("docs/reference/config.md has no `steps.<project>.<step-id>.skip` section")
	}
	need("docs/reference/config.md (skip section)", skipSec, "next-claim", "pf config set", "refuse", "micro")

	// The command reference: pf workflows' fields, the config write, the
	// step script.
	commands := w254Read(t, "docs/reference/commands.md")
	wf := w521Section(commands, "`pf workflows`")
	need("docs/reference/commands.md (pf workflows)", wf, "`skippable`", "`skip`")
	need("docs/reference/commands.md", commands, "steps.<project>.<step-id>.skip", "`published.py")
	need("docs/reference/commands.md (step scripts)", w521Section(commands, "Step scripts"), "`published.py --publish")

	// The lane's narrative and the bundles chapter.
	need("workflows/develop/README.md", w254Read(t, "workflows/develop/README.md"),
		"## Skipping merge", "`published`")
	need("docs/bundles.md", w254Read(t, "docs/bundles.md"),
		"`skippable: true`", "steps.<project>.merge.skip", "`published`")

	// The routing skill and the doctor verb no longer say a step entry has
	// one key.
	for _, p := range []string{"skills/polyforge/SKILL.md", "skills/polyforge/verbs/doctor.md"} {
		body := w254Read(t, p)
		if strings.Contains(w254Norm(body), "`models` its one key") {
			t.Errorf("%s still says `models` is a step entry's one key", p)
		}
		need(p, body, "skip: true")
	}
	if strings.Contains(w254Read(t, "skills/polyforge/SKILL.md"), "next claim") {
		t.Errorf("skills/polyforge/SKILL.md says \"next claim\"")
	}

}
