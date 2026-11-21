// carries: internal/polyforge/runtime/cleanup.go, internal/polyforge/runtime/recover.go, internal/polyforge/runtime/stepagentassets/extensions/subagent/*, internal/polyforge/driver/driver.go, internal/polyforge/driver/handoff.go

package test

// pi_subagent_lifecycle_test.go: AC3's production-path proof
// (polyforge#200, owner ruling Q7 A): a REAL pf daemon and driver, the
// REAL pi as the step harness (--pi-bin), the daemon-owned embedded
// subagent extension really loaded, a test-owned local DETERMINISTIC
// model service (anthropic-messages over loopback — the same wire a
// provider gateway speaks) and a test-owned operator agent dir, on a
// ONE-STEP terminal lane (Q7's sanctioned way to close the
// successor-spawn window: after the step hands off there is no
// successor agent to spawn). The step calls the subagent tool; the
// worker pi runs a bash that leaves a setsid'd, marked sleep behind
// (the ticket's escapee shape); after the lane wraps, nothing carrying
// the work item's PF_WI_ID (+ this daemon's PF_DAEMON_ID) may be alive,
// while three foreign shapes — the same WI under another daemon, the
// same daemon under another WI, and an unmarked process whose cwd sits
// inside the capsule — must all survive.
//
// Linux must run (the owner's Q7 ruling); macOS is not required in the
// same round. A missing real pi on Linux is a loud failure, never a
// skip.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pfruntime "github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// lifecycleLane is the one-step terminal lane the scenario runs on.
const lifecycleLane = "pflifecycle"

// lifecycleHandoffBody carries BOTH heading sets (the daemon checks the
// step's told language; a bilingual body satisfies either).
const lifecycleHandoffBody = `### Conclusion
The production-path lifecycle proof ran.

### The question this step answers
Does a normal hand-off leave no marked process behind?

### What was done
Called the subagent tool, which left a setsid'd marked sleep behind, then handed off.

### Decisions and why
none

### Verification
the deterministic model service scripted this step.

### Problems and risks found
none

### For downstream, and what is open
none

### 结论
生产路径生命周期证明已运行。

### 这一步要回答什么
正常交接后是否留下带标记的残留进程。

### 做了什么
调用 subagent 工具留下一个 setsid 的带标记 sleep，然后交接。

### 关键决定和理由
none

### 验证
确定性模型服务编排了本步骤。

### 发现的问题和风险
none

### 给下游与未决
none
`

// writeLifecycleLane adds the one-step terminal lane to a copied
// workflows tree.
func writeLifecycleLane(t *testing.T, wfDir string) {
	t.Helper()
	dir := filepath.Join(wfDir, lifecycleLane)
	for _, sub := range []string{"steps", "skills"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	yaml := `# yaml-language-server: $schema=../_schema/workflow-1.schema.json
contract: 1
name: pflifecycle
description: One terminal agent step - the production subagent lifecycle proof (polyforge#200): the step calls the daemon-owned worker, hands off done and the lane completes, so the step-end process sweep is judged with no successor spawn racing the scan.
max_rewinds: 3
steps:
  - id: solo
    skill: solo-bounds
    model_candidates: [pi:acme/m]
`
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	step := "---\nname: solo\ndescription: Run the production subagent lifecycle proof and hand off done.\n---\n\n# solo\n\nFollow the skill; the deterministic model service scripts the turn.\n"
	if err := os.WriteFile(filepath.Join(dir, "steps", "solo.md"), []byte(step), 0o644); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: solo-bounds\ndescription: The bounds of the lifecycle proof step.\n---\n\n# solo-bounds\n\nThe step runs the scripted proof and hands off done.\n"
	if err := os.WriteFile(filepath.Join(dir, "skills", "solo-bounds.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
}

// lifecycleModelService is the local deterministic model service: an
// anthropic-messages SSE endpoint whose answers are scripted per role
// (the step vs its worker — the worker's prompt opens with "Task:") and
// turn, so a real pi walks exactly the chain the scenario needs, no
// credentials, no network beyond loopback.
type lifecycleModelService struct {
	mu          sync.Mutex
	stepTurns   int
	workerTurns int
	url         string
}

func newLifecycleModelService(t *testing.T) *lifecycleModelService {
	t.Helper()
	m := &lifecycleModelService{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m.url = "http://" + ln.Addr().String()
	srv := &http.Server{Handler: http.HandlerFunc(m.serve)}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return m
}

// lastUserText digs the last user message's text out of a request.
func lastUserText(body map[string]any) string {
	msgs, _ := body["messages"].([]any)
	for i := len(msgs) - 1; i >= 0; i-- {
		msg, _ := msgs[i].(map[string]any)
		if msg == nil || msg["role"] != "user" {
			continue
		}
		switch c := msg["content"].(type) {
		case string:
			return c
		case []any:
			var b strings.Builder
			for _, blk := range c {
				m, _ := blk.(map[string]any)
				if m == nil {
					continue
				}
				if t, ok := m["text"].(string); ok {
					b.WriteString(t)
				}
			}
			return b.String()
		}
	}
	return ""
}

func (m *lifecycleModelService) serve(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(400)
		return
	}
	m.mu.Lock()
	worker := strings.HasPrefix(strings.TrimSpace(lastUserText(body)), "Task:")
	if worker {
		m.workerTurns++
	} else {
		m.stepTurns++
	}
	stepTurns, workerTurns := m.stepTurns, m.workerTurns
	m.mu.Unlock()

	type block struct {
		kind string // "text" | "tool_use"
		id   string
		name string
		text string // a text block, or the tool_use input JSON
	}
	var blk block
	switch {
	case worker && workerTurns == 1:
		blk = block{kind: "tool_use", id: "toolu_w1", name: "bash",
			text: `{"command":"setsid sleep 600 >/dev/null 2>&1 & echo $! > pf200-grandchild.pid; echo spawned"}`}
	case worker:
		blk = block{kind: "text", text: "worker finished"}
	case stepTurns == 1:
		blk = block{kind: "tool_use", id: "toolu_s1", name: "subagent",
			text: `{"agent":"worker","task":"leave a detached marked sleep behind, then finish"}`}
	case stepTurns == 2:
		blk = block{kind: "tool_use", id: "toolu_s2", name: "bash",
			text: `{"command":"cat > handoff-body.md <<'PFEORF'\n` + lifecycleHandoffBody + `PFEORF\npf step handoff --status done --body @handoff-body.md"}`}
	default:
		blk = block{kind: "text", text: "handed off"}
	}

	w.Header().Set("content-type", "text/event-stream")
	fe := func(event string, obj map[string]any) {
		blob, _ := json.Marshal(obj)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, blob)
	}
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	fe("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_lifecycle", "type": "message", "role": "assistant", "model": "m",
		"content": []any{}, "stop_reason": nil,
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}})
	flush()
	if blk.kind == "text" {
		fe("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""}})
		fe("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": blk.text}})
	} else {
		fe("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "tool_use", "id": blk.id, "name": blk.name, "input": map[string]any{}}})
		fe("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": blk.text}})
	}
	flush()
	fe("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	fe("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 1}})
	fe("message_stop", map[string]any{"type": "message_stop"})
	flush()
}

func (m *lifecycleModelService) calls() (step, worker int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stepTurns, m.workerTurns
}

// lifecycleMarkedProcs answers the live pids whose environment carries
// BOTH this WI's and this daemon's markers — the same fact the daemon's
// layer-4 sweep works from.
func lifecycleMarkedProcs(t *testing.T, wi, daemon string) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 {
			continue
		}
		if !lifecycleProcLive(pid) {
			continue
		}
		env, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil {
			continue
		}
		var hasWI, hasD bool
		for _, kv := range strings.Split(string(env), "\x00") {
			switch kv {
			case "PF_WI_ID=" + wi:
				hasWI = true
			case "PF_DAEMON_ID=" + daemon:
				hasD = true
			}
		}
		if hasWI && hasD {
			out = append(out, pid)
		}
	}
	return out
}

func lifecycleProcLive(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	rest := string(b)
	if i := strings.LastIndex(rest, ")"); i >= 0 {
		rest = rest[i+2:]
	}
	fields := strings.Fields(rest)
	return len(fields) > 0 && fields[0] != "Z"
}

// TestPiSubagentProductionHandoffCleanup: the real chain, the real
// escapee, and the zero-residue claim after a NORMAL hand-off.
func TestPiSubagentProductionHandoffCleanup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the owner's Q7 ruling: the production lifecycle proof must run on Linux; macOS is not required in the same round")
	}
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Fatalf("a real pi is required for the production lifecycle proof: %v", err)
	}

	inst := newV1Install(t)
	srv := newLifecycleModelService(t)

	// the test-owned operator agent dir: the local deterministic model
	// service is the only provider; the daemon copies this into its
	// dedicated step agent dir (the embedded worker and subagent
	// extension win over anything here).
	opDir := t.TempDir()
	models := map[string]any{"providers": map[string]any{
		"acme": map[string]any{
			"baseUrl": srv.url, "api": "anthropic-messages", "apiKey": "lifecycle",
			"models": []any{map[string]any{"id": "m"}},
		}}}
	blob, _ := json.Marshal(models)
	if err := os.WriteFile(filepath.Join(opDir, "models.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opDir, "auth.json"), []byte(`{"acme":{"api_key":"lifecycle"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// the one-step terminal lane in the daemon's workflows copy
	wf := strippedWorkflowsDir(t, filepath.Join(v1Root(t), "workflows"))
	writeLifecycleLane(t, wf)
	inst.strippedWF = wf

	d := newV1Daemon(t, inst,
		func(x *v1Daemon) {
			x.extraArgs = []string{"--pi-bin", piBin, "--operator-agent-dir", opDir}
		},
		// the step's bash runs the real `pf` CLI: the built binary's dir
		// leads the daemon's PATH, which every step inherits.
		optEnv("PATH", filepath.Dir(inst.pfPath)+":"+os.Getenv("PATH")),
	)
	d.start()

	wi := newWIWithBundle(t, inst, "pf200: production subagent lifecycle proof", lifecycleLane, nil)

	// the foreign controls (spawned once the capsule exists, so the third
	// one can sit with its cwd inside it):
	//   1. the same WI under ANOTHER daemon — a hand-over in flight;
	//   2. another WI under THIS daemon — a live sibling ticket;
	//   3. no markers at all, cwd inside the capsule — an operator shell.
	ctl1 := exec.Command("sleep", "300")
	ctl1.Env = append(os.Environ(), "PF_WI_ID="+wi, "PF_DAEMON_ID=other-daemon")
	if err := ctl1.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ctl1.Process.Kill(); _ = ctl1.Wait() }()
	ctl2 := exec.Command("sleep", "300")
	ctl2.Env = append(os.Environ(), "PF_WI_ID=other-wi", "PF_DAEMON_ID="+d.machineID)
	if err := ctl2.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ctl2.Process.Kill(); _ = ctl2.Wait() }()

	out := d.pf(t, "submit", wi)
	if !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	waitUntil(t, 60*time.Second, "the job never started running", func() bool {
		for _, j := range d.status(t).Jobs {
			if j.WorkItemID == wi && j.Status == "running" {
				return true
			}
		}
		return false
	})
	capDir := d.capsuleDir(wi)
	ctl3 := exec.Command("sleep", "300")
	ctl3.Dir = capDir
	if err := ctl3.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ctl3.Process.Kill(); _ = ctl3.Wait() }()

	// the lane is one terminal step: once it wraps, the step-end sweep has
	// run (it precedes the result write that completes the lane) and no
	// successor agent ever spawns — the scan window is closed.
	inst.waitWIStatus(t, wi, "wrapped", 4*time.Minute)
	d.settleClose(wi)

	// the chain really ran: the model service saw the step and its worker
	stepCalls, workerCalls := srv.calls()
	if stepCalls < 2 || workerCalls < 1 {
		t.Fatalf("the scripted chain did not run: step turns %d, worker turns %d (daemon log tail below)\n%s", stepCalls, workerCalls, d.logTail())
	}
	// the worker really left the escapee behind (its pid was recorded)
	rawPid, err := os.ReadFile(filepath.Join(capDir, "pf200-grandchild.pid"))
	if err != nil {
		t.Fatalf("the worker never recorded its detached sleep: %v (daemon log tail below)\n%s", err, d.logTail())
	}
	grandchild, err := strconv.Atoi(strings.TrimSpace(string(rawPid)))
	if err != nil {
		t.Fatalf("bad grandchild pid %q", rawPid)
	}
	if lifecycleProcLive(grandchild) {
		t.Fatalf("the step's setsid'd marked sleep (pid %d) survived the normal hand-off", grandchild)
	}

	// zero residue, positively: no live process carries this WI's and
	// this daemon's markers (settle bounded for the kernel's reaping).
	var left []int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		left = lifecycleMarkedProcs(t, wi, d.machineID)
		if len(left) == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(left) != 0 {
		t.Fatalf("after a normal hand-off, processes carrying the work item's markers remain: %v", left)
	}

	// the foreign controls all survive
	for name, ctl := range map[string]*exec.Cmd{"same WI other daemon": ctl1, "other WI same daemon": ctl2, "unmarked in-capsule cwd": ctl3} {
		if !lifecycleProcLive(ctl.Process.Pid) {
			t.Fatalf("the %s control must survive the sweep (pid %d)", name, ctl.Process.Pid)
		}
	}

	// the subagent conversation was really recorded on the capsule
	metas, _ := filepath.Glob(filepath.Join(capDir, "session", "*", "subagents", "*.meta.json"))
	if len(metas) == 0 {
		t.Fatalf("no subagent sidecar recorded on the capsule (daemon log tail below)\n%s", d.logTail())
	}
}

// TestPiSubagentProductionForeignProcessesSurvive is the negative control
// on its own: a live process carrying THIS daemon's but ANOTHER work
// item's marker (and vice versa) is never touched by any sweep the
// daemon runs — the run above proves the positive face; this one pins
// the sweep's scoping without driving a whole work item.
func TestPiSubagentProductionForeignProcessesSurvive(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the owner's Q7 ruling: the production lifecycle proof must run on Linux; macOS is not required in the same round")
	}
	const wi = "wi-pf200-foreign"
	const daemon = "dm-pf200-foreign"
	foreignWI := exec.Command("sleep", "300")
	foreignWI.Env = append(os.Environ(), "PF_WI_ID=wi-pf200-other", "PF_DAEMON_ID="+daemon)
	if err := foreignWI.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = foreignWI.Process.Kill(); _ = foreignWI.Wait() }()
	foreignDaemon := exec.Command("sleep", "300")
	foreignDaemon.Env = append(os.Environ(), "PF_WI_ID="+wi, "PF_DAEMON_ID=dm-pf200-other")
	if err := foreignDaemon.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = foreignDaemon.Process.Kill(); _ = foreignDaemon.Wait() }()

	// the layer the daemon's step-end and recovery sweeps share: a
	// double-marker match. Neither control matches both halves, so both
	// survive an actual Cleanup of this WI's tree.
	if got := lifecycleMarkedProcs(t, wi, daemon); len(got) != 0 {
		t.Fatalf("no live process may match both markers before the sweep, got %v", got)
	}
	// the sweep the daemon runs (the same Cleanup the runtime wires into
	// the step-end hook): it must leave both foreign processes alive.
	rep, err := pfruntime.Cleanup(pfruntime.CleanupTarget{WIID: wi, DaemonID: daemon}, 5*time.Second)
	if err != nil {
		t.Fatalf("cleanup errored: %v (report %+v)", err, rep)
	}
	if !lifecycleProcLive(foreignWI.Process.Pid) {
		t.Fatal("a process of another work item under this daemon must survive the sweep")
	}
	if !lifecycleProcLive(foreignDaemon.Process.Pid) {
		t.Fatal("a process of this work item under another daemon must survive the sweep")
	}
}
