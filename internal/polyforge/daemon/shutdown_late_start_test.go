package daemon

// shutdown_late_start_test.go — an agent start still binding when the
// shutdown latches (the shutdown does not wait for it) leaves no agent
// process behind: whether it binds shortly after or only once Run
// returned, its binder tears it down.

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// procAgent is an agent handle fronting a real process group (PID 0 to
// the runtime, so its teardown is the handle's own Terminate).
type procAgent struct {
	cmd  *exec.Cmd
	once sync.Once
	done chan struct{}
}

func startProcAgent(t *testing.T) *procAgent {
	cmd := exec.Command("sleep", "120")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Errorf("start agent process: %v", err)
		return nil
	}
	a := &procAgent{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(a.done) }()
	t.Cleanup(func() { _ = a.Terminate(time.Second) })
	return a
}

func (a *procAgent) Exit() <-chan struct{} { return a.done }
func (a *procAgent) PID() int              { return 0 }
func (a *procAgent) StartedAt() string     { return "" }
func (a *procAgent) Done() bool            { return !a.Alive() }
func (a *procAgent) Alive() bool {
	select {
	case <-a.done:
		return false
	default:
		return true
	}
}
func (a *procAgent) Terminate(bound time.Duration) error {
	a.once.Do(func() { _ = syscall.Kill(-a.cmd.Process.Pid, syscall.SIGKILL) })
	select {
	case <-a.done:
	case <-time.After(bound):
	}
	return nil
}

func TestShutdownLateAgentStartLeavesNoProcess(t *testing.T) {
	for _, delay := range []time.Duration{900 * time.Millisecond, -1} {
		name := "binds-during-the-sequence"
		if delay < 0 {
			name = "binds-after-run-returned"
		}
		t.Run(name, func(t *testing.T) {
			entered := make(chan struct{})
			proceed := make(chan struct{})
			bound := make(chan *procAgent, 1)
			var once sync.Once
			td := newTestDaemon(t, func(c *Config) {
				c.AgentStarter = func(ctx context.Context, in runtime.AgentStartInput) (Agent, error) {
					once.Do(func() { close(entered) })
					if delay >= 0 {
						time.Sleep(delay)
					} else {
						<-proceed
					}
					a := startProcAgent(t)
					bound <- a
					return a, nil
				}
			})
			const wi = "wi_late_start"
			mkBare(t, td.cfg.GitRemoteBase, wi)
			if _, err := td.client.ClaimWithModels(wi, "normal", nil); err != nil {
				t.Fatalf("claim: %v", err)
			}
			select {
			case <-entered:
			case <-time.After(20 * time.Second):
				t.Fatal("the agent start never began")
			}
			if err := td.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
				t.Fatalf("shutdown: %v", err)
			}
			select {
			case <-td.runDone:
			case <-time.After(20 * time.Second):
				t.Fatal("Run did not return")
			}
			close(proceed)
			var a *procAgent
			select {
			case a = <-bound:
			case <-time.After(20 * time.Second):
				t.Fatal("the agent start never returned")
			}
			deadline := time.Now().Add(15 * time.Second)
			for a.Alive() {
				if time.Now().After(deadline) {
					t.Fatal("the late-bound agent process outlived the shutdown")
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}
