package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

// Exercise the producer of /api/onboarding's connection.project against a
// real local identity transaction, including interruption and the done gate.
func TestFirstBootPublishedProject(t *testing.T) {
	for _, mode := range []string{"local", "external", "external-no-project"} {
		t.Run(mode, func(t *testing.T) {
			o, hub := fbOpts(t)
			fbSeed(t, o)
			want := "workstation"
			if mode != "local" {
				o.ExtServer, o.ExtKey = "https://hub.example.invalid", "test-key"
				o.ExtProject, want = "existing-project", "existing-project"
				if mode == "external-no-project" {
					o.ExtProject, want = "", ""
				}
			}
			// Interrupt after identity and config have committed.
			if err := os.MkdirAll(filepath.Dir(o.AgentDir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(o.AgentDir, []byte("blocked"), 0o600); err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			if err := runFirstBoot(o, &out); err == nil {
				t.Fatal("expected harness failure")
			}
			check := func(phase string) {
				t.Helper()
				rec := fbRecord(t, o)
				if rec.Phase != phase || rec.Project != want {
					t.Errorf("record phase/project = %q/%q, want %q/%q", rec.Phase, rec.Project, phase, want)
				}
				cfg, err := LoadFileConfig(o.ConfigPath)
				if err != nil {
					t.Fatal(err)
				}
				mapped := cfg.Mapping.ProjectsOf(o.WorkspaceDir)
				if want == "" {
					if len(mapped) != 0 {
						t.Errorf("external boot without project mapped %v", mapped)
					}
				} else if len(mapped) != 1 || mapped[0] != want {
					t.Errorf("mapping = %v, want [%s]", mapped, want)
				}
				if mode == "local" {
					db, err := core.Open(hub)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					var count int
					if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE name=?1`, want).Scan(&count); err != nil || count != 1 {
						t.Fatalf("published project must exist exactly once: count=%d, err=%v", count, err)
					}
				}
				fbAssertNoCredential(t, firstBootPath(o.DataDir), cfg.APIKey)
			}
			check("harness/failed")
			if mode == "local" {
				// An interrupted record written before #282 may omit the
				// project; the resumed identity transaction establishes it.
				rec := fbRecord(t, o)
				rec.Project = ""
				if err := publishFirstBoot(o.DataDir, &rec); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Remove(o.AgentDir); err != nil {
				t.Fatal(err)
			}
			// A resume can omit the external project; the record owns it.
			o.ExtProject = ""
			if err := runFirstBoot(o, &out); err != nil {
				t.Fatal(err)
			}
			check("done")
			read := func(path string) []byte {
				t.Helper()
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			record, config := read(firstBootPath(o.DataDir)), read(o.ConfigPath)
			o.ExtServer, o.ExtKey, o.ExtProject = "https://changed.example.invalid", "changed-key", "changed-project"
			if err := runFirstBoot(o, &out); err != nil {
				t.Fatal(err)
			}
			check("done")
			if !bytes.Equal(record, read(firstBootPath(o.DataDir))) || !bytes.Equal(config, read(o.ConfigPath)) {
				t.Fatal("a completed boot must preserve the record and default Cell")
			}
		})
	}
}

func TestFirstBootProjectNotPublishedBeforeIdentity(t *testing.T) {
	o, _ := fbOpts(t)
	fbSeed(t, o)
	// The local store cannot be opened. No project has been established.
	o.AihubDataDir = filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(o.AihubDataDir, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runFirstBoot(o, &out); err == nil {
		t.Fatal("expected identity failure")
	}
	if rec := fbRecord(t, o); rec.Phase != "identity/failed" || rec.Project != "" {
		t.Fatalf("an unestablished project must not be published: phase=%q, project=%q", rec.Phase, rec.Project)
	}
}
