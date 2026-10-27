package runtime

// taskproc_test.go: the task agent's own origin classification
// (workbench#53). The two classes must stay distinct: a process carrying
// PF_TASK_ID is a task and NOT a step, whatever its id shape suggests.

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	w53OriginHelperEnv = "W53_ORIGIN_HELPER"
	w53UnitDaemonID    = "m-w53-unit"
	w53UnitTaskID      = "task_0123456789abcdef"
)

// w53MarkedEnv is the helper's environment: the ambient one minus every
// polyforge marker (the unit test may itself run inside a workflow step),
// plus exactly the task markers.
func w53MarkedEnv(daemonID string) []string {
	out := make([]string, 0, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "PF_WI_ID", "PF_TASK_ID", "PF_DAEMON_ID", "PF_DAEMON_SOCKET", w53OriginHelperEnv:
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		w53OriginHelperEnv+"=1",
		"PF_TASK_ID="+w53UnitTaskID,
		"PF_DAEMON_ID="+daemonID,
	)
}

// TestMarkedTaskOriginHelper is not a test: re-executed as the marked
// process. It prints one readiness line and then parks until killed, so
// the parent reads a live process whose environ is fully in place.
func TestMarkedTaskOriginHelper(t *testing.T) {
	if os.Getenv(w53OriginHelperEnv) == "" {
		t.Skip("helper process only: re-executed with the task markers by TestTaskProcessOriginIsItsOwnClass")
	}
	os.Stdout.WriteString("ready\n")
	buf := make([]byte, 1)
	for {
		if _, err := os.Stdin.Read(buf); err != nil {
			return
		}
	}
}

// TestTaskProcessOriginIsItsOwnClass: a PF_TASK_ID + PF_DAEMON_ID process
// is classified as a task of THIS daemon, is not classified as a step, and
// is not claimed by a daemon with another machine id.
func TestTaskProcessOriginIsItsOwnClass(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestMarkedTaskOriginHelper$", "-test.count=1")
	cmd.Env = w53MarkedEnv(w53UnitDaemonID)
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
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && strings.TrimSpace(line) != "ready" {
			err = &os.PathError{Op: "helper", Path: "readiness", Err: os.ErrInvalid}
		}
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
	pid := cmd.Process.Pid
	if got, ok := TaskProcessOrigin(pid, w53UnitDaemonID); !ok || got != w53UnitTaskID {
		t.Fatalf("TaskProcessOrigin(%d, %q) = %q, %v; want the task id", pid, w53UnitDaemonID, got, ok)
	}
	if got, ok := StepProcessOrigin(pid, w53UnitDaemonID); ok {
		t.Fatalf("a task process must not be classified as a step; StepProcessOrigin answered %q", got)
	}
	if got, ok := TaskProcessOrigin(pid, "m-some-other-daemon"); ok {
		t.Fatalf("another daemon's machine id must not claim the task (got %q)", got)
	}
	_ = stdin.Close()
}
