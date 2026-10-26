package runtime

// Real-harness drive of the dedicated step agent directory (addendum 2,
// owner ruling A3): a REAL pi process, launched exactly as the daemon
// launches a step process, must (a) still be functional against the
// minimal snapshot — it resolves its model service credentials and
// answers a prompt — and (b) see no harness background-task facility,
// because the dedicated directory declares no packages even when the
// operator's own directory does, while (c) the subagent tool — the
// extension at <operator agent dir>/extensions/subagent/, carried into
// the snapshot — IS in its tool inventory. The inventory is read from the
// harness itself: the RPC export_html command embeds the session's tool
// list (base64 JSON in the session-data script element).
//
// OPT-IN, like the repo's other real-world drives (cf.
// POLYFORGE_TEST_SYSTEM_INSTALL for the installer scenarios): it burns
// real model tokens with the operator's credentials, so it runs only
// with POLYFORGE_TEST_REAL_PI=1 and SKIPS (never hard-fails) when a
// gate is missing — short mode, no pi binary, no credentials to copy.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStepAgentRealPiDedicatedDirectory(t *testing.T) {
	if os.Getenv("POLYFORGE_TEST_REAL_PI") != "1" {
		t.Skip("this drive launches a REAL pi process with the operator's credentials; set POLYFORGE_TEST_REAL_PI=1 to run it")
	}
	if testing.Short() {
		t.Skip("short mode: skipping the real-pi step agent drive")
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

	capDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(capDir, "session"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{PiBin: piBin, DataDir: t.TempDir(), OperatorAgentDir: src, OperatorAgentDirExplicit: true}
	cfg.FillDefaults()
	sp := newPiSpawner(&cfg)

	var (
		mu      sync.Mutex
		records []string
	)
	h, err := sp.Spawn(context.Background(), SpawnParams{
		WorkItemID:    "wi_realpi",
		CapsuleDir:    capDir,
		SpawnN:        1,
		InitialPrompt: "Answer with exactly the word PFREADY and nothing else.",
		Journal:       func(pid int, startedAt string) error { return nil },
		OnRecord: func(line string) {
			mu.Lock()
			records = append(records, line)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("spawn of the real harness against the dedicated dir failed: %v", err)
	}
	defer func() { _ = h.Terminate(5 * time.Second) }()

	// (a) functionality: the minimal snapshot carries enough for the
	// harness to reach its model service and answer.
	deadline := time.Now().Add(3 * time.Minute)
	answered := false
	for time.Now().Before(deadline) && !answered {
		time.Sleep(500 * time.Millisecond)
		mu.Lock()
		for _, line := range records {
			if strings.Contains(line, "PFREADY") {
				answered = true
				break
			}
		}
		mu.Unlock()
	}
	if !answered {
		mu.Lock()
		dump := strings.Join(records, "\n")
		mu.Unlock()
		t.Fatalf("the real harness never answered against the dedicated agent dir %s (credentials/model config did not survive the minimal snapshot):\n%s", cfg.StepAgentDir(), dump)
	}

	// (b) isolation: the dedicated directory declares no packages, so no
	// extension-registered background-task tool can load — whatever the
	// operator's own directory declares.
	_ = filepath.WalkDir(cfg.StepAgentDir(), func(p string, de os.DirEntry, err error) error {
		if err != nil || !de.Type().IsRegular() {
			return nil
		}
		if de.Name() == "settings.json" {
			t.Errorf("the dedicated dir must never carry settings.json (user-level packages): %s", p)
		}
		return nil
	})
	mu.Lock()
	all := strings.Join(records, "\n")
	mu.Unlock()
	for _, probe := range []string{"background_task", "backgroundTask", "pi-background-tasks"} {
		if strings.Contains(all, probe) {
			t.Fatalf("a harness background-task facility is visible to the step process (%q seen in its record stream)", probe)
		}
	}
	// the tool inventory the harness announces, when it announces one,
	// must not contain a background-task tool either
	for _, line := range strings.Split(all, "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if name, _ := rec["tool"].(string); strings.Contains(strings.ToLower(name), "background") {
			t.Fatalf("the step process can call a background tool: %s", line)
		}
	}

	// (c) the tool inventory, straight from the harness: subagent is
	// there, no background-task tool is.
	tools := realPiToolInventory(t, h)
	if !tools["subagent"] {
		t.Fatalf("the step process lost the subagent tool (inventory: %v)", tools)
	}
	for name := range tools {
		if strings.Contains(strings.ToLower(name), "background") {
			t.Fatalf("the step process can call a background tool %q (inventory: %v)", name, tools)
		}
	}
}

// realPiToolInventory asks the live harness to export its session to HTML
// (RPC export_html) and decodes the tool names embedded in it.
func realPiToolInventory(t *testing.T, h AgentHandle) map[string]bool {
	t.Helper()
	p, ok := h.(*proc)
	if !ok {
		t.Fatalf("unexpected handle type %T", h)
	}
	out := filepath.Join(t.TempDir(), "export.html")
	blob, _ := json.Marshal(map[string]string{"id": "pf-tools", "type": "export_html", "outputPath": out})
	p.mu.Lock()
	_, err := p.stdin.Write(append(blob, '\n'))
	p.mu.Unlock()
	if err != nil {
		t.Fatalf("export_html command: %v", err)
	}
	var raw []byte
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		if raw, err = os.ReadFile(out); err == nil && strings.Contains(string(raw), "</html>") {
			break
		}
	}
	const open = `<script id="session-data" type="application/json">`
	html := string(raw)
	i := strings.Index(html, open)
	if i < 0 {
		t.Fatalf("no session-data element in the export (%d bytes)", len(raw))
	}
	rest := html[i+len(open):]
	j := strings.Index(rest, "</script>")
	if j < 0 {
		t.Fatal("unterminated session-data element")
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest[:j]))
	if err != nil {
		t.Fatalf("session-data is not base64: %v", err)
	}
	var data struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(dec, &data); err != nil {
		t.Fatalf("session-data is not JSON: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range data.Tools {
		names[tl.Name] = true
	}
	if len(names) == 0 {
		t.Fatal("the export carries no tool inventory")
	}
	return names
}
