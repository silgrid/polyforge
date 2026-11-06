package test

// w405_gate_limits_test.go: the test gate's container memory limit.
//
// The gate's limits are the script's own flags (design r2 D7: gate.py
// --cpus/--memory/--pids, defaults min(8, cores) / 8g / 4096; the machine's
// pf CLI config is no input of it, w405_pf_gate_test.go). Its --memory rule
// is docker's --memory syntax — an integer with an optional unit b, k, m or
// g — at least docker's 6m floor, value for value as the table below says.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW405GateLimits' -count=1

import (
	"strings"
	"testing"
)

// TestW405GateLimitsMemoryRule: gate.py refuses exactly the --memory values
// the rule refuses (a value it takes goes on to the gate, which here finds
// no container engine: exit 2, not judged — never the limit's refusal).
func TestW405GateLimitsMemoryRule(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	for v, why := range map[string]string{
		"8g": "", "12G": "", "6m": "", "6291456": "",
		"1024k": "below docker's 6m floor", "5m": "below docker's 6m floor", "0": "below docker's 6m floor",
		"8 gigs": "is not a size", "8gb": "is not a size", "-1g": "is not a size", "g": "is not a size",
		"999999999999999999999g": "is out of range",
	} {
		t.Run(v, func(t *testing.T) {
			_, errb, code := w405RunGate(t, w, []string{"FAKE_DOCKER_DOWN=1"}, "--plan-only", "--memory="+v)
			if code != 2 {
				t.Fatalf("gate.py --memory %s: exit %d, want 2 (refused, or no engine)\n%s", v, code, errb)
			}
			refused := strings.Contains(errb, "gate.py: --memory: ")
			if refused != (why != "") || !strings.Contains(errb, why) {
				t.Fatalf("--memory %q: refused %v, want %q\n%s", v, refused, why, errb)
			}
			if !refused && !strings.Contains(errb, "container engine is unavailable") {
				t.Fatalf("--memory %q was taken but the gate did not go on to the engine:\n%s", v, errb)
			}
		})
	}
}
