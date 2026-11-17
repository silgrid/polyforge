package cli

// carries: internal/polyforge/cli/verbs_doctor.go, internal/polyforge/cli/w20_doctor_*_test.go

// w20_doctor_mapping_test.go (polyforge#20): the `workspace mapping` row's
// verdict is machine-wide (Q2=c): a Cell without a [workspace] mapping
// warns — naming its closed execution plane — while any other Cell of
// the machine has one; only a machine whose every Cell lacks a mapping
// fails (exit 1), matching `pf daemon ensure`'s gate.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w20MappingFixture builds a dual-Cell machine whose two config files
// carry mappings as told (defMapping/alphaMapping: "" = none), over the
// w20 fake Host (per-profile GET /status, per-Cell AIHubs). It answers
// the runPf env and the config dir.
func w20MappingFixture(t *testing.T, defMapping, alphaMapping string) (map[string]string, string) {
	t.Helper()
	def, alpha := w20FakeHub(t), w20FakeHub(t)
	wsD, wsA := t.TempDir(), t.TempDir()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	cfgDir := filepath.Join(xdg, "polyforge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wf := t.TempDir()
	writeLane(t, wf, "develop", "steps:\n  - { id: build }\n  - { id: review }\n  - { id: sync }\n  - { id: verification }\n  - { id: publish }\n  - { id: merge }\n")
	bin := t.TempDir()
	for _, tool := range []string{"pf", "git"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sock := w20CellHost(t, def, alpha, wsD, wsA, wf, bin, &reqRecorder{})
	host := "server = " + jsonBody(def.srv.URL) + "\napi_key = \"kd\"\ndaemon_socket = " + jsonBody(sock) + "\n"
	if defMapping != "" {
		host += "\n[workspace.\"" + defMapping + "\"]\nprojects = [\"dproj\"]\n"
	}
	cell := "server = " + jsonBody(alpha.srv.URL) + "\napi_key = \"ka\"\n"
	if alphaMapping != "" {
		cell += "\n[workspace.\"" + alphaMapping + "\"]\nprojects = [\"aproj\"]\n"
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "cli.toml"), []byte(host), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "cli-alpha.toml"), []byte(cell), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"XDG_CONFIG_HOME": xdg, "POLYFORGE_DATA_DIR": t.TempDir()}, cfgDir
}

// TestW20DoctorMappingVerdict (AC3): the mapping row judges the whole
// machine — a mapping-less Cell warns (its execution plane closed) while
// another Cell has a mapping; every Cell mapping-less fails with exit 1;
// a Cell with a mapping passes as before.
func TestW20DoctorMappingVerdict(t *testing.T) {
	t.Run("default unmapped, alpha mapped", func(t *testing.T) {
		env, _ := w20MappingFixture(t, "", t.TempDir())
		code, stdout, stderr := runPf(t, env, "--json", "doctor")
		if code != 0 {
			t.Fatalf("a machine whose alpha Cell maps: exit %d (stdout %s stderr %s)", code, stdout, stderr)
		}
		var doc w20Doc
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("doctor --json: %v", err)
		}
		if !doc.OK {
			t.Fatalf("ok = false: the mapping-less default Cell must not fail the machine (%+v)", doc.Checks)
		}
		st, detail := w20Find(t, doc, "default", "workspace mapping")
		if st != "warn" {
			t.Fatalf("default mapping row = %s %q, want warn (another Cell has a mapping)", st, detail)
		}
		if !strings.Contains(detail, "execution plane is closed") {
			t.Fatalf("the warn must say the execution plane is closed: %q", detail)
		}
		if st, _ := w20Find(t, doc, "alpha", "workspace mapping"); st != "pass" {
			t.Fatalf("alpha mapping row = %s, want pass", st)
		}
	})
	t.Run("every Cell unmapped", func(t *testing.T) {
		env, _ := w20MappingFixture(t, "", "")
		code, stdout, _ := runPf(t, env, "--json", "doctor")
		if code != 1 {
			t.Fatalf("a machine no Cell maps: exit %d, want 1 (stdout %s)", code, stdout)
		}
		var doc w20Doc
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("doctor --json: %v", err)
		}
		if doc.OK {
			t.Fatalf("ok = true on a machine no Cell maps")
		}
		for _, cell := range []string{"default", "alpha"} {
			st, detail := w20Find(t, doc, cell, "workspace mapping")
			if st != "fail" || !strings.Contains(detail, "[workspace") {
				t.Fatalf("%s mapping row = %s %q, want fail naming the [workspace] shape", cell, st, detail)
			}
		}
	})
	t.Run("only the default Cell mapped", func(t *testing.T) {
		env, _ := w20MappingFixture(t, t.TempDir(), "")
		// no named Cell file exists: drop cli-alpha.toml's mapping by
		// removing the file — w20MappingFixture wrote it, so remove it here
		cfgDir := filepath.Join(env["XDG_CONFIG_HOME"], "polyforge")
		if err := os.Remove(filepath.Join(cfgDir, "cli-alpha.toml")); err != nil {
			t.Fatal(err)
		}
		code, stdout, _ := runPf(t, env, "--json", "doctor")
		if code != 0 {
			t.Fatalf("default-only machine: exit %d (stdout %s)", code, stdout)
		}
		var doc w20Doc
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("doctor --json: %v", err)
		}
		st, detail := w20Find(t, doc, "default", "workspace mapping")
		if st != "pass" || !strings.Contains(detail, "1 workspace(s) mapping 1 project(s)") {
			t.Fatalf("default mapping row = %s %q, want the pass of old", st, detail)
		}
		for _, c := range doc.Checks {
			if c.Cell == "alpha" {
				t.Fatalf("an alpha row without an alpha Cell file: %+v", c)
			}
		}
	})
}
