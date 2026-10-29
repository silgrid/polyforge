package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// captureStderr runs fn with os.Stderr redirected and answers what was
// written meanwhile.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = wr
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, rd)
		close(done)
	}()
	defer func() {
		os.Stderr = orig
		wr.Close()
		<-done
		rd.Close()
	}()
	fn()
	os.Stderr = orig
	wr.Close()
	<-done
	return buf.String()
}

// TestTerminalWrapParkLogsThePark: a completed workflow whose driver Run
// ignores the teardown parks its terminal wrap; the daemon log must say
// it parked (and finishes once the Run exits), not that it failed and is
// being retried.
func TestTerminalWrapParkLogsThePark(t *testing.T) {
	k := newCloseParkRig(t, "wi_terminal_wrap_park_log")
	execDir := filepath.Join(k.j.capsuleDir, "runtime-state")
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(map[string]string{"workflow_status": capsule.WorkflowStatusCompleted})
	if err := os.WriteFile(filepath.Join(execDir, "execution.json"), blob, 0o644); err != nil {
		t.Fatal(err)
	}

	logged := captureStderr(t, func() {
		k.r.handleOB(context.Background(), recoveryMsg(k.j, protocol.RecoverDeath))
	})
	if !k.j.closeParked() {
		t.Fatal("precondition: the terminal wrap did not park on the live Run")
	}
	if strings.Contains(logged, "terminal wrap failed") || strings.Contains(logged, "retrying") {
		t.Fatalf("the parked terminal wrap is logged as a failed, retried close:\n%s", logged)
	}
	if !strings.Contains(logged, "terminal wrap parked") {
		t.Fatalf("the parked terminal wrap must be logged as parked:\n%s", logged)
	}
}
