// carries: internal/polyforge/components/rollback.go
package components

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

// (u18: the field and input landed as zero-value signatures first, so
// this red is an assertion, never a compile error.)

// setPreviousDataLayout rewrites the fixture's previous manifest with
// the given declared data layout (the shape its build reads — the
// owner's ruling of 2026-11-21 added the field for exactly this
// judgment).
func setPreviousDataLayout(t *testing.T, root, dataLayout string) {
	t.Helper()
	target, err := os.Readlink(filepath.Join(root, PreviousLinkname))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifest(filepath.Join(root, target))
	if err != nil {
		t.Fatal(err)
	}
	m.DataLayout = dataLayout
	if err := WriteManifest(filepath.Join(root, target), m); err != nil {
		t.Fatal(err)
	}
}

// setLiveDataLayout writes the data dir's layout-version file — the
// kernel of the live fact the rollback compares against.
func setLiveDataLayout(t *testing.T, layout string) string {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, cellcfg.LayoutVersionFile), []byte(layout+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

// TestComponentsRollbackRefusesAcrossADataLayoutMigration pins AC6's
// "crossed a schema migration" refusal on the data layer (review
// r2#1/#2, the owner's ruling of 2026-11-21): a previous version that
// DECLARES the data layout it reads is refused — changing nothing,
// naming both layouts — when the data dir carries another. A previous
// that declares the live layout, and one that predates the field
// entirely (undeclared), both roll back: the undeclared case is the
// documented operator responsibility, never a silent machine guess.
func TestComponentsRollbackRefusesAcrossADataLayoutMigration(t *testing.T) {
	root := rollbackFixture(t)
	setPreviousDataLayout(t, root, "cells-v1")
	dataDir := setLiveDataLayout(t, "cells-v2")
	before := rootState(t, root)
	face := &fakeFace{}

	err := runRollback(t, root, face, func(o *RollbackOptions) { o.DataDir = dataDir })
	if err == nil || !strings.Contains(err.Error(), "cells-v1") || !strings.Contains(err.Error(), "cells-v2") {
		t.Fatalf("a rollback across a data layout migration must be refused naming both layouts: %v", err)
	}
	if !strings.Contains(err.Error(), "schema") && !strings.Contains(err.Error(), "migration") {
		t.Fatalf("the refusal must name the crossed migration: %v", err)
	}
	if got := rootState(t, root); got != before {
		t.Fatalf("a refused rollback changes nothing:\n%s", got)
	}
	if len(face.calls) != 0 {
		t.Fatalf("a refused rollback never reaches the daemon, got %v", face.callVerbs())
	}

	// the declared-and-matching case rolls back.
	root = rollbackFixture(t)
	setPreviousDataLayout(t, root, "cells-v1")
	dataDir = setLiveDataLayout(t, "cells-v1")
	face = &fakeFace{}
	if err := runRollback(t, root, face, func(o *RollbackOptions) { o.DataDir = dataDir }); err != nil {
		t.Fatalf("a matching declared layout must roll back: %v", err)
	}
	if target, _ := CurrentTarget(root); target != filepath.Join(ReleasesDirname, "v0.2.4") {
		t.Fatalf("the matching-layout rollback must swap, current is %s", target)
	}

	// the undeclared case (a version older than the field) rolls back
	// too — the operator responsibility the ruling wrote down.
	root = rollbackFixture(t)
	dataDir = setLiveDataLayout(t, "cells-v1")
	face = &fakeFace{}
	if err := runRollback(t, root, face, func(o *RollbackOptions) { o.DataDir = dataDir }); err != nil {
		t.Fatalf("an undeclared previous must roll back (the documented operator case): %v", err)
	}
}
