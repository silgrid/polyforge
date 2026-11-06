package cli

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestW501AgentLanguageKey: [daemon] agent_language — the
// language of the daemon's step agents — accepts exactly "en" and "zh-CN"
// (quoted), refuses anything else naming the key, renders its value with
// the default "en", is a live [daemon] key with an enum schema, and
// `pf config set` refuses an unknown value without touching the file.
func TestW501AgentLanguageKey(t *testing.T) {
	for _, v := range []string{"en", "zh-CN"} {
		s, err := parseDaemonTable("cli.toml", []daemonKV{{key: "agent_language", val: `"` + v + `"`, line: 1}})
		if err != nil || s.AgentLanguage != v {
			t.Fatalf("agent_language = %q: parsed %+v, %v", v, s, err)
		}
		if got := daemonValue(s, "agent_language"); got != v {
			t.Fatalf("agent_language = %q rendered %q", v, got)
		}
	}
	for _, bad := range []string{`"fr"`, `""`, `en`, `"zh-cn"`} {
		_, err := parseDaemonTable("cli.toml", []daemonKV{{key: "agent_language", val: bad, line: 3}})
		if err == nil || !strings.Contains(err.Error(), "agent_language") || !strings.Contains(err.Error(), "line 3") {
			t.Errorf("agent_language = %s must be refused naming the key and line: %v", bad, err)
		}
	}
	if got := daemonValue(DaemonSettings{}, "agent_language"); got != "" {
		t.Fatalf("an unset agent_language must render unset, got %q", got)
	}
	if got := daemonDefault("agent_language"); got != "en" {
		t.Fatalf("agent_language default = %q, want en", got)
	}
	found := false
	for _, d := range configKeyDefs {
		if d.name == "agent_language" {
			found = d.class == ConfigLive && d.kind == keyDaemon
		}
	}
	if !found {
		t.Fatal("agent_language must be a live [daemon] key")
	}
	want := map[string]any{"kind": "enum", "values": []string{"en", "zh-CN"}}
	if got := ConfigSchema("agent_language"); !reflect.DeepEqual(got, want) {
		t.Fatalf("ConfigSchema(agent_language) = %#v, want %#v", got, want)
	}

	ctx, _ := w22WorkspaceCtx(t)
	before, err := os.ReadFile(ctx.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := runConfigSet(ctx, []string{"agent_language", "fr"}); err == nil || !strings.Contains(err.Error(), "agent_language") {
		t.Fatalf("pf config set agent_language fr must be refused naming the key, got %v", err)
	}
	if after, _ := os.ReadFile(ctx.ConfigPath); !bytes.Equal(before, after) {
		t.Fatalf("a refused set changed the file:\n%s", after)
	}
	if err := runConfigSet(ctx, []string{"agent_language", "zh-CN"}); err != nil {
		t.Fatalf("pf config set agent_language zh-CN: %v", err)
	}
	raw, err := os.ReadFile(ctx.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	i := strings.Index(text, "[daemon]\n")
	table := ""
	if i >= 0 {
		table = text[i:]
		if j := strings.Index(table[1:], "\n["); j >= 0 {
			table = table[:j+2]
		}
	}
	if !strings.Contains(table, "\nagent_language = \"zh-CN\"\n") {
		t.Fatalf("agent_language = \"zh-CN\" was not written under [daemon]:\n%s", text)
	}
	cfg, err := LoadFileConfig(ctx.ConfigPath)
	if err != nil || cfg.Daemon.AgentLanguage != "zh-CN" {
		t.Fatalf("the written file reads %q, %v", cfg.Daemon.AgentLanguage, err)
	}
}
