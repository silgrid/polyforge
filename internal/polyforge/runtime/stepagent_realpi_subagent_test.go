package runtime

// Real-harness drive of a pi subagent call's supervision input
// ( AC7): a REAL pi step process, launched exactly as the
// daemon launches one against the dedicated step agent dir, calls the
// subagent tool with an agent the operator dir defines (a test-only
// definition in a temp copy of it); the call's record stream, folded
// through the real pi translator, reports the sub-agent's inner activity
// on that call — the input that restarts its tool_hung clock.
//
// OPT-IN with the other real drives (stepagent_realpi_test.go): it burns
// real model tokens, so it runs only with POLYFORGE_TEST_REAL_PI=1 and
// SKIPS when a gate is missing. POLYFORGE_TEST_PI_MODEL (optional) names
// the model as `<provider>/<model>`.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// realPiTestAgent is the test-only definition the temp operator dir
// carries: no model (the step's own) and no tools line (pi's defaults).
const realPiTestAgent = "pf-test-worker"

// activeSink counts the inner-activity signals per tool call id; every
// other signal is dropped.
type activeSink struct {
	mu     sync.Mutex
	active map[string]int
}

func (a *activeSink) ToolActive(id string) {
	a.mu.Lock()
	a.active[id]++
	a.mu.Unlock()
}
func (a *activeSink) Alive()                            {}
func (a *activeSink) Noise()                            {}
func (a *activeSink) WaitingModel()                     {}
func (a *activeSink) ModelOutput(supervise.OutputKind)  {}
func (a *activeSink) ToolStart(id, name, args string)   {}
func (a *activeSink) ToolEnd(id, name string)           {}
func (a *activeSink) TurnEnded(ok bool, errText string) {}
func (a *activeSink) ErrorSeen(text string)             {}
func (a *activeSink) ProcessExit(supervise.ExitInfo)    {}
func (a *activeSink) ContextWindow(string, int64)       {}

func TestStepAgentRealPiSubagentReportsInnerActivity(t *testing.T) {
	if os.Getenv("POLYFORGE_TEST_REAL_PI") != "1" {
		t.Skip("this drive launches a REAL pi process with the operator's credentials; set POLYFORGE_TEST_REAL_PI=1 to run it")
	}
	if testing.Short() {
		t.Skip("short mode: skipping the real-pi worker drive")
	}
	piBin := os.Getenv("POLYFORGE_TEST_PI_BIN")
	if piBin == "" {
		piBin = "pi"
	}
	src := os.Getenv("POLYFORGE_TEST_OPERATOR_AGENT_DIR")
	if src == "" {
		src = OperatorAgentDirDefault()
	}
	if _, err := os.Stat(filepath.Join(src, "auth.json")); err != nil {
		t.Skipf("no model service credentials at %s/auth.json: %v", src, err)
	}
	if _, err := os.Stat(filepath.Join(src, "extensions", "subagent")); err != nil {
		t.Skipf("no subagent extension at %s/extensions/subagent: %v", src, err)
	}
	// pi's extension loader (jiti) caches the transpiled extension under
	// TMPDIR: that cache is the harness's, not a leak of this test
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(os.TempDir(), "jiti")) })
	// a temp operator dir: the source's credentials, model config and
	// subagent extension, plus the test-only agent definition
	op := t.TempDir()
	for _, rel := range []string{"auth.json", "models.json"} {
		raw, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err == nil {
			if err := os.WriteFile(filepath.Join(op, rel), raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := copyStepAgentTree(filepath.Join(src, "extensions", "subagent"), filepath.Join(op, "extensions", "subagent")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(op, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	def := "---\nname: " + realPiTestAgent + "\ndescription: test-only worker\n---\nDo exactly the task.\n"
	if err := os.WriteFile(filepath.Join(op, "agents", realPiTestAgent+".md"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	capDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(capDir, "session"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{PiBin: piBin, DataDir: t.TempDir(), OperatorAgentDir: op, OperatorAgentDirExplicit: true}
	cfg.FillDefaults()
	sp := newPiSpawner(&cfg)

	sink := &activeSink{active: map[string]int{}}
	tl := supervise.NewPi(sink)
	var (
		mu      sync.Mutex
		records []string
	)
	h, err := sp.Spawn(context.Background(), SpawnParams{
		WorkItemID: "wi_realpi_subagent",
		CapsuleDir: capDir,
		SpawnN:     1,
		Model:      os.Getenv("POLYFORGE_TEST_PI_MODEL"),
		InitialPrompt: `Call the subagent tool exactly once, single mode, with agent "` + realPiTestAgent +
			`" and task "Run the bash command: echo WORKER-RAN. Then reply with exactly: WORKER-OK". Then reply with the sub-agent's answer and nothing else.`,
		Journal: func(pid int, startedAt string) error { return nil },
		OnRecord: func(line string) {
			mu.Lock()
			records = append(records, line)
			mu.Unlock()
			tl.Feed([]byte(line))
		},
	})
	if err != nil {
		t.Fatalf("spawn of the real harness failed: %v", err)
	}
	defer func() { _ = h.Terminate(5 * time.Second) }()

	type call struct {
		id      string
		details struct {
			Results []struct {
				Agent       string `json:"agent"`
				AgentSource string `json:"agentSource"`
				ExitCode    int    `json:"exitCode"`
			} `json:"results"`
		}
	}
	var done *call
	for deadline := time.Now().Add(4 * time.Minute); time.Now().Before(deadline) && done == nil; time.Sleep(500 * time.Millisecond) {
		mu.Lock()
		for _, line := range records {
			var rec struct {
				Type       string `json:"type"`
				ToolName   string `json:"toolName"`
				ToolCallID string `json:"toolCallId"`
				Result     struct {
					Details json.RawMessage `json:"details"`
				} `json:"result"`
			}
			if json.Unmarshal([]byte(line), &rec) != nil || rec.Type != "tool_execution_end" || rec.ToolName != "subagent" {
				continue
			}
			c := &call{id: rec.ToolCallID}
			_ = json.Unmarshal(rec.Result.Details, &c.details)
			done = c
		}
		mu.Unlock()
	}
	if done == nil {
		mu.Lock()
		dump := strings.Join(records, "\n")
		mu.Unlock()
		t.Fatalf("no subagent call ended within the deadline:\n%s", dump)
	}
	if r := done.details.Results; len(r) != 1 || r[0].Agent != realPiTestAgent || r[0].AgentSource != "user" || r[0].ExitCode != 0 {
		t.Fatalf("the subagent call did not run the test agent: %+v", done.details)
	}
	sink.mu.Lock()
	n := sink.active[done.id]
	sink.mu.Unlock()
	if n == 0 {
		t.Fatalf("the real subagent call %s reported no inner activity to the translator", done.id)
	}
	t.Logf("subagent call %s ran %s and reported %d inner records", done.id, realPiTestAgent, n)
}
