package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestW501AgentLanguageReloadIsLive: a reload that changes
// [daemon] agent_language applies it live — the next dispatch reads the
// new value — and removing the key restores the default "en".
func TestW501AgentLanguageReloadIsLive(t *testing.T) {
	td := newTestDaemon(t, nil)
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cli.toml")
	td.cfg.ConfigPath = path
	base := "[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n"
	write := func(extra string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(base+extra), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := td.live.AgentLanguage(); got != "en" {
		t.Fatalf("premise: no key, the live language is the default en; got %q", got)
	}
	write("\n[daemon]\nagent_language = \"zh-CN\"\n")
	resp, err := td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, o := range resp.Outcomes {
		status[o.Key] = o.Status + "/" + o.Class
	}
	if status["agent_language"] != "applied/live" {
		t.Fatalf("outcomes = %v, want agent_language applied/live", status)
	}
	if got := td.live.AgentLanguage(); got != "zh-CN" {
		t.Fatalf("live language after reload = %q, want zh-CN", got)
	}
	write("")
	resp, err = td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	status = map[string]string{}
	for _, o := range resp.Outcomes {
		status[o.Key] = o.Status + "/" + o.Class
	}
	if status["agent_language"] != "applied/live" {
		t.Fatalf("outcomes after removing the key = %v, want agent_language applied/live", status)
	}
	if got := td.live.AgentLanguage(); got != "en" {
		t.Fatalf("live language after removing the key = %q, want en", got)
	}
}
