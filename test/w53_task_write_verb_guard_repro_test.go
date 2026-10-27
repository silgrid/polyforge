package test

// w53_task_write_verb_guard_repro_test.go: workbench#53 (the re-established
// workbench#40) reproduction for the ONE real gate the owner kept in scope
// (owner ruling 2026-10-26, mem_TdI0isaGmTgG): a task process carries
// PF_TASK_ID + PF_DAEMON_ID, and every write verb it attempts must be
// refused DAEMON-403 — at the daemon's UDS face (the gate a task cannot
// talk its way around) and through the real pf CLI.
//
// Baseline posture on the claim-base tree: the whole task lane and its
// guard are absent, so
//   - `pf <write verb>` with the task markers runs on and fails for an
//     unrelated reason (no mapping, no daemon socket, dead hub) — never
//     DAEMON-403, and `pf task spawn` is an unknown command;
//   - a marker-carrying process speaking the daemon's UDS face directly
//     is not classified as a task peer, so its writes are handled normally
//     (POST /config/reload answers 200, POST /shutdown shuts the daemon
//     down) instead of being refused 403.
//
// The file compiles, vets and gofmts against the tree as it stands today:
// it drives only the real pf binary, a real daemon UDS face and the
// existing e2e harness (w3PF/w113Env, w3NewDaemon). No implementation
// internal is read, so the fix is free to place the guard where it belongs.

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
)

const (
	// w53TaskID is the task id the fake task process carries (the shape
	// the plan pins: `task_` + 16 lowercase hex chars).
	w53TaskID = "task_0123456789abcdef"
	// w53ProbeSockEnv hands the daemon socket to the re-exec'd probe.
	w53ProbeSockEnv = "W53_TASK_PROBE_SOCK"
)

// w53TaskEnv builds the environment of one fake task process: the ambient
// env with every polyforge marker stripped (the test run itself may be a
// step or a task), plus exactly the two task markers. The daemon's guard
// walks the caller's own environ, so the markers must be on the probe
// process itself.
func w53TaskEnv(sock, daemonID string) []string {
	out := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "PF_WI_ID", "PF_TASK_ID", "PF_DAEMON_ID", "PF_DAEMON_SOCKET", w53ProbeSockEnv:
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		w53ProbeSockEnv+"="+sock,
		"PF_TASK_ID="+w53TaskID,
		"PF_DAEMON_ID="+daemonID,
	)
}

// w53DaemonProbe is one mutating UDS request a task process must not be
// able to deliver. The list covers the lifecycle write verbs the WI names
// (submit/answer/pause/resume/wrap, msg/config knobs, task spawn itself)
// plus /shutdown, the most destructive one.
type w53DaemonProbe struct {
	method, path, body string
}

func w53DaemonProbes() []w53DaemonProbe {
	return []w53DaemonProbe{
		{"POST", "/config/reload", ""},
		{"POST", "/ops/config", `{"auto":false}`},
		{"POST", "/jobs/wi_0000000000000000/pause", ""},
		{"POST", "/jobs/wi_0000000000000000/resume-paused", ""},
		{"POST", "/jobs/wi_0000000000000000/wrap", ""},
		{"POST", "/jobs/wi_0000000000000000/answer", `{"question_id":"q_x","answer":"yes","answered_by":"usr_x"}`},
		{"POST", "/chats/chat_0000000000000000/subscribe", `{"wi":"wi_0000000000000000"}`},
		{"POST", "/chats/chat_0000000000000000/unsubscribe", `{"wi":"wi_0000000000000000"}`},
		{"POST", "/messages/msg_0000000000000000/analyze", ""},
		{"POST", "/task/spawn", `{"project":"e2e","prompt":"x"}`},
		// last: the pre-guard daemon accepts it and stops serving, which
		// is exactly the observed baseline this file records
		{"POST", "/shutdown", ""},
	}
}

// w53Do issues one raw request over the daemon socket and answers
// (status, body). A transport failure answers status 0 with the error text
// as the body, so the probe still prints a line per request.
func w53Do(sock, method, path, body string) (int, string) {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	c := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), method, "http://daemon"+path, strings.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(raw))
}

// TestW53TaskPeerProbeHelper is not a test: re-executed by
// TestW53TaskProcessRefusedAtDaemonFace as the marker-carrying task
// process, it issues the probes and prints one machine-readable line each.
func TestW53TaskPeerProbeHelper(t *testing.T) {
	sock := os.Getenv(w53ProbeSockEnv)
	if sock == "" {
		t.Skip("helper process only: re-executed with the task markers by TestW53TaskProcessRefusedAtDaemonFace")
	}
	for _, p := range w53DaemonProbes() {
		code, body := w53Do(sock, p.method, p.path, p.body)
		fmt.Printf("W53 %s %s -> %d %s\n", p.method, p.path, code, body)
	}
}

// TestW53TaskProcessRefusedAtDaemonFace is the gate: a process carrying
// PF_TASK_ID + PF_DAEMON_ID (and nothing else) talks the daemon's UDS face
// directly, bypassing the pf CLI entirely. Every mutating request must
// answer 403 with the DAEMON-403 envelope. RED on the claim-base tree: no
// task classification exists, so the same requests reach their handlers.
func TestW53TaskProcessRefusedAtDaemonFace(t *testing.T) {
	d, _ := w3NewDaemon(t, nil, []w113WS{{key: t.TempDir(), projects: []string{v1Project}}})
	d.start()

	cmd := exec.Command(os.Args[0], "-test.run=^TestW53TaskPeerProbeHelper$", "-test.count=1")
	cmd.Env = w53TaskEnv(d.sock, d.machineID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the marker-carrying probe process failed: %v\n%s", err, out)
	}
	lines := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "W53 ") {
			continue
		}
		if i := strings.Index(line, " -> "); i >= 0 {
			lines[line[:i]] = line[i+4:]
		}
	}

	for _, p := range w53DaemonProbes() {
		key := "W53 " + p.method + " " + p.path
		got, ok := lines[key]
		if !ok {
			t.Errorf("%s: the task process got no answer (missing probe line)", key)
			continue
		}
		if !strings.HasPrefix(got, "403 ") {
			t.Errorf("%s from a task process must be refused 403; got %q", key, got)
			continue
		}
		if !strings.Contains(got, "DAEMON-403") {
			t.Errorf("%s refused with the wrong envelope; want DAEMON-403, got %q", key, got)
		}
	}
}

// TestW53TaskProcessWriteVerbsRefusedByCLI is the CLI half: the real pf
// binary, carrying the task markers, attempts each write verb. Every
// invocation must exit non-zero carrying DAEMON-403. RED on the claim-base
// tree: the verbs run on and fail for unrelated reasons (no mapping, no
// daemon socket, dead hub) and `pf task spawn` is an unknown command.
func TestW53TaskProcessWriteVerbsRefusedByCLI(t *testing.T) {
	env := w113Env{noConn: true, extra: []string{
		// a dead loopback, so a leaked write fails fast and never reaches
		// a real hub; the guard must refuse before any connection anyway
		"PF_SERVER=http://127.0.0.1:1",
		"PF_TASK_ID=" + w53TaskID,
		"PF_DAEMON_ID=m-w53-e2e",
	}}

	verbs := [][]string{
		{"file", "create", "--project", "e2e", "--goal", "x"},
		{"file", "edit", "wi_x", "--goal", "y"},
		{"file", "dep", "add", "wi_x", "wi_y"},
		{"submit", "wi_x"},
		{"answer", "wi_x", "q_x", "yes"},
		{"pause", "wi_x"},
		{"resume", "wi_x"},
		{"wrap", "wi_x"},
		{"remember", "--project", "e2e", "--type", "fact.note", "--title", "t", "body"},
		{"msg", "send", "--to", "usr_x", "--text", "hi"},
		{"result", "submit", "-"},
		{"task", "spawn", "e2e", "x"},
	}
	for _, args := range verbs {
		out, code := w3PF(t, nil, env, "{}", args...)
		if code == 0 {
			t.Errorf("pf %s from a task process was accepted (exit 0): %q", strings.Join(args, " "), out)
			continue
		}
		if !strings.Contains(out, "DAEMON-403") {
			t.Errorf("pf %s from a task process must be refused DAEMON-403; exit %d, output %q",
				strings.Join(args, " "), code, out)
		}
	}
}
