package runtime

import (
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// setsidEscapeeHelperEnv switches the test binary into the escapee role.
const setsidEscapeeHelperEnv = "W77_SETSID_ESCAPEE_HELPER"

// setsidEscapeeScript is the step process TestStepHandleTerminateReapsSetsidChild
// starts as its own process-group leader: it puts one background child in a
// new session (setsid) — outside the step's group — and then becomes the
// long-lived step process itself.
//
// The child is this test binary in the escapee role, and it reports its own
// pid on "$ESCAPEE_PID" only once it runs: setsid() has returned and its
// exec is complete (its environ, which the step-end sweep reads, is in
// place). A reader that sees the pid sees a settled process outside the
// step's group. The parent shell's `echo $!` right after the fork raced the
// child's setsid(): a reader woken by it could find the child still in the
// group.
var setsidEscapeeScript = setsidEscapeeHelperEnv + `=1 setsid ` + shellQuote(os.Args[0]) +
	` -test.run='^TestSetsidEscapeeHelper$' -test.count=1 & exec sleep 300`

// shellQuote single-quotes s for sh.
func shellQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

// TestSetsidEscapeeHelper is not a test: re-executed through setsid by
// setsidEscapeeScript. It reports its pid on $ESCAPEE_PID and parks until
// SIGTERM (the step-end sweep's first signal) or SIGKILL.
func TestSetsidEscapeeHelper(t *testing.T) {
	if os.Getenv(setsidEscapeeHelperEnv) == "" {
		t.Skip("helper process only: re-executed by TestStepHandleTerminateReapsSetsidChild's step script")
	}
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	if err := os.WriteFile(os.Getenv("ESCAPEE_PID"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("report the escapee pid: %v", err)
	}
	<-term
}
