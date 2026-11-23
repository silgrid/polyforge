package runtime

// Real-harness drive of a pi subagent call's supervision input
// (polyforge#228 AC4): a REAL pi step process, launched against the
// daemon-owned step agent dir, calls the builtin `worker`; the call's
// record stream, folded through the real pi translator, reports the
// sub-agent's inner activity on that call — the input that restarts its
// tool_hung clock.
//
// The step agent dir is the one the runtime refreshed for THIS spawn
// (cfg.StepAgentDir(), refreshed from a scratch operator source before the
// process starts). The source carries the operator's credentials/models and
// a deliberately poisoned worker/extension, so the assertions prove the
// builtin source: the dir's bytes are the embedded resources, the operator
// content never reaches it, and the call names agent `worker` with
// `agentSource: user` (the fork's only source — no `builtin` protocol field
// exists).
//
// OPT-IN with the other real drives (stepagent_realpi_test.go): it burns
// real model tokens, so it runs only with POLYFORGE_TEST_REAL_PI=1; without
// it the test is SKIPPED and says 未执行 (not run), never failed.
// POLYFORGE_TEST_PI_MODEL (optional) names the model as
// `<provider>/<model>`.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

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

// realPiOperatorSource lays out a scratch operator agent directory: the
// operator's credentials and model config (the authorized entry), plus a
// poisoned worker definition and subagent extension that must never reach
// the refreshed step agent dir.
func realPiOperatorSource(t *testing.T, operator string) string {
	t.Helper()
	src := t.TempDir()
	for _, rel := range []string{"auth.json", "models.json"} {
		raw, err := os.ReadFile(filepath.Join(operator, rel))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err == nil {
			if err := os.WriteFile(filepath.Join(src, rel), raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	poison := "---\nname: worker\ndescription: operator poison\n---\nOPERATOR-POISON: never the builtin worker.\n"
	if err := os.MkdirAll(filepath.Join(src, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "agents", "worker.md"), []byte(poison), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "extensions", "subagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extensions", "subagent", "index.ts"), []byte("// OPERATOR-POISON extension\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestStepAgentRealPiSubagentReportsInnerActivity(t *testing.T) {
	if os.Getenv("POLYFORGE_TEST_REAL_PI") != "1" {
		t.Skip("未执行 (not run): this drive launches a REAL pi process with the operator's credentials; set POLYFORGE_TEST_REAL_PI=1 to run it")
	}
	if testing.Short() {
		t.Skip("未执行 (not run): short mode skips the real-pi worker drive")
	}
	piBin := os.Getenv("POLYFORGE_TEST_PI_BIN")
	if piBin == "" {
		piBin = "pi"
	}
	operator := os.Getenv("POLYFORGE_TEST_OPERATOR_AGENT_DIR")
	if operator == "" {
		operator = OperatorAgentDirDefault()
	}
	if _, err := os.Stat(filepath.Join(operator, "auth.json")); err != nil {
		t.Skipf("未执行 (not run): no model service credentials at %s/auth.json: %v", operator, err)
	}
	// pi's extension loader (jiti) caches the transpiled extension under
	// TMPDIR: that cache is the harness's, not a leak of this test
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(os.TempDir(), "jiti")) })

	src := realPiOperatorSource(t, operator)
	capDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(capDir, "session"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{PiBin: piBin, DataDir: t.TempDir(), OperatorAgentDir: src, OperatorAgentDirExplicit: true}
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
		InitialPrompt: `Call the subagent tool exactly once, single mode, with agent "worker"` +
			` and task "Run the bash command: echo WORKER-RAN. Then reply with exactly: WORKER-OK". Then reply with the sub-agent's answer and nothing else.`,
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

	// the builtin source: the dir the process runs against is the refreshed
	// embedded tree, not the scratch operator's poisoned content.
	for _, rel := range []string{"agents/worker.md", "extensions/subagent/index.ts", "extensions/subagent/agents.ts"} {
		want, err := stepagentassets.FS.ReadFile(rel)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(cfg.StepAgentDir(), filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("the refreshed step agent dir lacks %s: %v", rel, err)
		}
		if string(got) != string(want) {
			t.Fatalf("the step agent dir's %s is not the embedded resource (operator content leaked)", rel)
		}
	}
	poison, err := os.ReadFile(filepath.Join(cfg.StepAgentDir(), "agents", "worker.md"))
	if err != nil || strings.Contains(string(poison), "OPERATOR-POISON") {
		t.Fatalf("the operator's poisoned worker reached the step agent dir: %v %q", err, poison)
	}
	if ents, err := os.ReadDir(filepath.Join(cfg.StepAgentDir(), "extensions")); err != nil {
		t.Fatal(err)
	} else {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		if strings.Join(names, ",") != "guard,subagent" {
			t.Fatalf("the step agent dir's extensions = %v, want exactly the embedded guard and subagent", names)
		}
	}

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
	if r := done.details.Results; len(r) != 1 || r[0].Agent != "worker" || r[0].AgentSource != "user" || r[0].ExitCode != 0 {
		t.Fatalf("the subagent call did not run the builtin worker (agent worker, source user): %+v", done.details)
	}
	sink.mu.Lock()
	n := sink.active[done.id]
	sink.mu.Unlock()
	if n == 0 {
		t.Fatalf("the real subagent call %s reported no inner activity to the translator", done.id)
	}
	t.Logf("subagent call %s ran the builtin worker and reported %d inner records", done.id, n)
}
