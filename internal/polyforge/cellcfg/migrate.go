package cellcfg

// migrate.go: the one-time cell-layout migration of a stopped Host's data
// dir (`pf migrate cell-layout`). The pre-Cell root's per-Cell items are
// COPIED (never moved) into cells/default through a temporary directory
// and published one by one by rename; layout-version is written last.
// (The chat-subs.json annotation the migration once made is gone with the
// chat-subscription model, that file is dead state `pf
// doctor --prune-chat-state` removes.) The untouched root copies
// are the pre-migration copy a rollback returns to, allowed until the Host
// stamps cells/default/migration-accepted-work and only while the new
// layout is byte-identical to that copy (RollbackMigration). An
// interrupted run (no layout-version yet) converges on a re-run: a
// published item whose content hash matches is left alone, a partial or
// different one is replaced (no Host can have written it: the runtime
// refuses a dir without layout-version). A
// completed layout (layout-version written) is never re-planned: its
// re-apply is a no-op, so no state written after the migration is ever
// replaced or deleted.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
)

// MigrationItem is one manifest row.
type MigrationItem struct {
	// Item is the member name; Source/Dest its paths.
	Item   string `json:"item"`
	Source string `json:"source"`
	Dest   string `json:"dest"`
	Size   int64  `json:"size"`
	Hash   string `json:"sha256"`
	// Action: copy | done (already published with the same content) |
	// annotate (an in-place WorkRef annotation).
	Action string `json:"action"`
	// Problem names a failed check ("" = none).
	Problem string `json:"problem,omitempty"`
}

// MigrationPlan is the manifest a plan prints and an apply records.
type MigrationPlan struct {
	DataDir string          `json:"data_dir"`
	Items   []MigrationItem `json:"items"`
}

// Pending reports whether anything is left to do.
func (p *MigrationPlan) Pending() bool {
	for _, it := range p.Items {
		if it.Action != "done" {
			return true
		}
	}
	return false
}

// Problems lists every failed check.
func (p *MigrationPlan) Problems() []string {
	var out []string
	for _, it := range p.Items {
		if it.Problem != "" {
			out = append(out, it.Item+": "+it.Problem)
		}
	}
	return out
}

// PlanMigration builds the manifest without writing anything. A completed
// layout answers an empty plan (nothing is scanned or compared); the
// accepted-work marker without layout-version is refused — that state is
// repaired forward, never re-planned from the pre-migration copy.
func PlanMigration(dataDir string) (*MigrationPlan, error) {
	p := &MigrationPlan{DataDir: dataDir}
	dst := CellDir(dataDir, DefaultProfile)
	v, err := ReadLayoutVersion(dataDir)
	if err != nil {
		return nil, err
	}
	if v == CurrentLayout {
		return p, nil
	}
	if _, err := os.Stat(filepath.Join(dst, AcceptedWorkMarker)); err == nil {
		return nil, fmt.Errorf("migrate cell-layout refused: %s exists but %s does not — a Host served this layout, so its state must not be re-planned from the pre-migration copy; nothing was changed (restore %s from a backup with `pf migrate cell-layout --restore-backup`, or run `pf doctor`)",
			filepath.Join(dst, AcceptedWorkMarker), LayoutVersionFile, LayoutVersionFile)
	}
	for _, m := range CellMembers {
		src := filepath.Join(dataDir, m)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		it := MigrationItem{Item: m, Source: src, Dest: filepath.Join(dst, m), Action: "copy"}
		size, hash, err := treeHash(src)
		if err != nil {
			it.Problem = "unreadable: " + err.Error()
		}
		it.Size, it.Hash = size, hash
		if it.Problem == "" {
			if _, dh, derr := treeHash(it.Dest); derr == nil && dh == hash {
				it.Action = "done"
			}
		}
		p.Items = append(p.Items, it)
	}
	// capacity: the copies must fit the data dir's file system
	var need int64
	for _, it := range p.Items {
		if it.Action == "copy" {
			need += it.Size
		}
	}
	if need > 0 {
		if free, err := freeBytes(dataDir); err == nil && uint64(need) > free {
			p.Items = append(p.Items, MigrationItem{Item: "capacity", Dest: CellDir(dataDir, DefaultProfile), Size: need, Action: "check",
				Problem: fmt.Sprintf("the copies need %d bytes, the file system has %d free", need, free)})
		}
	}
	if len(p.Items) > 0 {
		p.Items = append(p.Items, MigrationItem{Item: LayoutVersionFile, Dest: filepath.Join(dataDir, LayoutVersionFile), Action: "write"})
	}
	return p, nil
}

// ApplyMigration carries the plan out (see the file comment). A plan with a
// failed check writes nothing.
func ApplyMigration(dataDir string) (*MigrationPlan, error) {
	p, err := PlanMigration(dataDir)
	if err != nil {
		return nil, err
	}
	if probs := p.Problems(); len(probs) > 0 {
		return p, fmt.Errorf("the migration plan has failed checks; nothing was changed:\n  %s", strings.Join(probs, "\n  "))
	}
	if !p.Pending() {
		return p, nil
	}
	dst := CellDir(dataDir, DefaultProfile)
	// an interrupted earlier run's staging dirs are rebuilt, never reused
	if stale, err := filepath.Glob(filepath.Join(dataDir, ".cell-layout.tmp-*")); err == nil {
		for _, dir := range stale {
			_ = os.RemoveAll(dir)
		}
	}
	tmp := filepath.Join(dataDir, fmt.Sprintf(".cell-layout.tmp-%d", os.Getpid()))
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return p, err
	}
	defer os.RemoveAll(tmp)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return p, err
	}
	for _, it := range p.Items {
		if it.Action != "copy" {
			continue
		}
		staged := filepath.Join(tmp, it.Item)
		if err := copyTree(it.Source, staged); err != nil {
			return p, fmt.Errorf("copy %s: %w", it.Item, err)
		}
		if _, h, err := treeHash(staged); err != nil || h != it.Hash {
			return p, fmt.Errorf("copy %s: the staged copy does not match its source (integrity check)", it.Item)
		}
		if err := os.RemoveAll(it.Dest); err != nil {
			return p, err
		}
		if err := os.Rename(staged, it.Dest); err != nil {
			return p, fmt.Errorf("publish %s: %w", it.Item, err)
		}
		crashpoint.At("migrate-published-" + it.Item)
	}
	migDir := filepath.Join(dataDir, MigrationDirName)
	if err := os.MkdirAll(migDir, 0o700); err != nil {
		return p, err
	}
	manifest, _ := json.MarshalIndent(p, "", "  ")
	if err := writeAtomic(filepath.Join(migDir, "cell-layout-manifest.json"), append(manifest, '\n'), 0o600); err != nil {
		return p, err
	}
	crashpoint.At("migrate-before-version")
	if err := writeAtomic(filepath.Join(dataDir, LayoutVersionFile), []byte(CurrentLayout+"\n"), 0o644); err != nil {
		return p, err
	}
	return p, nil
}

// RestoreScope lists what restoring backupDir over dataDir replaces: every
// current top-level item of the data dir (the new layout's cells/, the
// Host files, layout-version) — the work recorded since the backup is lost
// from the live dir (it is kept aside under migration/replaced-<n>/).
func RestoreScope(dataDir, backupDir string) ([]string, error) {
	if fi, err := os.Stat(backupDir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("restore-backup: %s is not a directory", backupDir)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if keepOnRestore(e.Name()) {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// keepOnRestore names the entries a restore never touches: the migration
// record itself, the live socket and lock, the release tree.
func keepOnRestore(name string) bool {
	switch name {
	case MigrationDirName, "daemon.sock", "daemon.lock", "release":
		return true
	}
	return strings.HasPrefix(name, ".cell-layout.tmp-")
}

// RestoreBackup replaces the data dir's state by a copy of backupDir (an
// explicit, confirmation-gated last resort after the rollback window
// closed): the current items are moved aside under
// migration/replaced-<n>/ (never deleted), then the backup's items are
// copied in. The result is whatever layout the backup carries (a pre-Cell
// backup is refused by the runtime until a new --apply).
func RestoreBackup(dataDir, backupDir string) (string, error) {
	scope, err := RestoreScope(dataDir, backupDir)
	if err != nil {
		return "", err
	}
	migDir := filepath.Join(dataDir, MigrationDirName)
	if err := os.MkdirAll(migDir, 0o700); err != nil {
		return "", err
	}
	var aside string
	for n := 1; ; n++ {
		aside = filepath.Join(migDir, fmt.Sprintf("replaced-%d", n))
		if _, err := os.Lstat(aside); os.IsNotExist(err) {
			break
		}
	}
	if err := os.MkdirAll(aside, 0o700); err != nil {
		return "", err
	}
	for _, name := range scope {
		if err := os.Rename(filepath.Join(dataDir, name), filepath.Join(aside, name)); err != nil {
			return aside, fmt.Errorf("restore-backup: move %s aside: %w", name, err)
		}
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return aside, err
	}
	for _, e := range entries {
		if keepOnRestore(e.Name()) {
			continue
		}
		if err := copyTree(filepath.Join(backupDir, e.Name()), filepath.Join(dataDir, e.Name())); err != nil {
			return aside, fmt.Errorf("restore-backup: copy %s: %w", e.Name(), err)
		}
	}
	return aside, nil
}

// RollbackMigration returns the data dir to its pre-migration copy, only
// when that return is byte-exact: refused once a Host served the new
// layout (the marker), and refused — naming every blocker, changing
// nothing — when anything in the new layout differs from what the
// migration wrote (rollbackBlockers). Only then does it mutate.
func RollbackMigration(dataDir string) error {
	dst := CellDir(dataDir, DefaultProfile)
	if _, err := os.Stat(filepath.Join(dst, AcceptedWorkMarker)); err == nil {
		return fmt.Errorf("rollback refused: the Host already served the new layout and may have accepted work on it (%s exists) — restoring the pre-migration copy would lose it; nothing was changed; repair forward instead (`pf doctor`)", filepath.Join(dst, AcceptedWorkMarker))
	}
	if v, _ := ReadLayoutVersion(dataDir); v == "" {
		return fmt.Errorf("rollback: %s carries no %s — nothing was migrated", dataDir, LayoutVersionFile)
	}
	if blockers := rollbackBlockers(dataDir); len(blockers) > 0 {
		return fmt.Errorf("rollback refused: the new layout holds state the pre-migration copy does not, and a rollback would delete it; nothing was changed; repair forward instead (`pf doctor`):\n  %s", strings.Join(blockers, "\n  "))
	}
	// the version gate goes first: from here the runtime refuses the dir
	// until the rollback completed or a new apply ran
	if err := os.Remove(filepath.Join(dataDir, LayoutVersionFile)); err != nil {
		return err
	}
	return os.RemoveAll(dst)
}

// rollbackBlockers lists everything that keeps a rollback from being a
// byte-exact return to the pre-migration copy: a named Cell's directory,
// an entry of cells/default that is no migrated member, and a member
// whose content differs from its root copy.
func rollbackBlockers(dataDir string) []string {
	var out []string
	cells := filepath.Join(dataDir, CellsDirName)
	if entries, err := os.ReadDir(cells); err == nil {
		for _, e := range entries {
			if e.Name() != DefaultProfile {
				out = append(out, filepath.Join(CellsDirName, e.Name())+": a Cell created after the migration")
			}
		}
	}
	dst := CellDir(dataDir, DefaultProfile)
	if entries, err := os.ReadDir(dst); err == nil {
		for _, e := range entries {
			rel := filepath.Join(CellsDirName, DefaultProfile, e.Name())
			if !isCellMember(e.Name()) {
				out = append(out, rel+": written after the migration")
				continue
			}
			_, have, herr := treeHash(filepath.Join(dst, e.Name()))
			_, want, werr := treeHash(filepath.Join(dataDir, e.Name()))
			if herr != nil || werr != nil || have != want {
				out = append(out, rel+": differs from its pre-migration copy "+e.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

// isCellMember reports whether name is one of the migrated CellMembers.
func isCellMember(name string) bool {
	for _, m := range CellMembers {
		if m == name {
			return true
		}
	}
	return false
}

// treeHash answers the total size and a content hash over a file or a
// directory tree (relative names, modes and bytes).
func treeHash(root string) (int64, string, error) {
	h := sha256.New()
	var size int64
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return 0, "", err
	}
	sort.Strings(paths)
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return 0, "", err
		}
		rel, _ := filepath.Rel(root, path)
		fmt.Fprintf(h, "%s\x00%o\x00", rel, info.Mode().Perm()|(info.Mode()&fs.ModeType))
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return 0, "", err
			}
			n, err := io.Copy(h, f)
			f.Close()
			if err != nil {
				return 0, "", err
			}
			size += n
		} else if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return 0, "", err
			}
			fmt.Fprint(h, target)
		}
	}
	return size, hex.EncodeToString(h.Sum(nil)), nil
}

// copyTree copies a file or a directory tree, keeping modes (credential
// files stay 0600) and symlinks, fsyncing every file.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			if err := out.Chmod(info.Mode().Perm()); err != nil {
				out.Close()
				return err
			}
			if err := out.Sync(); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
		return nil // sockets, fifos: not state
	})
}
