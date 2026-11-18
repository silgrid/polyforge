package test

// carries: .polyforge/capsule-gc.conf, internal/polyforge/capsulegc, internal/polyforge/cli/doctor_rebuildables.go

// pf127_capsule_gc_contract_test.go (polyforge#127, AC5): the
// rebuildable-artifacts cleanup is project-neutral. The cleanup's own
// sources — the capsulegc package and the doctor row that drives it —
// name no project component and no concrete rebuildable path: the
// declaration (.polyforge/capsule-gc.conf, one rebuild_dir= line per
// repository-root-relative directory) is the only place a concrete
// directory is named, and this repository's own declaration is the
// owner-approved first batch, parsed by the shipped parser — one fact,
// one place. A hit is red wherever it appears in the sources: code,
// comment or docstring alike. A test fixture may carry a concrete path
// (the filing analysis's own exception), so the scan covers the
// sources, not the test files.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/capsulegc"
)

// pf127Banned are the components and concrete rebuildable paths the
// cleanup's sources may never name: the components of the Q8 ruling
// (pi-web, webui) and the first batch's own directories (.next,
// node_modules) — a cleanup source that names one of these is the first
// step back to special-casing a project.
var pf127Banned = []string{"pi-web", "webui", ".next", "node_modules"}

// pf127Sources are the cleanup's own source files, repo-relative.
var pf127Sources = []string{
	"internal/polyforge/capsulegc/capsulegc.go",
	"internal/polyforge/cli/doctor_rebuildables.go",
}

// pf127FirstBatch is the owner-approved first batch of this
// repository's own declaration (the ruling of 2026-11-18: only these
// two, everything else proven per item before it joins).
var pf127FirstBatch = []string{"pi-web/.next", "pi-web/node_modules"}

// TestPf127CapsuleGcDeclarationAndNeutrality (AC5): the cleanup's
// sources carry no component name and no concrete rebuildable path, and
// the repository's own declaration exists, parses by the shipped
// parser, and is exactly the owner-approved first batch.
func TestPf127CapsuleGcDeclarationAndNeutrality(t *testing.T) {
	root := v1Root(t)

	t.Run("the cleanup's sources name no component or concrete path", func(t *testing.T) {
		var bad []string
		for _, rel := range pf127Sources {
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatal(err)
			}
			for _, banned := range pf127Banned {
				if strings.Contains(string(raw), banned) {
					bad = append(bad, rel+" names "+banned)
				}
			}
		}
		if len(bad) > 0 {
			t.Fatalf("the rebuildable-artifacts cleanup names a project component or a concrete rebuildable path: %s", strings.Join(bad, "; "))
		}
	})

	t.Run("the repository's own declaration is the owner-approved first batch", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(capsulegc.ConfPath)))
		if err != nil {
			t.Fatalf("this repository declares no %s (AC5: the list lives in the repository's own declaration): %v", capsulegc.ConfPath, err)
		}
		dirs, err := capsulegc.ParseDeclaration(raw)
		if err != nil {
			t.Fatalf("%s does not parse by the shipped parser: %v", capsulegc.ConfPath, err)
		}
		if len(dirs) != len(pf127FirstBatch) || dirs[0] != pf127FirstBatch[0] || dirs[1] != pf127FirstBatch[1] {
			t.Fatalf("the declaration = %v, want the owner-approved first batch %v in file order", dirs, pf127FirstBatch)
		}
	})
}
