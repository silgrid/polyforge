package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestW501NamedConfigListShowsInheritedAgentLanguage(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	host := writeFile(t, dir+"/cli.toml", "agent_language = \"zh-CN\"\n")
	named := writeFile(t, dir+"/cli-alpha.toml", "server = \"http://hub:1\"\napi_key = \"key\"\n")
	_ = host
	ctx := &Context{JSON: false, Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer), cfgScope: configScope{path: named, profile: "alpha"}}
	if err := runConfigList(ctx); err != nil {
		t.Fatal(err)
	}
	got := ctx.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(got, "agent_language = zh-CN (source: default Cell") {
		t.Fatalf("named Cell list did not show inherited language from default Cell:\n%s", got)
	}
	if strings.Contains(got, "agent_language = <unset>") {
		t.Fatalf("named Cell list still showed unset language:\n%s", got)
	}
}

func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
