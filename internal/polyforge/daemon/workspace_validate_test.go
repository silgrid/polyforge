package daemon

// workspace_validate_test.go pins the startup workspace validation
// (#103): the workspace must pre-exist as a directory (the daemon never
// creates it) and <workspace>/capsules must be creatable and writable —
// the daemon's only write target inside the user's workspace. Every
// failure is a hard New error naming the workspace path; a usable
// workspace assembles the daemon (and capsules/ is then created).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// validateCfg is the minimal valid Config around a workspace: everything
// else is scratch paths under dir plus the fakes the daemon tests use.
func validateCfg(t *testing.T, ws string) Config {
	t.Helper()
	setGitIdentity(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	hubBase := filepath.Join(dir, "hub")
	for _, d := range []string{data, hubBase} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Config{
		MachineID:     "m-wsval",
		SocketPath:    filepath.Join(dir, "s.sock"),
		Mapping:       mappingFor(ws),
		DataDir:       data,
		GitRemoteBase: hubBase,
		MaxConcurrent: 1,
		Hub:           newFakeHub(),
		Spawner:       &fakeSpawner{},
		ObservePoll:   10 * time.Millisecond,
		DrainInterval: 10 * time.Millisecond,
		DispatchTick:  10 * time.Millisecond,
	}
}

func TestValidateWorkspaceMissingDir(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "no-such-workspace")
	_, err := New(validateCfg(t, ws))
	if err == nil {
		t.Fatal("missing workspace dir must be a hard startup error")
	}
	if !strings.Contains(err.Error(), ws) {
		t.Errorf("error %q must name the workspace path", err)
	}
	if _, statErr := os.Stat(ws); !os.IsNotExist(statErr) {
		t.Errorf("the daemon must never create the workspace dir; stat err = %v", statErr)
	}
}

func TestValidateWorkspaceNotADirectory(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.WriteFile(ws, []byte("a file, not a dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New(validateCfg(t, ws))
	if err == nil {
		t.Fatal("a non-directory workspace must be a hard startup error")
	}
	if !strings.Contains(err.Error(), ws) {
		t.Errorf("error %q must name the workspace path", err)
	}
}

func TestValidateWorkspaceCapsulesNotCreatable(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	// A regular file squatting the capsules/ path makes MkdirAll fail —
	// the not-creatable failure class (works under root too, where a
	// chmod-based read-only dir would not).
	if err := os.WriteFile(filepath.Join(ws, "capsules"), []byte("not a dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New(validateCfg(t, ws))
	if err == nil {
		t.Fatal("an uncreatable capsules/ must be a hard startup error")
	}
	if !strings.Contains(err.Error(), ws) {
		t.Errorf("error %q must name the workspace path", err)
	}
}

func TestValidateWorkspaceCapsulesNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission checks: the read-only-dir case cannot be exercised here")
	}
	ws := filepath.Join(t.TempDir(), "ws")
	capsules := filepath.Join(ws, "capsules")
	if err := os.MkdirAll(capsules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(capsules, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(capsules, 0o755) })
	_, err := New(validateCfg(t, ws))
	if err == nil {
		t.Fatal("an unwritable capsules/ must be a hard startup error")
	}
	if !strings.Contains(err.Error(), ws) {
		t.Errorf("error %q must name the workspace path", err)
	}
}

func TestValidateWorkspaceUsable(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := New(validateCfg(t, ws))
	if err != nil {
		t.Fatalf("usable workspace must assemble: %v", err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background(), DirectShutdownCause()) })
	// The daemon created exactly the capsules/ subtree — nothing else
	// outside it (notably never a .gitignore or any sibling file).
	ents, err := os.ReadDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != "capsules" {
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("workspace contents after startup = %v, want only [capsules]", names)
	}
}

// TestValidateAllMappedWorkspaces (#113): New validates EVERY mapped
// workspace — one missing directory among two fails New naming THAT
// workspace; all present, every workspace gets the #103 contract
// (capsules/ created, nothing else).
func TestValidateAllMappedWorkspaces(t *testing.T) {
	wsOK := filepath.Join(t.TempDir(), "ws-ok")
	wsMissing := filepath.Join(t.TempDir(), "ws-missing")
	if err := os.MkdirAll(wsOK, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := validateCfg(t, wsOK)
	cfg.Mapping = mappingFor(wsOK, "proj")
	plan, err := mapping.Build([]mapping.Entry{
		{Workspace: wsOK, Projects: []string{"proj"}},
		{Workspace: wsMissing, Projects: []string{"acme-ops"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Mapping = plan
	if _, err := New(cfg); err == nil {
		t.Fatal("a missing mapped workspace must be a hard startup error")
	} else if !strings.Contains(err.Error(), "ws-missing") {
		t.Errorf("error %q must name the missing workspace", err)
	}
	if _, statErr := os.Stat(wsMissing); !os.IsNotExist(statErr) {
		t.Errorf("the daemon must never create a missing mapped workspace; stat err = %v", statErr)
	}

	// All present: both workspaces get their capsules/, nothing else.
	wsB := filepath.Join(t.TempDir(), "ws-b")
	if err := os.MkdirAll(wsB, 0o755); err != nil {
		t.Fatal(err)
	}
	plan2, err := mapping.Build([]mapping.Entry{
		{Workspace: wsOK, Projects: []string{"proj"}},
		{Workspace: wsB, Projects: []string{"acme-ops"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Mapping = plan2
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("all-present mapping must assemble: %v", err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background(), DirectShutdownCause()) })
	for _, ws := range []string{wsOK, wsB} {
		ents, err := os.ReadDir(ws)
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Name())
		}
		if len(names) != 1 || names[0] != "capsules" {
			t.Fatalf("workspace %s contents = %v, want only [capsules]", ws, names)
		}
	}
}
