package cli

// w159_container_engine_test.go: the [daemon] container_engine key
// (polyforge#159, 84-engine): one of the two backends a verification
// container runs on. Host-only, restart-only — the engine is a Host
// capability (its image and volume storage are not interchangeable), and
// the daemon resolves it once at its start and injects it into every step
// as PF_CONTAINER_ENGINE. The TOML carries exactly "docker" or "podman",
// quoted; an explicit empty string and any other spelling are refused
// naming the key — never a silent fallback to the other engine's storage.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// TestContainerEngineKeyModel: the key is addressed as [daemon]
// container_engine, classified restart-only, and carries the two-value
// enum schema the shared validator and the panel's form read.
func TestContainerEngineKeyModel(t *testing.T) {
	clearEnv(t)
	k, err := parseConfigKey("[daemon] container_engine")
	if err != nil {
		t.Fatalf("parseConfigKey: %v", err)
	}
	if k.kind != keyDaemon || k.name != "container_engine" {
		t.Fatalf("parseConfigKey = %+v, want the [daemon] key container_engine", k)
	}
	if classOfKey(k) != ConfigRestartOnly {
		t.Fatalf("container_engine is %q, want restart-only (a Host restart applies it)", classOfKey(k))
	}
	if !slices.Contains(ConfigKeys(), "container_engine") {
		t.Fatalf("the one key model must list container_engine: %v", ConfigKeys())
	}
	want := map[string]any{"kind": "enum", "values": []string{"docker", "podman"}}
	if got := ConfigSchema("container_engine"); !reflect.DeepEqual(got, want) {
		t.Fatalf("ConfigSchema(container_engine) = %#v, want %#v", got, want)
	}
}

// TestContainerEngineValueRules: an unset key stays "" (the daemon
// resolves the default, docker); "docker" and "podman" parse; an explicit
// empty string and an unknown spelling are refused naming the key — the
// refusal is the whole point (Q3 A: never a fallback).
func TestContainerEngineValueRules(t *testing.T) {
	clearEnv(t)
	cfg, err := LoadConfig(writeTempConfig(t, "[daemon]\nstep_timeout = \"45m\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Daemon.ContainerEngine != "" {
		t.Fatalf("an unset container_engine is \"\" (the daemon resolves docker), got %q", cfg.Daemon.ContainerEngine)
	}
	for _, v := range []string{"docker", "podman"} {
		cfg, err := LoadConfig(writeTempConfig(t, "[daemon]\ncontainer_engine = \""+v+"\"\n"))
		if err != nil {
			t.Fatalf("container_engine = %q must parse: %v", v, err)
		}
		if cfg.Daemon.ContainerEngine != v {
			t.Fatalf("container_engine = %q parsed as %q", v, cfg.Daemon.ContainerEngine)
		}
	}
	for _, bad := range []string{``, `k8s`, `Docker`, `containerd`, `podman docker`} {
		content := "[daemon]\ncontainer_engine = "
		switch bad {
		case ``:
			content += `""`
		default:
			content += `"` + bad + `"`
		}
		_, err := LoadConfig(writeTempConfig(t, content+"\n"))
		if err == nil {
			t.Fatalf("container_engine = %q must be refused", bad)
		}
		if !strings.Contains(err.Error(), "container_engine") {
			t.Fatalf("the refusal must name the key: %v", err)
		}
	}
}

// TestContainerEngineResolveDefaultIsDocker: the resolution the daemon's
// start applies — an unset key is docker, a stated key is itself; the
// value is validated before this (the parse refuses everything else),
// so the resolution never sees a third spelling.
func TestContainerEngineResolveDefaultIsDocker(t *testing.T) {
	for _, tc := range []struct{ v, want string }{
		{"", "docker"},
		{"docker", "docker"},
		{"podman", "podman"},
	} {
		if got := ResolveContainerEngine(tc.v); got != tc.want {
			t.Errorf("ResolveContainerEngine(%q) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

// TestContainerEngineIsRefusedInANamedCellFile: container_engine is a
// Host-only key — a named Cell's file never carries it (the Host file
// owns it; every Cell of the Host runs on the one engine).
func TestContainerEngineIsRefusedInANamedCellFile(t *testing.T) {
	clearEnv(t)
	cell := filepath.Join(t.TempDir(), "cli-public.toml")
	if err := os.WriteFile(cell, []byte("[daemon]\ncontainer_engine = \"podman\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCellFile(Config{Mapping: &mapping.Plan{}}, cell)
	if err == nil || !strings.Contains(err.Error(), "container_engine") {
		t.Fatalf("a named Cell file must refuse the Host-only key naming it: %v", err)
	}
}

// TestContainerEngineConfigFace (review r1#1): the key's read faces know
// it like its siblings — `pf config get` answers the file's podman, the
// documented default docker when unset, `pf config list` carries the set
// value, and the CAS snapshot records it (a write's precondition compares
// against exactly this value).
func TestContainerEngineConfigFace(t *testing.T) {
	clearEnv(t)
	s := DaemonSettings{ContainerEngine: "podman"}
	if got := daemonValue(s, "container_engine"); got != "podman" {
		t.Fatalf("daemonValue(container_engine) = %q, want podman", got)
	}
	if got := daemonValue(DaemonSettings{}, "container_engine"); got != "" {
		t.Fatalf("an unset key renders empty, got %q", got)
	}
	if got := daemonDefault("container_engine"); got != "docker" {
		t.Fatalf("daemonDefault(container_engine) = %q, want the documented default docker", got)
	}
	// the CAS snapshot: the file's value reaches it verbatim
	raw := []byte("[daemon]\ncontainer_engine = \"podman\"\n")
	snap := configSnapshotValues(raw, true)
	if snap["container_engine"] != "podman" {
		t.Fatalf("the CAS snapshot carries the set value: %v", snap)
	}
	empty := configSnapshotValues([]byte("server = \"http://hub.example:1\"\n"), true)
	if _, ok := empty["container_engine"]; ok {
		t.Fatalf("an unset key stays out of the snapshot (its comparison value is the default's): %v", empty)
	}

	// the verb faces, over the real file: get reads the set value back
	xdg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"),
		[]byte("[daemon]\ncontainer_engine = \"podman\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"XDG_CONFIG_HOME": xdg}
	code, stdout, stderr := runPf(t, env, "config", "get", "container_engine")
	if code != 0 {
		t.Fatalf("config get exited %d: %s", code, stderr)
	}
	if want := "container_engine = podman (source: file)\n"; stdout != want {
		t.Fatalf("config get = %q, want %q", stdout, want)
	}
	code, stdout, stderr = runPf(t, env, "--json", "config", "get", "container_engine")
	if code != 0 {
		t.Fatalf("config get --json exited %d: %s", code, stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("config get --json did not print JSON: %v\n%s", err, stdout)
	}
	if doc["value"] != "podman" || doc["source"] != "file" {
		t.Fatalf("config get --json = %v, want podman from source file", doc)
	}
	// the list carries the set value
	code, stdout, stderr = runPf(t, env, "--json", "config", "list")
	if code != 0 {
		t.Fatalf("config list exited %d: %s", code, stderr)
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("config list --json did not print JSON: %v\n%s", err, stdout)
	}
	found := false
	for _, it := range listed.Items {
		if it["key"] == "container_engine" {
			found = true
			if it["value"] != "podman" || it["source"] != "file" {
				t.Fatalf("config list item = %v, want podman from source file", it)
			}
		}
	}
	if !found {
		t.Fatal("config list must carry container_engine")
	}
	// unset: get answers the documented default, not <unset>
	fresh := map[string]string{"XDG_CONFIG_HOME": t.TempDir()}
	code, stdout, stderr = runPf(t, fresh, "config", "get", "container_engine")
	if code != 0 {
		t.Fatalf("config get (unset) exited %d: %s", code, stderr)
	}
	if want := "container_engine = docker (source: default)\n"; stdout != want {
		t.Fatalf("config get (unset) = %q, want %q (docs/reference/config.md's defaults table)", stdout, want)
	}
}
