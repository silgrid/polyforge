package cellcfg

// w350_migrate_guard_test.go (D1): a completed layout is
// never re-planned (re-apply is a byte-identical no-op, whatever was
// written after it); the accepted-work marker without layout-version is
// refused; a rollback that would not be a byte-exact return to the
// pre-migration copy is refused naming every blocker and changes nothing.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func w350Write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func w350Tree(t *testing.T, dir string) string {
	t.Helper()
	_, h, err := treeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestW350CompletedLayoutReapplyIsANoOp(t *testing.T) {
	dir := oldLayout(t)
	if _, err := ApplyMigration(dir); err != nil {
		t.Fatal(err)
	}
	cell := CellDir(dir, DefaultProfile)
	w350Write(t, filepath.Join(cell, AcceptedWorkMarker), "served\n")
	w350Write(t, filepath.Join(cell, "events", "post-migration.json"), `{"seq":2}`+"\n")
	w350Write(t, filepath.Join(cell, "daemon-id"), "m-old\n") // unchanged member
	before := w350Tree(t, dir)
	for i := 0; i < 2; i++ {
		p, err := ApplyMigration(dir)
		if err != nil || p.Pending() || len(p.Items) != 0 {
			t.Fatalf("re-apply %d of a completed layout: %+v %v", i, p, err)
		}
		if after := w350Tree(t, dir); after != before {
			t.Fatalf("re-apply %d changed the data dir", i)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(cell, "events", "post-migration.json")); string(raw) != `{"seq":2}`+"\n" {
		t.Fatalf("the post-migration event was replaced: %q", raw)
	}
}

func TestW350MarkerWithoutVersionIsRefused(t *testing.T) {
	dir := oldLayout(t)
	w350Write(t, filepath.Join(CellDir(dir, DefaultProfile), AcceptedWorkMarker), "served\n")
	before := w350Tree(t, dir)
	if _, err := PlanMigration(dir); err == nil || !strings.Contains(err.Error(), AcceptedWorkMarker) {
		t.Fatalf("plan with the marker and no layout-version: %v", err)
	}
	if _, err := ApplyMigration(dir); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("apply with the marker and no layout-version: %v", err)
	}
	if w350Tree(t, dir) != before {
		t.Fatal("a refused apply changed the data dir")
	}
}

func TestW350RollbackRefusesUnlessByteExact(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(dir string)
		want  string
	}{
		{"a post-migration event", func(dir string) {
			w350Write(t, filepath.Join(CellDir(dir, DefaultProfile), "events", "post.json"), "{}\n")
		}, "cells/default/events"},
		{"a non-member entry", func(dir string) {
			w350Write(t, filepath.Join(CellDir(dir, DefaultProfile), "effective-connection.json"), "{}\n")
		}, "effective-connection.json"},
		{"a named Cell", func(dir string) {
			w350Write(t, filepath.Join(CellDir(dir, "alpha"), "daemon-id"), "m-a\n")
		}, "cells/alpha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := oldLayout(t)
			if _, err := ApplyMigration(dir); err != nil {
				t.Fatal(err)
			}
			tc.write(dir)
			before := w350Tree(t, dir)
			err := RollbackMigration(dir)
			if err == nil || !strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("rollback with %s: %v", tc.name, err)
			}
			if w350Tree(t, dir) != before {
				t.Fatal("a refused rollback changed the data dir")
			}
		})
	}
	// untouched: the byte-exact rollback still works
	dir := oldLayout(t)
	if _, err := ApplyMigration(dir); err != nil {
		t.Fatal(err)
	}
	if err := RollbackMigration(dir); err != nil {
		t.Fatalf("a byte-exact rollback: %v", err)
	}
}
