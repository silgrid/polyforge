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

// TestContainerEngineResolveDefaultIsDocker lives in u4's tests (the
// resolution is the daemon-start half of the chain).

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
