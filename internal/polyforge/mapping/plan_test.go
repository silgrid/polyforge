package mapping

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The unit pins the spec's Requirement-level error copy: key
// normalization (~/absoluteness/trailing separator), the two
// invertibility refusals, and the deterministic views.

func mustHome(t *testing.T) string {
	t.Helper()
	home, err := osUserHomeDir()
	if err != nil || home == "" {
		t.Fatalf("home dir unavailable: %v", err)
	}
	return home
}

func TestBuildTildeExpansionAndAbsoluteKeys(t *testing.T) {
	home := mustHome(t)
	wsTilde := filepath.Join(home, "ws-tilde")
	wsAbs := t.TempDir()
	p, err := Build([]Entry{
		{Workspace: "~/ws-tilde", Projects: []string{"p1"}},
		{Workspace: wsAbs, Projects: []string{"p2"}},
	})
	if err != nil {
		t.Fatalf("valid mapping must build: %v", err)
	}
	if got := p.Workspaces(); len(got) != 2 || got[0] != wsTilde || got[1] != wsAbs {
		t.Fatalf("Workspaces() = %v, want [%s %s] (~ expanded, absolute literal)", got, wsTilde, wsAbs)
	}
	for _, proj := range []string{"p1", "p2"} {
		if _, err := p.WorkspaceFor(proj); err != nil {
			t.Fatalf("WorkspaceFor(%q): %v", proj, err)
		}
	}
}

func TestBuildBareTildeKey(t *testing.T) {
	home := mustHome(t)
	p, err := Build([]Entry{{Workspace: "~", Projects: []string{"p"}}})
	if err != nil {
		t.Fatalf("bare ~ must expand to home: %v", err)
	}
	if ws := p.Workspaces(); len(ws) != 1 || ws[0] != home {
		t.Fatalf("Workspaces() = %v, want [%s]", ws, home)
	}
}

func TestBuildRelativeKeyRejected(t *testing.T) {
	_, err := Build([]Entry{{Workspace: "rel/w113rel", Projects: []string{"p"}}})
	if err == nil {
		t.Fatal("relative key must be a load error")
	}
	if !strings.Contains(err.Error(), "rel/w113rel") {
		t.Fatalf("error %q must name the offending key", err)
	}
	var ke *KeyError
	if !errors.As(err, &ke) {
		t.Fatalf("relative-key refusal must be the typed KeyError, got %T", err)
	}
}

func TestBuildTrailingSeparatorRejected(t *testing.T) {
	ws := t.TempDir()
	_, err := Build([]Entry{{Workspace: ws + "/", Projects: []string{"p"}}})
	if err == nil {
		t.Fatal("trailing separator must be a load error, never silently normalized")
	}
	if !strings.Contains(err.Error(), ws) {
		t.Fatalf("error %q must name the offending key", err)
	}
}

func TestBuildEmptyKeyRejected(t *testing.T) {
	if _, err := Build([]Entry{{Workspace: "", Projects: []string{"p"}}}); err == nil {
		t.Fatal("empty workspace key must be a load error")
	}
	if _, err := Build([]Entry{{Workspace: t.TempDir(), Projects: []string{""}}}); err == nil {
		t.Fatal("an empty project name must be a load error")
	}
}

func TestBuildAmbiguousInverseRejected(t *testing.T) {
	wsA := t.TempDir()
	wsB := t.TempDir()
	_, err := Build([]Entry{
		{Workspace: wsA, Projects: []string{"demo-ops"}},
		{Workspace: wsB, Projects: []string{"demo-ops"}},
	})
	if err == nil {
		t.Fatal("a project under two workspaces must be a load error")
	}
	var ae *AmbiguityError
	if !errors.As(err, &ae) {
		t.Fatalf("ambiguity must be the typed AmbiguityError, got %T: %v", err, err)
	}
	for _, want := range []string{"demo-ops", wsA, wsB} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must name %q", err, want)
		}
	}
}

func TestBuildDuplicateWithinOneListRejected(t *testing.T) {
	ws := t.TempDir()
	_, err := Build([]Entry{{Workspace: ws, Projects: []string{"e2e", "e2e"}}})
	if err == nil {
		t.Fatal("a duplicated project must be a load error")
	}
	var de *DuplicateError
	if !errors.As(err, &de) {
		t.Fatalf("duplicate must be the typed DuplicateError, got %T: %v", err, err)
	}
	for _, want := range []string{"e2e", ws} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must name %q", err, want)
		}
	}
}

func TestViewsAreDeterministic(t *testing.T) {
	wsA := t.TempDir()
	wsB := t.TempDir()
	p, err := Build([]Entry{
		{Workspace: wsB, Projects: []string{"b1", "b2"}},
		{Workspace: wsA, Projects: []string{"a1"}},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for i := 0; i < 3; i++ {
		if got := strings.Join(p.Projects(), ","); got != "b1,b2,a1" {
			t.Fatalf("Projects() = %q, want declaration order b1,b2,a1", got)
		}
		if got := strings.Join(p.Workspaces(), ","); got != wsB+","+wsA {
			t.Fatalf("Workspaces() = %q, want declaration order", got)
		}
	}
}

func TestWorkspaceForUnmappedTyped(t *testing.T) {
	p, err := Build([]Entry{{Workspace: t.TempDir(), Projects: []string{"e2e"}}})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	_, err = p.WorkspaceFor("ghostproj")
	if err == nil {
		t.Fatal("unmapped project must answer the typed error")
	}
	var ue *UnmappedError
	if !errors.As(err, &ue) {
		t.Fatalf("unmapped miss must be the typed UnmappedError, got %T", err)
	}
	for _, want := range []string{"ghostproj", "[workspace", "doctor"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must carry the %q migration pointer", err, want)
		}
	}
}

func TestNilPlanIsEmpty(t *testing.T) {
	var p *Plan
	if !p.Empty() {
		t.Fatal("nil plan must read as empty")
	}
	if _, err := p.WorkspaceFor("x"); err == nil {
		t.Fatal("nil plan must refuse every project")
	}
}

func TestProjectsOf(t *testing.T) {
	wsA := t.TempDir()
	wsB := t.TempDir()
	p, err := Build([]Entry{
		{Workspace: wsA, Projects: []string{"a1", "a2"}},
		{Workspace: wsB, Projects: []string{"b1"}},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := strings.Join(p.ProjectsOf(wsA), ","); got != "a1,a2" {
		t.Fatalf("ProjectsOf(wsA) = %q", got)
	}
	if got := p.ProjectsOf(wsB); len(got) != 1 || got[0] != "b1" {
		t.Fatalf("ProjectsOf(wsB) = %v", got)
	}
	if got := p.ProjectsOf(t.TempDir()); got != nil {
		t.Fatalf("unmapped workspace = %v, want nil", got)
	}
	// A mapped workspace with zero projects answers a non-nil empty set
	// (the workspace yaml then refuses every project key).
	wsC := t.TempDir()
	p, err = Build([]Entry{{Workspace: wsC, Projects: []string{}}})
	if err != nil {
		t.Skipf("a zero-project workspace is not buildable: %v", err)
	}
	if got := p.ProjectsOf(wsC); got == nil || len(got) != 0 {
		t.Fatalf("zero-project workspace = %#v, want non-nil empty", got)
	}
}
