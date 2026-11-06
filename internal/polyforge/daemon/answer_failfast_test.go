package daemon

// answer_failfast_test.go: pins the answer criterion's fail-fast posture
// on the daemon's answer route (polyforge#106) — the legacy no-bridge-
// handle relay fallback is deleted, so a request the driver branch
// cannot consume fails fast with a typed error and no relay executes.
// (The plan named this pin for the cli package; it lives here because
// the route and the in-process fakes it needs are daemon-side.)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

func TestAnswerWithoutLiveBridgeHandleFailsFast(t *testing.T) {
	td := newTestDaemon(t, nil)
	payload := cli.AnswerPayload{AnswerText: "fail fast", AnsweredBy: "u_1"}

	// (1) Not monitored at all (an idle daemon): the observe fallthrough
	// shape — typed 404, never a success.
	if _, err := td.client.Answer("wi_ghost", payload); err == nil {
		t.Fatal("answer for an unmonitored work item was accepted")
	} else {
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != api.ErrDaemonNotFound {
			t.Fatalf("unmonitored answer error = %v, want DAEMON-404", err)
		}
	}

	// (2) Monitored, but the scripted engine carries no bridge Handle
	// (the between-derivations shape): typed 409 naming the missing
	// live driver — and no relay path executes anywhere.
	td.submitClaim(t, "wi_ff")
	td.waitMonitored(t, "wi_ff")
	raw, err := td.client.Answer("wi_ff", payload)
	if err == nil {
		t.Fatalf("answer with no live driver was accepted: %s", raw)
	}
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict {
		t.Fatalf("no-driver answer error = %v, want DAEMON-409", err)
	}
	if !strings.Contains(ae.Detail, "no live driver") {
		t.Fatalf("the refusal must name the missing live driver: %q", ae.Detail)
	}
}

// answerAgent is a fake engine handle that carries the driver's answer
// face (runtime.DriverAnswerFace), so the answer route takes its driver
// branch: the owner-message relay (messages gets the text; the ack it
// answers is a fixed record, or refuse with nothing recorded when set).
type answerAgent struct {
	Agent
	messages chan string
	refuse   error
}

func (a *answerAgent) OwnerMessage(ctx context.Context, text, by string) (protocol.OwnerMessageAck, error) {
	if a.messages != nil {
		a.messages <- text
	}
	if a.refuse != nil {
		return protocol.OwnerMessageAck{}, a.refuse
	}
	return protocol.OwnerMessageAck{Status: "accepted", At: "2026-11-10T21:00:00.000000001Z", Kind: "message", Step: "fix", SpawnN: 1, Delivered: true, Disposition: "started"}, nil
}

// newAnswerDaemon starts a test daemon whose engine handle is aa.
func newAnswerDaemon(t *testing.T, aa *answerAgent) *testDaemon {
	t.Helper()
	return newTestDaemon(t, func(c *Config) {
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
}

// A driver-branch answer is the owner-message relay (workflow contract v2
// §5.2, PR 6d): the words reach the handle's OwnerMessage verbatim and the
// route answers the relay's ack. A request naming a question id or an
// option — the v1 answer's fields, gone with workflow contract v2 change
// 19 — is refused DAEMON-400 before the handle.
func TestAnswerIsRelayed(t *testing.T) {
	aa := &answerAgent{messages: make(chan string, 1)}
	td := newAnswerDaemon(t, aa)
	td.submitClaim(t, "wi_nq")
	td.waitMonitored(t, "wi_nq")
	raw, err := td.client.Answer("wi_nq", cli.AnswerPayload{AnswerText: "/lean, please", AnsweredBy: "u_1"})
	if err != nil {
		t.Fatalf("the relay: %v", err)
	}
	var ack protocol.OwnerMessageAck
	if json.Unmarshal(raw, &ack) != nil || ack.Status != "accepted" || ack.Kind != "message" || ack.At == "" || !ack.Delivered {
		t.Fatalf("the relay's ack: %s", raw)
	}
	select {
	case text := <-aa.messages:
		if text != "/lean, please" {
			t.Fatalf("the handle got %q, want the words verbatim", text)
		}
	default:
		t.Fatal("the handle's OwnerMessage was never reached")
	}
	for _, body := range []string{
		`{"answer_text":"x","answered_by":"u_1","option":"waive"}`,
		`{"answer_text":"x","answered_by":"u_1","question_id":"q1"}`,
	} {
		if code, out := answerRaw(t, td.sock, "wi_nq", body); code != http.StatusBadRequest || !strings.Contains(out, "unknown field") {
			t.Fatalf("%s = %d %s, want DAEMON-400", body, code, out)
		}
	}
	select {
	case text := <-aa.messages:
		t.Fatalf("a refused request reached the handle: %q", text)
	default:
	}
}

// answerRaw posts body verbatim to the answer route over the daemon's UDS.
func answerRaw(t *testing.T, sock, wi, body string) (int, string) {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}, Timeout: 10 * time.Second}
	resp, err := c.Post("http://daemon/jobs/"+wi+"/answer", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST answer: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// The relay's refusals, nothing recorded: the words themselves — empty, or
// over capsule.MaxOwnerMessageBytes (driver.ErrInvalidOwnerMessage) — are
// invalid input, DAEMON-400; no step agent to talk to is a state
// conflict, DAEMON-409 (PR 6d review: both were 409).
func TestAnswerRelayRefusalsAre400ForTheWords409ForTheState(t *testing.T) {
	aa := &answerAgent{messages: make(chan string, 1)}
	td := newAnswerDaemon(t, aa)
	td.submitClaim(t, "wi_rf")
	td.waitMonitored(t, "wi_rf")
	for _, tc := range []struct {
		text   string
		refuse error
		code   string
	}{
		{"", fmt.Errorf("driver: %w: an owner message needs text", driver.ErrInvalidOwnerMessage), api.ErrDaemonBadInput},
		{strings.Repeat("x", capsule.MaxOwnerMessageBytes+1), fmt.Errorf("driver: %w: an owner message is at most %d bytes", driver.ErrInvalidOwnerMessage, capsule.MaxOwnerMessageBytes), api.ErrDaemonBadInput},
		{"lean", errors.New(`driver: no step agent is running or waiting for the owner (step "")`), api.ErrDaemonConflict},
	} {
		aa.refuse = tc.refuse
		_, err := td.client.Answer("wi_rf", cli.AnswerPayload{AnswerText: tc.text, AnsweredBy: "u_1"})
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != tc.code || !strings.Contains(ae.Detail, tc.refuse.Error()) {
			t.Fatalf("%d bytes refused %v: got %v, want %s", len(tc.text), tc.refuse, err, tc.code)
		}
		<-aa.messages
	}
}

// seedOpenAsk writes the capsule's execution.json as a running workflow
// whose step is in progress under dispatch 1 (no v1 question) and one open
// ask of that dispatch into runtime-state/asks.jsonl.
func (td *testDaemon) seedOpenAsk(t *testing.T, wi, step, askAt string) {
	t.Helper()
	td.hub.mu.Lock()
	name := api.PathName(td.hub.slugs[wi], wi)
	td.hub.mu.Unlock()
	dir := filepath.Join(td.cfg.Mapping.Workspaces()[0], "capsules", name)
	exec := capsule.Execution{WorkItemID: wi, WorkflowStatus: capsule.WorkflowStatusRunning, CurrentStep: step, Version: 1,
		Steps: map[string]*capsule.Step{step: {Status: capsule.StepInProgress, Dispatch: 1}}}
	blob, err := json.Marshal(exec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filepath.FromSlash(capsule.ExecutionJSON))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".tmp", blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
	if err := capsule.AppendAsk(dir, capsule.AskLine{At: askAt, Step: step, Spawn: 1, Kind: capsule.AskKindAsk, Text: "lean or compatible?"}); err != nil {
		t.Fatal(err)
	}
}
