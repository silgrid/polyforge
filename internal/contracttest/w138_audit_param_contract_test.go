// carries: workflows/develop/workflow.yaml, workflows/integrate/workflow.yaml, internal/contracttest/w138_audit_param_contract_test.go
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

import "testing"

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
