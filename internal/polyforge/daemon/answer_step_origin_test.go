package daemon

// answer_step_origin_test.go: polyforge#122 — a grill-review step agent
// answered its own human question with `pf answer` and the workflow moved
// on. The daemon now refuses an answer whose UDS peer belongs to a step
// process tree of this daemon (the PF_WI_ID/PF_DAEMON_ID markers every
// step process is spawned with, on the peer or an ancestor). The
// scenarios dial the real UDS face from a CHILD process, so the kernel's
// SO_PEERCRED names that child — exactly what an agent's bash tool is.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// TestPF122AnswerHelperProcess is not a test: re-executed as a child, it
// posts one answer over the socket named in its environment and prints
// the outcome.
func TestPF122AnswerHelperProcess(t *testing.T) {
	sock := os.Getenv("PF122_SOCK")
	if sock == "" {
		t.Skip("helper process only")
	}
	_, err := cli.NewDaemonClient(sock).Answer(os.Getenv("PF122_WI"),
		cli.AnswerPayload{QuestionID: os.Getenv("PF122_Q"), AnswerText: "self-answer", AnsweredBy: "agent"})
	if err != nil {
		fmt.Printf("PF122-REFUSED %v\n", err)
		return
	}
	fmt.Println("PF122-ACCEPTED")
}

func pf122Daemon(t *testing.T) (*testDaemon, *answerAgent) {
	t.Helper()
	aa := &answerAgent{answered: make(chan string, 4)}
	td := newTestDaemon(t, func(c *Config) {
		inner := fakeEngineStarter()
		c.AgentStarter = func(ctx context.Context, in runtime.AgentStartInput) (Agent, error) {
			h, err := inner(ctx, in)
			if err != nil {
				return nil, err
			}
			aa.Agent = h
			return aa, nil
		}
	})
	td.submitClaim(t, "wi_122")
	td.waitMonitored(t, "wi_122")
	td.seedPendingQuestion(t, "wi_122", "q_122", "grill-review", "Is this spec what you want?")
	return td, aa
}

// runAnswerChild runs argv (the helper, or a shell around it) with the
// given extra environment and answers its printed outcome.
func runAnswerChild(t *testing.T, td *testDaemon, argv []string, env ...string) string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), append([]string{"PF122_SOCK=" + td.sock, "PF122_WI=wi_122", "PF122_Q=q_122"}, env...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("answer child: %v\n%s", err, out)
	}
	return string(out)
}

func helperArgv() []string {
	return []string{os.Args[0], "-test.run=^TestPF122AnswerHelperProcess$", "-test.count=1"}
}

func assertNotDelivered(t *testing.T, aa *answerAgent) {
	t.Helper()
	select {
	case q := <-aa.answered:
		t.Fatalf("REPRODUCED #122: the step process's answer reached question %q", q)
	default:
	}
}

// The #122 scenario: the step agent (carrying the step markers) runs
// `pf answer` against its own question. Refused DAEMON-403, never
// delivered.
func TestAnswerFromStepProcessIsRefused(t *testing.T) {
	td, aa := pf122Daemon(t)
	out := runAnswerChild(t, td, helperArgv(), "PF_WI_ID=wi_122", "PF_DAEMON_ID="+td.cfg.MachineID)
	if !strings.Contains(out, "PF122-REFUSED") || !strings.Contains(out, "DAEMON-403") || !strings.Contains(out, "step process tree") {
		t.Fatalf("REPRODUCED #122: a step process answered a human question: %s", out)
	}
	assertNotDelivered(t, aa)
}

// Clearing the environment does not launder the caller: an `env -u`
// child of a marked step shell is still in the step's tree.
func TestAnswerFromScrubbedChildOfStepIsRefused(t *testing.T) {
	td, aa := pf122Daemon(t)
	h := helperArgv()
	script := `env -u PF_WI_ID -u PF_DAEMON_ID "$0" "$1" "$2"; exit $?`
	out := runAnswerChild(t, td, []string{"sh", "-c", script, h[0], h[1], h[2]},
		"PF_WI_ID=wi_122", "PF_DAEMON_ID="+td.cfg.MachineID)
	if !strings.Contains(out, "PF122-REFUSED") || !strings.Contains(out, "DAEMON-403") {
		t.Fatalf("a scrubbed child of a step shell answered: %s", out)
	}
	assertNotDelivered(t, aa)
}

// The control: the same answer from a human session (no step markers on
// the caller or its ancestors) is delivered. Markers of ANOTHER daemon
// (e.g. this test itself running inside a real step) do not count.
func TestAnswerFromHumanSessionIsDelivered(t *testing.T) {
	td, aa := pf122Daemon(t)
	out := runAnswerChild(t, td, helperArgv(), "PF_WI_ID=wi_other", "PF_DAEMON_ID=another-daemon")
	if !strings.Contains(out, "PF122-ACCEPTED") {
		t.Fatalf("a human session's answer must be accepted: %s", out)
	}
	select {
	case q := <-aa.answered:
		if q != "q_122" {
			t.Fatalf("answer delivered to %q, want q_122", q)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the human answer was never delivered")
	}
}

// ---------------------------------------------------------------------------
// repo review 2026-10-16 V-1: the guard covers every mutating route, not
// only /answer — a step told to "finish the work item" must not be able
// to `pf wrap` it past review, shut the daemon down, flip ops config or
// submit work. Reads stay open.

// TestStepOriginHelperProcess is not a test: re-executed as a child, it
// sends one raw request (PFSO_METHOD PFSO_PATH, body "{}") over the
// socket named in its environment and prints the HTTP status.
func TestStepOriginHelperProcess(t *testing.T) {
	sock := os.Getenv("PFSO_SOCK")
	if sock == "" {
		t.Skip("helper process only")
	}
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}, Timeout: 30 * time.Second}
	req, err := http.NewRequest(os.Getenv("PFSO_METHOD"), "http://pf"+os.Getenv("PFSO_PATH"), strings.NewReader("{}"))
	if err != nil {
		fmt.Printf("PFSO-ERR %v\n", err)
		return
	}
	resp, err := c.Do(req)
	if err != nil {
		fmt.Printf("PFSO-ERR %v\n", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("PFSO-STATUS %d %s\n", resp.StatusCode, body)
}

func stepOriginRequest(t *testing.T, td *testDaemon, method, path string, env ...string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStepOriginHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), append([]string{"PFSO_SOCK=" + td.sock, "PFSO_METHOD=" + method, "PFSO_PATH=" + path}, env...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("request child: %v\n%s", err, out)
	}
	return string(out)
}

func TestMutatingRoutesFromStepProcessAreRefused(t *testing.T) {
	td, _ := pf122Daemon(t)
	marks := []string{"PF_WI_ID=wi_122", "PF_DAEMON_ID=" + td.cfg.MachineID}
	for _, path := range []string{
		"/jobs/wi_122/wrap",
		"/shutdown",
		"/ops/config",
		"/jobs/wi_other/claim",
		"/jobs/wi_122/pause",
		"/chats/c1/subscribe",
		"/chats/c1/subscribe-owned",
	} {
		out := stepOriginRequest(t, td, "POST", path, marks...)
		if !strings.Contains(out, "PFSO-STATUS 403") || !strings.Contains(out, "DAEMON-403") || !strings.Contains(out, "step process tree") {
			t.Errorf("POST %s from a step process was not refused: %s", path, out)
		}
	}
	// The daemon is still up and the job still running: nothing got through.
	if _, live := td.rt.Info("wi_122"); !live {
		t.Fatal("a refused wrap/shutdown still reached the job")
	}
	// Reads stay open to a step.
	if out := stepOriginRequest(t, td, "GET", "/status", marks...); !strings.Contains(out, "PFSO-STATUS 200") {
		t.Fatalf("GET /status from a step process must stay open: %s", out)
	}
	if out := stepOriginRequest(t, td, "GET", "/jobs/wi_122/watch", marks...); strings.Contains(out, "PFSO-STATUS 403") {
		t.Fatalf("GET watch from a step process must stay open: %s", out)
	}
}

// The control: the same mutating requests from a human session pass the
// guard (whatever the route itself then answers, it is not the 403).
func TestMutatingRoutesFromHumanSessionPassTheGuard(t *testing.T) {
	td, _ := pf122Daemon(t)
	for _, path := range []string{"/ops/config", "/jobs/wi_none/claim", "/jobs/wi_none/pause", "/chats/c1/subscribe-owned"} {
		out := stepOriginRequest(t, td, "POST", path, "PF_WI_ID=wi_other", "PF_DAEMON_ID=another-daemon")
		if !strings.Contains(out, "PFSO-STATUS") || strings.Contains(out, "PFSO-STATUS 403") {
			t.Errorf("POST %s from a human session was refused by the guard: %s", path, out)
		}
	}
}
