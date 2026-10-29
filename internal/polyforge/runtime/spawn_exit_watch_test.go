package runtime

// spawn_exit_watch_test.go: C7 — the exit watcher is a plain cmd.Wait
// plus the bounded pipe drain. A pin, not a repro (the deleted 5s ticker
// branch never fired): a step process whose grandchild keeps both pipes
// open still reports its real exit within the drain bound.

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

func TestExitWatcherReportsExitWhileGrandchildHoldsPipes(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30 & exit 3")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	outW.Close()
	errW.Close()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	got := make(chan spawn.ExitInfo, 1)
	newProcOpts(cmd, nil, outR, procOpts{stderr: errR, onExit: func(e spawn.ExitInfo) { got <- e }})
	select {
	case e := <-got:
		if e.Code != 3 {
			t.Fatalf("exit class = %+v, want code 3", e)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Fatalf("exit reported after %s, want within the 500ms drain bound", took)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("the exit was never reported while a grandchild held the pipes")
	}
}
