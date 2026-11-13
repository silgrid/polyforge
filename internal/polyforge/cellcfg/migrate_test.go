package cellcfg

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func oldLayout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("daemon-id", "m-old\n", 0o644)
	write("machine-key", "secret\n", 0o600)
	write("events/1.json", "{}\n", 0o644)
	write("chat-subs.json", `{"version":1,"chats":{"c":{"subs":{"wi_a":{"wi":"wi_a","cursor":3}},"muted":{"wi_m":true}}}}`, 0o600)
	write("chats.json", `{"version":1}`, 0o600)
	return dir
}

// TestMigrationPlanApplyConvergeRollback: a plan writes nothing; apply
// copies the members into cells/default (credential modes kept), leaves a
// retired chat-subs.json untouched, writes layout-version
// last, leaves the root
// copies untouched; a re-run and a later plan change nothing; rollback
// restores the pre-migration state; once a Host served the new layout
// (the marker exists) the rollback is refused.
func TestMigrationPlanApplyConvergeRollback(t *testing.T) {
	dir := oldLayout(t)
	p, err := PlanMigration(dir)
	if err != nil || !p.Pending() {
		t.Fatalf("plan: %+v %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(dir, LayoutVersionFile)); err == nil {
		t.Fatal("a plan wrote layout-version")
	}
	if fresh, err := CheckLayout(dir); fresh || err == nil || !strings.Contains(err.Error(), "pf migrate cell-layout") {
		t.Fatalf("the old layout must be refused naming the migration: %v %v", fresh, err)
	}
	if _, err := ApplyMigration(dir); err != nil {
		t.Fatal(err)
	}
	cell := CellDir(dir, DefaultProfile)
	if raw, _ := os.ReadFile(filepath.Join(cell, "daemon-id")); string(raw) != "m-old\n" {
		t.Fatalf("daemon-id = %q", raw)
	}
	if fi, err := os.Stat(filepath.Join(cell, "machine-key")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("machine-key must stay 0600: %v %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "daemon-id")); err != nil {
		t.Fatal("the root copy (the pre-migration copy) must stay")
	}
	if subs, _ := os.ReadFile(filepath.Join(dir, "chat-subs.json")); strings.Contains(string(subs), "profile") {
		t.Fatalf("the retired chat-subs.json must not be rewritten: %s", subs)
	}
	if fresh, err := CheckLayout(dir); fresh || err != nil {
		t.Fatalf("the migrated dir must pass the gate: %v %v", fresh, err)
	}
	if p, err := PlanMigration(dir); err != nil || p.Pending() {
		t.Fatalf("after apply nothing is left: %+v %v", p, err)
	}
	if _, err := ApplyMigration(dir); err != nil {
		t.Fatal(err)
	}

	if err := RollbackMigration(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cell); !os.IsNotExist(err) {
		t.Fatal("rollback left cells/default")
	}
	if _, err := ApplyMigration(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cell, AcceptedWorkMarker), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RollbackMigration(dir); err == nil || !strings.Contains(err.Error(), "accepted work") {
		t.Fatalf("rollback after accepted work must be refused: %v", err)
	}
}

// TestMigrationRepublishesAPartialCopy: a published item whose content
// differs (an interrupted earlier run) is replaced, never kept.
func TestMigrationRepublishesAPartialCopy(t *testing.T) {
	dir := oldLayout(t)
	cell := CellDir(dir, DefaultProfile)
	if err := os.MkdirAll(filepath.Join(cell, "events"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cell, "daemon-id"), []byte("m-ol"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyMigration(dir); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(cell, "daemon-id")); string(raw) != "m-old\n" {
		t.Fatalf("a partial copy survived: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(cell, "events", "1.json")); err != nil {
		t.Fatal("a partial directory was not completed")
	}
}

// TestMigrationChildApply is the child half of the kill -9 test: it runs
// one apply on the dir the parent names and dies at the parent's crash
// point (PF_CRASH_AT). It does nothing when run on its own.
func TestMigrationChildApply(t *testing.T) {
	dir := os.Getenv("PF_MIGRATE_CHILD_DIR")
	if dir == "" {
		t.Skip("child half of TestMigrationConvergesAfterKill9")
	}
	_, _ = ApplyMigration(dir)
}

// TestMigrationConvergesAfterKill9: an apply SIGKILLed after publishing one
// item, or right before the version gate, leaves no layout-version, and a
// re-run converges — every member once in cells/default with the source's
// content.
func TestMigrationConvergesAfterKill9(t *testing.T) {
	for _, point := range []string{"migrate-published-daemon-id", "migrate-before-version"} {
		t.Run(point, func(t *testing.T) {
			dir := oldLayout(t)
			cmd := exec.Command(os.Args[0], "-test.run=^TestMigrationChildApply$", "-test.count=1")
			cmd.Env = append(os.Environ(), "PF_MIGRATE_CHILD_DIR="+dir, "PF_CRASH_AT="+point)
			err := cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != -1 {
				t.Fatalf("the child must die by SIGKILL at %s: %v", point, err)
			}
			if _, err := os.Stat(filepath.Join(dir, LayoutVersionFile)); err == nil {
				t.Fatal("an interrupted apply published layout-version")
			}
			if fresh, err := CheckLayout(dir); fresh || err == nil {
				t.Fatalf("an interrupted dir must still be refused: %v %v", fresh, err)
			}
			if _, err := ApplyMigration(dir); err != nil {
				t.Fatal(err)
			}
			cell := CellDir(dir, DefaultProfile)
			for _, m := range []string{"daemon-id", "machine-key", "events"} {
				_, src, _ := treeHash(filepath.Join(dir, m))
				_, dst, err := treeHash(filepath.Join(cell, m))
				if err != nil || src != dst {
					t.Fatalf("%s did not converge: %v", m, err)
				}
			}
			if entries, _ := filepath.Glob(filepath.Join(dir, ".cell-layout.tmp-*")); len(entries) != 0 {
				t.Fatalf("a re-run left temp dirs: %v", entries)
			}
		})
	}
}

// TestRestoreBackupListsScopeAndKeepsTheReplaced: the scope names the live
// items; a restore moves them aside (never deletes) and copies the backup in.
func TestRestoreBackupListsScopeAndKeepsTheReplaced(t *testing.T) {
	dir := oldLayout(t)
	backup := t.TempDir()
	if err := copyTree(dir, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyMigration(dir); err != nil {
		t.Fatal(err)
	}
	scope, err := RestoreScope(dir, backup)
	if err != nil || !strings.Contains(strings.Join(scope, ","), "cells") || !strings.Contains(strings.Join(scope, ","), LayoutVersionFile) {
		t.Fatalf("scope = %v %v", scope, err)
	}
	aside, err := RestoreBackup(dir, backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(aside, "cells", "default", "daemon-id")); err != nil {
		t.Fatal("the replaced state must be kept aside")
	}
	if _, err := os.Stat(filepath.Join(dir, LayoutVersionFile)); err == nil {
		t.Fatal("the restored (pre-Cell) backup carries no layout-version")
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "daemon-id")); string(raw) != "m-old\n" {
		t.Fatalf("daemon-id = %q", raw)
	}
	if _, err := RestoreScope(dir, filepath.Join(dir, "nope")); err == nil {
		t.Fatal("a missing backup dir must be refused")
	}
}

func TestFreeBytesAnswers(t *testing.T) {
	if n, err := freeBytes(t.TempDir()); err != nil || n == 0 {
		t.Fatalf("freeBytes = %d %v", n, err)
	}
}
