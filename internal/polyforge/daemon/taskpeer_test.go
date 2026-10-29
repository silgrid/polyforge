package daemon

// taskpeer_test.go: the task agent class at the daemon's caller
// classification seam. The write guard itself is pinned
// end-to-end by tasks_test.go and the frozen reproduction; this file pins
// the second use of the same classification — a task's GET may read a watch
// snapshot, but it is not the HUMAN side, so a demand-marked snapshot from
// it must not arm the summarize lane (that is a dispatch, not a read).

import (
	"bufio"
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	w53DemandHelperEnv = "W53_DEMAND_HELPER"
	w53DemandDaemonID  = "m-w53-demand-unit"
)

// TestW53DemandHelperProcess is not a test: it is the marked task process
// the check below reads a peer credential for. It signals readiness and
// parks until its stdin closes.
func TestW53DemandHelperProcess(t *testing.T) {
	if os.Getenv(w53DemandHelperEnv) == "" {
		t.Skip("helper process only: re-executed with the task markers by TestHumanSidePeerExcludesATaskProcessTree")
	}
	os.Stdout.WriteString("ready\n")
	buf := make([]byte, 1)
	for {
		if _, err := os.Stdin.Read(buf); err != nil {
			return
		}
	}
}

// TestHumanSidePeerExcludesATaskProcessTree: a task process is not the
// human side, so it cannot mark summarize demand; a peer without a
// credential is not either (fail closed).
func TestHumanSidePeerExcludesATaskProcessTree(t *testing.T) {
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "PF_WI_ID", "PF_TASK_ID", "PF_DAEMON_ID", "PF_DAEMON_SOCKET", w53DemandHelperEnv:
			continue
		}
		env = append(env, kv)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestW53DemandHelperProcess$", "-test.count=1")
	cmd.Env = append(env, w53DemandHelperEnv+"=1", "PF_TASK_ID=task_0123456789abcdef", "PF_DAEMON_ID="+w53DemandDaemonID)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	ready := make(chan error, 1)
	go func() {
		_, err := bufio.NewReader(stdout).ReadString('\n')
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("the marked helper never signalled readiness: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the marked helper never signalled readiness")
	}
	d := &Daemon{cfg: Config{MachineID: w53DemandDaemonID}}
	req := httptest.NewRequest("GET", "/jobs/wi_x/watch?demand=1", nil)
	req = req.WithContext(context.WithValue(req.Context(), peerCredKey{}, peerCred{pid: cmd.Process.Pid, ok: true}))
	if d.humanSidePeer(req) {
		t.Fatal("a task process tree must not count as the human side")
	}
	if (&Daemon{cfg: Config{MachineID: w53DemandDaemonID}}).humanSidePeer(httptest.NewRequest("GET", "/jobs/wi_x/watch?demand=1", nil)) {
		t.Fatal("a peer without a credential is not the human side (fail closed)")
	}
}
