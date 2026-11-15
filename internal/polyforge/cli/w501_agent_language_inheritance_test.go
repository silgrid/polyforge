package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestW501NamedConfigListShowsInheritedAgentLanguage(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	host := writeFile(t, dir+"/cli.toml", "[daemon]\nagent_language = \"zh-CN\"\n")
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

func TestW501NamedConfigListShowsDefaultWhenBothCellsUnset(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	host := writeFile(t, dir+"/cli.toml", "")
	named := writeFile(t, dir+"/cli-alpha.toml", "server = \"http://hub:1\"\napi_key = \"key\"\n")
	_ = host
	ctx := &Context{Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer), cfgScope: configScope{path: named, profile: "alpha"}}
	if err := runConfigList(ctx); err != nil {
		t.Fatal(err)
	}
	got := ctx.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(got, "agent_language = en (source: default") {
		t.Fatalf("both unset must show effective en:\n%s", got)
	}
	if strings.Contains(got, "agent_language = <unset>") {
		t.Fatalf("both unset must not show <unset>:\n%s", got)
	}
}

func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// getConfigGet runs `config get agent_language` under the given scope and
// answers stdout (polyforge#47 AC4: get inherits the default Cell's
// language the way list does, with the same source label).
func getConfigGet(t *testing.T, hostPath, profilePath, profile string, json bool) string {
	t.Helper()
	_ = hostPath
	ctx := &Context{JSON: json, Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer), cfgScope: configScope{path: profilePath, profile: profile}}
	if err := runConfigGet(ctx, []string{"agent_language"}); err != nil {
		t.Fatal(err)
	}
	return ctx.Stdout.(*bytes.Buffer).String()
}

func TestW501NamedConfigGetShowsInheritedAgentLanguage(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	host := writeFile(t, dir+"/cli.toml", "[daemon]\nagent_language = \"zh-CN\"\n")
	named := writeFile(t, dir+"/cli-alpha.toml", "server = \"http://hub:1\"\napi_key = \"key\"\n")

	// the named Cell leaves agent_language unset: get shows the default
	// Cell's value with its source, exactly like list.
	if got := getConfigGet(t, host, named, "alpha", false); got != "agent_language = zh-CN (source: default Cell)\n" {
		t.Fatalf("named Cell get did not show inherited language from default Cell:\n%q", got)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(getConfigGet(t, host, named, "alpha", true)), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["value"] != "zh-CN" || doc["source"] != "default Cell" {
		t.Fatalf("named Cell get --json did not show inherited language from default Cell: %v", doc)
	}

	// both Cells unset: the effective default, like list.
	emptyHost := writeFile(t, dir+"/cli.toml", "")
	if got := getConfigGet(t, emptyHost, named, "alpha", false); got != "agent_language = en (source: default)\n" {
		t.Fatalf("both unset must show the effective default:\n%q", got)
	}

	// the named Cell's own value wins with its own source.
	set := writeFile(t, dir+"/cli-alpha.toml", "server = \"http://hub:1\"\napi_key = \"key\"\n[daemon]\nagent_language = \"en\"\n")
	if got := getConfigGet(t, host, set, "alpha", false); got != "agent_language = en (source: file)\n" {
		t.Fatalf("the named Cell's own value must keep source file:\n%q", got)
	}
}
