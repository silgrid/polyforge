package cellcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The Host data-dir layout (docs/reference/config.md, "Data layout"): the
// Host root keeps the Host state; every Cell's state lives under
// <data-dir>/cells/<profile>/. layout-version gates the runtime.
const (
	LayoutVersionFile = "layout-version"
	CurrentLayout     = "cells-v1"
	CellsDirName      = "cells"
	// AcceptedWorkMarker is stamped in cells/default by the Host when it
	// first serves a migrated data dir, before any Cell writes state on
	// the new layout: it closes the rollback window.
	AcceptedWorkMarker = "migration-accepted-work"
	// MigrationDirName holds the migration manifests and the pre-migration
	// copies of the files the migration rewrites in place.
	MigrationDirName = "migration"
)

// CellMembers are the per-Cell state items (files and directories) a Cell
// writes under its directory; the migration moves the old root's copies of
// them into cells/default.
var CellMembers = []string{
	"daemon-id", "machine-key", "runtime-state.json", "claiming.json",
	"submit-queue.json", "manual-models.json", "events", "git", "bar-runs",
	"model-health.json", "task-reports", "step-agent", "step-agent-sessions",
	"claude-config", "summarize-cwd",
}

// CellDir answers a Cell's state directory.
func CellDir(dataDir, profile string) string {
	return filepath.Join(dataDir, CellsDirName, profile)
}

// ReadLayoutVersion answers the data dir's layout version ("" = none).
func ReadLayoutVersion(dataDir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, LayoutVersionFile))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// OldLayoutItems lists the pre-Cell state items present in the data dir's
// root (empty = nothing to migrate).
func OldLayoutItems(dataDir string) []string {
	var out []string
	for _, m := range CellMembers {
		if _, err := os.Lstat(filepath.Join(dataDir, m)); err == nil {
			out = append(out, m)
		}
	}
	return out
}

// ErrMigrationNeeded is the refusal text naming the migration verb.
const migrateHint = "run `pf migrate cell-layout` (plan) then `pf migrate cell-layout --apply` with the daemon stopped"

// CheckLayout gates the runtime on the data dir's layout: the current
// version passes; a different version is refused; no version over pre-Cell
// state is refused naming the migration (never auto-migrated); no version
// over no state is a fresh data dir (fresh=true: the caller initializes it
// with InitLayout).
func CheckLayout(dataDir string) (fresh bool, err error) {
	v, err := ReadLayoutVersion(dataDir)
	if err != nil {
		return false, err
	}
	switch v {
	case CurrentLayout:
		return false, nil
	case "":
		if old := OldLayoutItems(dataDir); len(old) > 0 {
			return false, fmt.Errorf("data dir %s carries the pre-Cell layout (%s) and no %s: %s", dataDir, strings.Join(old, ", "), LayoutVersionFile, migrateHint)
		}
		return true, nil
	default:
		return false, fmt.Errorf("data dir %s has layout version %q, this runtime needs %q: upgrade or downgrade pf to match, or %s", dataDir, v, CurrentLayout, migrateHint)
	}
}

// InitLayout writes the current layout version into a fresh data dir.
func InitLayout(dataDir string) error {
	if err := os.MkdirAll(filepath.Join(dataDir, CellsDirName), 0o755); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dataDir, LayoutVersionFile), []byte(CurrentLayout+"\n"), 0o644)
}

// writeAtomic writes data through a synced temporary file and a rename.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
