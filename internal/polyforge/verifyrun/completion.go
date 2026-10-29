package verifyrun

// completion.go: the one output that proves a suite ran to completion.
// When every failure a command's output locates is explained (flaky or
// pre-existing) and the output also proves completion, nothing else can
// hide in the exit, so the command needs no confirming re-run. Only the
// repo's declared Go harness, run directly (`sh|bash <go_harness>`, no
// `-c`, no wrapper script), offers such a proof: its trailer is its last
// line and counts every listed test as run or excluded. Every other
// runner can stop early or fail beside its tests (go test's panics,
// timeouts and -failfast, pytest -x, a coverage floor, cargo stopping
// after its first failing target, a suite crashing after its last
// marker), so its explained failures are confirmed by a whole re-run in
// its skip form, or, without a skip form, the command stays failed.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var reHarnessRan = regexp.MustCompile(`^FAIL\te2e\t(\d+)/(\d+) tests ran(?:, (\d+) excluded by E2E_SKIP)?,`)

// lastLine answers the last non-empty output line (verify-run's own kill
// notes skipped).
func lastLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" && !strings.HasPrefix(lines[i], "# pf verify-run:") {
			return lines[i]
		}
	}
	return ""
}

// completionProof answers how cr's first-run output proves its suite ran
// to completion ("" = it does not).
func completionProof(cr *cmdRun) string {
	if !cr.harness || cr.timedOut || cr.logPath == "" {
		return ""
	}
	m := reHarnessRan.FindStringSubmatch(lastLine(outputLines(cr.logPath)))
	if m == nil {
		return ""
	}
	ran, _ := strconv.Atoi(m[1])
	listed, _ := strconv.Atoi(m[2])
	excluded, _ := strconv.Atoi(m[3])
	if ran+excluded != listed {
		return ""
	}
	return fmt.Sprintf("the harness trailer shows all %d listed tests ran or were excluded", listed)
}
