// carries: workflows/develop/workflow.yaml, workflows/integrate/workflow.yaml, docs/reference/config.md, docs/reference/commands.md, workflows/develop/README.md, workflows/integrate/README.md, workflows/_shared/skills/verify.md, workflows/integrate/skills/full-gate.md, internal/contracttest/w138_audit_param_contract_test.go
package test

// w138_audit_param_contract_test.go (polyforge#138): a text contract. The
// verification step (develop) and the full-gate step (integrate) declare
// the `audit` parameter, default "on" — the lane's own dependency audit.
// A project whose owner decides against the lane's dependency audit sets
// steps.<project>.<step>.params.audit to "off" in the workspace config (or
// a ticket override): the step then asks the scope engine without the
// appended `audit` command and records the closure in its result. The
// switch is the project owner's alone — no key of a repository's
// verify-scope.conf can turn the audit off or loosen it (that contract is
// pinned by test/w320_audit_engine_contract_test.go and stays).
//
// The acceptance here is AC1's declaration: the lanes' steps really parse
// with a params map whose audit default is the string "on" (read through
// the yaml, not a substring — a params key of another name must not
// satisfy it), beside the image parameter of polyforge#64.
//
// Red on the base tree: neither lane declares the parameter — the first
// yaml assertion fails.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -run '^TestW138AuditParamLanes$' -count=1 -v

import (
	"strings"
	"testing"
)

func TestW138AuditParamLanes(t *testing.T) {
	t.Run("the lanes declare audit, default on beside image", func(t *testing.T) {
		for _, lane := range []struct{ name, rel, step string }{
			{"develop", "workflows/develop/workflow.yaml", "verification"},
			{"integrate", "workflows/integrate/workflow.yaml", "full-gate"},
		} {
			t.Run(lane.step+" of "+lane.name, func(t *testing.T) {
				params := pf64StepParams(t, lane.rel, lane.step)
				v, ok := params["audit"]
				if !ok {
					t.Errorf("%s: the %s step declares no params.audit — the lane default must be there, \"on\"",
						lane.rel, lane.step)
					return
				}
				if v != "on" {
					t.Errorf("%s: the %s step's params.audit default is %q, not \"on\"",
						lane.rel, lane.step, v)
				}
				if _, ok := params["image"]; !ok {
					t.Errorf("%s: the %s step's params.image (polyforge#64) must stay beside params.audit",
						lane.rel, lane.step)
				}
			})
		}
	})
}

// TestW138AuditParamDocs pins AC4's documents and the skills' routing of the
// fixed mark: the parameter is documented where the image parameter of
// polyforge#64 is (config.md's params section, the two lanes' READMEs and
// commands.md's verify.py / gate.py sections), stating the values, the
// named error and that the switch is the project owner's, never a
// repository's own; and the two skills route pf-audit-param-invalid like
// pf-verify-image-missing — blocked at once, no retry (a misconfigured
// parameter is a person's fix, not a retry's).
func TestW138AuditParamDocs(t *testing.T) {
	t.Run("config.md documents the audit parameter beside image", func(t *testing.T) {
		flat := w68Flat(t, "docs/reference/config.md")
		for _, marker := range []string{
			"steps.<project>.verification.params.audit",
			"steps.<project>.full-gate.params.audit",
			"`pf override <id> --step <step-id> --param audit=off`",
			"pf-audit-param-invalid",
			"the project owner's decision, not the repository's",
		} {
			if !strings.Contains(flat, marker) {
				t.Errorf("docs/reference/config.md must document the audit parameter: %q", marker)
			}
		}
	})
	t.Run("the lanes' READMEs name the parameter", func(t *testing.T) {
		for _, rel := range []string{"workflows/develop/README.md", "workflows/integrate/README.md"} {
			flat := w68Flat(t, rel)
			for _, marker := range []string{"`audit` parameter", "pf-audit-param-invalid"} {
				if !strings.Contains(flat, marker) {
					t.Errorf("%s must document the audit parameter: %q", rel, marker)
				}
			}
		}
	})
	t.Run("commands.md documents both steps' new exit and record, section by section", func(t *testing.T) {
		flat := w68Flat(t, "docs/reference/commands.md")
		for _, marker := range []string{
			"PF_PARAM_AUDIT", "audit_disabled", "audit_off", "--audit off",
		} {
			if !strings.Contains(flat, marker) {
				t.Errorf("docs/reference/commands.md must document the audit parameter's behavior: %q", marker)
			}
		}
		// each step's own section keeps its prepared-image error AND gains
		// the audit one — a whole-file Contains would let one section lose
		// a mark while the other still carries it (review r2 #1). A section
		// must END at the next top-level heading (review r3 #1): a check
		// whose span reaches into the next section lets that next section's
		// marks stand in for this one's, and a section that is the last of
		// the two is unbounded only to the file's end.
		sections := []struct{ name, head, next string }{
			{"verify.py", "## The verification bar — `verify.py`'s mechanical run",
				"## The full test gate — `gate.py`"},
			{"gate.py", "## The full test gate — `gate.py`", ""},
		}
		for _, sec := range sections {
			i := strings.Index(flat, sec.head)
			if i < 0 {
				t.Fatalf("docs/reference/commands.md has no %s section", sec.name)
			}
			rest := flat[i:]
			if j := strings.Index(rest[1:], sec.head); j >= 0 {
				rest = rest[:j+1]
			}
			if sec.next != "" && strings.Contains(rest, sec.next) {
				t.Errorf("docs/reference/commands.md's %s section must end at the next top-level heading; it spans into %q, so its own marks are pinned by the wrong section",
					sec.name, sec.next)
			}
			for _, marker := range []string{"pf-verify-image-missing", "pf-audit-param-invalid"} {
				if !strings.Contains(rest, marker) {
					t.Errorf("docs/reference/commands.md's %s section must carry both the image and the audit error marks, lacks %q",
						sec.name, marker)
				}
			}
		}
	})
	t.Run("the skills route the fixed mark, blocked at once", func(t *testing.T) {
		// one marker across both halves of the route: the mark AND the
		// no-retry hand-off in one sentence, so a skill that names the mark
		// but still lets it retry stays red (review r2 N10)
		route := "`pf-audit-param-invalid`: the `audit` parameter's value is neither `on` nor `off` — hand off `blocked` at once, no retry"
		for _, rel := range []string{"workflows/_shared/skills/verify.md", "workflows/integrate/skills/full-gate.md"} {
			flat := w68Flat(t, rel)
			if !strings.Contains(flat, route) {
				t.Errorf("%s must route pf-audit-param-invalid without a retry: %q", rel, route)
			}
		}
	})
}
