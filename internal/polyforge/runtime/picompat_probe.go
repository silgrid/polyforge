package runtime

// picompat_probe.go: the real capability probe of polyforge#227. The
// compatibility gate does not trust `pi --help`: it launches the installed
// pi against the daemon-owned resources and a loopback fake provider, and
// proves the subagent tool is registered, callable, and that its result
// returns to the model (owner rulings 2026-11-22 Q1/Q2/D3).
//
// Cost and credential boundary: the probe writes its own models.json with a
// literal fake key and a 127.0.0.1 base URL, runs with a minimal environment
// (PATH/HOME/TMPDIR/PI_* only), PI_OFFLINE=1, a throwaway agent dir and a
// throwaway cwd. It never reads the operator's auth.json/models.json and no
// request leaves the loopback service.
//
// The minimal call is the controlled refusal: a subagent call for an agent
// name the daemon fork does not accept returns an error result without
// starting a worker, and that result is what the fake provider sees on the
// next request (owner ruling Q1; a real worker dispatch is #228's proof).

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// compatProbeAgent is an agent name the daemon fork refuses: the call
	// exercises the real execute path without starting a worker.
	compatProbeAgent = "__pf_compat_probe__"
	// compatProbeRejection is the fixed first sentence of that refusal.
	compatProbeRejection  = "The daemon worker accepts exactly agent=worker with one task"
	compatProbeProviderID = "pf-compat-probe"
	compatProbeModelID    = "pf-compat-probe"
)

// compatProbeProvider is the loopback OpenAI-compatible service: it records
// every request's tool inventory, answers the first request with one
// subagent tool call, and answers the follow-up with a plain completion.
type compatProbeProvider struct {
	mu         sync.Mutex
	toolResult string
	requestCh  chan []string
}

func newCompatProbeProvider() *compatProbeProvider {
	return &compatProbeProvider{requestCh: make(chan []string, 8)}
}

func (p *compatProbeProvider) sawToolResult() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.toolResult, p.toolResult != ""
}

func (p *compatProbeProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	names := make([]string, 0, len(body.Tools))
	offersSubagent := false
	for _, t := range body.Tools {
		names = append(names, t.Function.Name)
		if t.Function.Name == "subagent" {
			offersSubagent = true
		}
	}
	toolResult := ""
	for _, m := range body.Messages {
		if m.Role == "tool" {
			toolResult = string(m.Content)
		}
	}
	p.mu.Lock()
	if toolResult != "" {
		p.toolResult = toolResult
	}
	p.mu.Unlock()
	select {
	case p.requestCh <- names:
	default:
	}

	chunks := []map[string]any{}
	switch {
	case offersSubagent && toolResult == "":
		args, _ := json.Marshal(map[string]string{"agent": compatProbeAgent, "task": "compatibility probe"})
		chunks = append(chunks,
			map[string]any{"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"index": 0, "id": "call_pf_compat", "type": "function",
						"function": map[string]any{"name": "subagent", "arguments": string(args)},
					}},
				},
			}}},
			map[string]any{"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}},
		)
	default:
		chunks = append(chunks,
			map[string]any{"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": "pf compatibility probe complete"}}}},
			map[string]any{"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}},
		)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for i, chunk := range chunks {
		chunk["id"] = "chatcmpl-pf-compat"
		chunk["object"] = "chat.completion.chunk"
		chunk["created"] = i
		chunk["model"] = compatProbeModelID
		raw, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", raw)
		if flusher != nil {
			flusher.Flush()
		}
	}
	io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// piCapabilityProbe runs the one true capability call and answers nil only
// when the built-in extensions loaded, the model was offered the subagent
// tool, the tool executed the controlled refusal, that result returned to
// the model, and the session settled un-aborted.
func piCapabilityProbe(ctx context.Context, bin, version string) error {
	dir, err := os.MkdirTemp("", "pf-pi-compat-")
	if err != nil {
		return piAPIProbeRefusal(bin, version, "no temporary directory for the probe", err)
	}
	defer os.RemoveAll(dir)
	agentDir := filepath.Join(dir, "agent")
	workDir := filepath.Join(dir, "work")
	homeDir := filepath.Join(dir, "home")
	for _, d := range []string{agentDir, workDir, homeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return piAPIProbeRefusal(bin, version, "no temporary directory for the probe", err)
		}
	}
	if err := writeCompatProbeResources(agentDir); err != nil {
		return piAPIProbeRefusal(bin, version, "the built-in resources could not be written for the probe", err)
	}
	provider := newCompatProbeProvider()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return piAPIProbeRefusal(bin, version, "the loopback provider could not listen", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(provider.serve)}
	served := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(served)
	}()
	defer func() {
		_ = server.Close()
		<-served
	}()
	baseURL := "http://" + listener.Addr().String() + "/v1"
	if err := writeCompatProbeModels(filepath.Join(agentDir, "models.json"), baseURL); err != nil {
		return piAPIProbeRefusal(bin, version, "the probe's models.json could not be written", err)
	}

	args := []string{"--mode", "rpc", "--no-session"}
	args = append(args, piIsolationFlags...)
	args = append(args, "--model", compatProbeProviderID+"/"+compatProbeModelID)
	cmd := exec.Command(bin, args...)
	cmd.Dir = workDir
	cmd.Env = compatProbeEnv(agentDir, homeDir, dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return piAPIProbeRefusal(bin, version, "the probe's command channel could not be created", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return piAPIProbeRefusal(bin, version, "the probe's stdout pipe could not be created", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return piAPIProbeRefusal(bin, version, "the probe's stderr pipe could not be created", err)
	}
	cmd.Stdout = outW
	cmd.Stderr = errW
	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		return piAPIProbeRefusal(bin, version, "the target could not start the probe process", err)
	}
	outW.Close()
	errW.Close()
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	done := make(chan struct{})
	lines := make(chan string, 256)
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-done:
				return
			}
		}
		close(lines)
	}()
	stderr := &boundedTail{max: 8 << 10}
	go func() { _, _ = io.Copy(stderr, errR) }()
	stopped := false
	// stop recovers the WHOLE process group this probe started. A graceful
	// stop (the proof passed) closes the command channel and gives the
	// harness a bounded moment to exit; every other stop goes straight to
	// TERM, a bounded grace, KILL and a bounded wait for a proved exit, so
	// the recovery fits inside the budget's last second (owner ruling Q5).
	stop := func(graceful bool) {
		if stopped {
			return
		}
		stopped = true
		_ = stdin.Close()
		if graceful {
			select {
			case <-waitCh:
				close(done)
				outR.Close()
				errR.Close()
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		select {
		case <-waitCh:
		case <-time.After(500 * time.Millisecond):
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			select {
			case <-waitCh:
			case <-time.After(500 * time.Millisecond):
			}
		}
		close(done)
		outR.Close()
		errR.Close()
	}
	defer stop(false)

	if _, err := io.WriteString(stdin, `{"id":"pf-compat-probe","type":"prompt","message":"Run the daemon compatibility probe."}`+"\n"); err != nil {
		return piAPIProbeRefusal(bin, version, "the probe's prompt could not be delivered", err)
	}
	providerSawTools := false
	offersSubagent := false
	executed := false
	settled := false
	for {
		select {
		case <-ctx.Done():
			stop(true)
			return piTimeoutRefusal(bin, version, "capability")
		case names, ok := <-provider.requestCh:
			if !ok {
				continue
			}
			providerSawTools = true
			for _, n := range names {
				if n == "subagent" {
					offersSubagent = true
				}
			}
			if !offersSubagent {
				stop(false)
				return &PiDispatchError{Kind: PiReasonToolRegistration, Detail: "subagent tool is unavailable", Target: bin, Version: version, Stage: "tool-registration"}
			}
		case line, ok := <-lines:
			if !ok {
				stop(false)
				return probeExitRefusal(bin, version, stderr.String())
			}
			var rec struct {
				Type     string `json:"type"`
				ToolName string `json:"toolName"`
				IsError  bool   `json:"isError"`
				Result   struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"result"`
				Aborted bool `json:"aborted"`
			}
			if json.Unmarshal([]byte(line), &rec) != nil {
				continue
			}
			switch rec.Type {
			case "tool_execution_end":
				if rec.ToolName != "subagent" {
					continue
				}
				executed = true
				text := ""
				for _, c := range rec.Result.Content {
					text += c.Text
				}
				if !rec.IsError || !strings.Contains(text, compatProbeRejection) {
					stop(false)
					return &PiDispatchError{Kind: PiReasonAPI, Detail: "extension API probe failed: the subagent tool did not return its controlled refusal", Target: bin, Version: version, Stage: "tool-call"}
				}
			case "agent_settled":
				if rec.Aborted {
					stop(false)
					return &PiDispatchError{Kind: PiReasonAPI, Detail: "extension API probe failed: the minimal call aborted", Target: bin, Version: version, Stage: "settle"}
				}
				settled = true
			}
			result, sawResult := provider.sawToolResult()
			if providerSawTools && offersSubagent && executed && settled && sawResult && strings.Contains(result, compatProbeRejection) {
				stop(true)
				return nil
			}
		case <-waitCh:
			stop(false)
			return probeExitRefusal(bin, version, stderr.String())
		}
	}
}

// writeCompatProbeResources writes the built-in tree the probe will load into
// its throwaway agent directory (the same files the resource validation and
// the digest cover).
func writeCompatProbeResources(dir string) error {
	return fs.WalkDir(piCompatResourceFS, ".", func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			return nil
		}
		raw, err := fs.ReadFile(piCompatResourceFS, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o644)
	})
}

func writeCompatProbeModels(path, baseURL string) error {
	doc := map[string]any{
		"providers": map[string]any{
			compatProbeProviderID: map[string]any{
				"baseUrl": baseURL,
				"api":     "openai-completions",
				"apiKey":  "pf-compat-probe-fake",
				"models": []map[string]any{{
					"id":   compatProbeModelID,
					"name": compatProbeModelID,
					"cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
				}},
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// compatProbeEnv is the probe process's whole environment: no daemon
// secrets, no operator agent dir, no model credentials but the fake literal
// in the probe's own models.json, and no automatic network activity.
func compatProbeEnv(agentDir, homeDir, tmpDir string) []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	return []string{
		"PATH=" + path,
		"HOME=" + homeDir,
		"TMPDIR=" + tmpDir,
		"LANG=C.UTF-8",
		"PI_CODING_AGENT_DIR=" + agentDir,
		"PI_OFFLINE=1",
		"PI_SKIP_VERSION_CHECK=1",
		"PI_TELEMETRY=0",
	}
}

// boundedTail keeps the last max bytes of a stream.
type boundedTail struct {
	max int
	buf []byte
}

func (b *boundedTail) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}

func (b *boundedTail) String() string { return string(b.buf) }

// probeExitRefusal classifies a probe whose process ended before the call
// proved itself: an extension that cannot load is the API class; any other
// early exit is still a failure of the minimal call, never a pass.
func probeExitRefusal(bin, version, stderr string) *PiDispatchError {
	detail := "extension API probe failed: the minimal call did not complete"
	if first := firstNonEmptyLine(stderr); first != "" {
		if len(first) > 240 {
			first = first[:240]
		}
		detail += ": " + first
	}
	return &PiDispatchError{Kind: PiReasonAPI, Detail: detail, Target: bin, Version: version, Stage: "minimal-call"}
}

func piAPIProbeRefusal(bin, version, detail string, err error) *PiDispatchError {
	return &PiDispatchError{Kind: PiReasonAPI, Detail: detail, Target: bin, Version: version, Stage: "probe-setup", Err: err}
}
