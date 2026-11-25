package core

// carries: deploy/sql/v0.2.4/upgrade.sql, deploy/sql/v0.2.4/probe.sql, deploy/sql/v0.2.4/rollback.sql, internal/aihub/core/upgrade_v024_lock_test.go, docs/upgrade.md

// upgrade_v024_lock_test.go: the v0.2.4 window's second half — the
// generic lease base's lock schema (polyforge#245 AC4). The window SQL
// must cover five shapes: a new database (already applied), a v0.2.3
// database (flat split keys, no lock tables), a database that already
// ran the OLD v0.2.4 (split migrated, no lock tables — the probe must
// answer apply, the case the old probe missed), a repeated run
// (idempotent), and the rollback (lock tables gone, split restored, a
// retry applies again). The probe is the run-once guard, so every shape
// is judged by its FINAL statement's scalar.

import (
	"database/sql"
	"reflect"
	"testing"
)

// w247LeaseTables are the window's lock tables (the probe counts them).
var w247LeaseTables = []string{"leases", "lease_waiters", "repo_aliases"}

// w247DropLeaseTables removes the lock tables: the pre-window shape a
// database written before this change carries.
func w247DropLeaseTables(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range w247LeaseTables {
		if _, err := db.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
}

// w247RequireTables asserts every lock table exists.
func w247RequireTables(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range w247LeaseTables {
		if !w174TableExists(t, db, table) {
			t.Fatalf("the window created no %s table", table)
		}
	}
}

func TestLeaseMigrationV024(t *testing.T) {
	t.Run("a new database is already applied", func(t *testing.T) {
		_, raw, _ := w174Fixture(t)
		if v := w174ProbeValue(t, raw); v != 0 {
			t.Fatalf("probe on a new database = %d, want 0 (skip)", v)
		}
		w247RequireTables(t, raw)
	})

	t.Run("a v0.2.3 database applies the split and the lock schema together", func(t *testing.T) {
		env, raw, _ := w174Fixture(t)
		id := w174Age(t, env, raw, "old split", `{"split.origin":"O1","split.unit":"ui","team":"x"}`)
		w247DropLeaseTables(t, raw)
		// The probe counts both halves: one row still carries a flat split
		// key and the three lock tables are missing.
		if v := w174ProbeValue(t, raw); v != 4 {
			t.Fatalf("probe on the v0.2.3 shape = %d, want 4 (1 flat row + 3 missing tables)", v)
		}
		if err := w174ApplyTx(t, raw, loadSQLFileOf(t, w174SQLVer, "upgrade.sql")); err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		if v := w174ProbeValue(t, raw); v != 0 {
			t.Fatalf("probe after the upgrade = %d, want 0", v)
		}
		w247RequireTables(t, raw)
		if w174FlatCount(t, raw) != 0 {
			t.Fatal("the upgrade left a flat split key")
		}
		want := map[string]any{"split": map[string]any{"origin": "O1", "unit": "ui"}, "team": "x"}
		if got := w174JSON(t, raw, id); !reflect.DeepEqual(got, want) {
			t.Fatalf("split migration = %v, want %v", got, want)
		}
		// The lock schema is one holder per (kind, key): the primary key
		// refuses a second row.
		insert := `INSERT INTO leases(kind,key,request_id,machine_id,work_item,operator_id,token_hash,acquired_at_ns,expires_at_ns)
			VALUES ('landing',?1,?2,'m','alpha#1','u','h',0,1)`
		if _, err := raw.Exec(insert, "k", "r1"); err != nil {
			t.Fatalf("first holder insert: %v", err)
		}
		if _, err := raw.Exec(insert, "k", "r2"); err == nil {
			t.Fatal("a second holder row for one (kind, key) was accepted")
		}
	})

	t.Run("a database that already ran the old v0.2.4 applies only the lock schema", func(t *testing.T) {
		env, raw, _ := w174Fixture(t)
		id := w174Age(t, env, raw, "split already migrated", `{"split.origin":"O2","split.unit":"ui"}`)
		if err := w174ApplyTx(t, raw, loadSQLFileOf(t, w174SQLVer, "upgrade.sql")); err != nil {
			t.Fatalf("old v0.2.4 upgrade: %v", err)
		}
		// The old v0.2.4 ran the split migration and never created the lock
		// schema: the state the old probe answered 0 on.
		w247DropLeaseTables(t, raw)
		pre := w174Attrs(t, raw, id)
		if v := w174ProbeValue(t, raw); v != 3 {
			t.Fatalf("probe on the old-v0.2.4 shape = %d, want 3 (the three missing tables) — the old probe answered 0 here", v)
		}
		if err := w174ApplyTx(t, raw, loadSQLFileOf(t, w174SQLVer, "upgrade.sql")); err != nil {
			t.Fatalf("upgrade of the old v0.2.4 database: %v", err)
		}
		if v := w174ProbeValue(t, raw); v != 0 {
			t.Fatalf("probe after the lock schema = %d, want 0", v)
		}
		w247RequireTables(t, raw)
		if got := w174Attrs(t, raw, id); got != pre {
			t.Fatalf("the lock schema touched attrs: %q -> %q", pre, got)
		}
	})

	t.Run("repeating the upgrade is a no-op", func(t *testing.T) {
		_, raw, _ := w174Fixture(t)
		w247DropLeaseTables(t, raw)
		for i := 0; i < 2; i++ {
			if err := w174ApplyTx(t, raw, loadSQLFileOf(t, w174SQLVer, "upgrade.sql")); err != nil {
				t.Fatalf("upgrade run %d: %v", i+1, err)
			}
		}
		if v := w174ProbeValue(t, raw); v != 0 {
			t.Fatalf("probe after the repeated upgrade = %d, want 0", v)
		}
		var n int
		if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master
			WHERE type='table' AND name IN ('leases','lease_waiters','repo_aliases')`).Scan(&n); err != nil {
			t.Fatalf("count lock tables: %v", err)
		}
		if n != 3 {
			t.Fatalf("lock table count = %d, want 3 (each table once)", n)
		}
	})

	t.Run("the rollback drops the lock tables and restores the split", func(t *testing.T) {
		env, raw, _ := w174Fixture(t)
		id := w174Age(t, env, raw, "rollback", `{"split.origin":"O3","split.unit":"ui"}`)
		w247DropLeaseTables(t, raw)
		pre := w174Attrs(t, raw, id)
		if err := w174ApplyTx(t, raw, loadSQLFileOf(t, w174SQLVer, "upgrade.sql")); err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		if err := w174ApplyTx(t, raw, loadSQLFileOf(t, w174SQLVer, "rollback.sql")); err != nil {
			t.Fatalf("rollback: %v", err)
		}
		for _, table := range w247LeaseTables {
			if w174TableExists(t, raw, table) {
				t.Fatalf("the rollback left the %s table", table)
			}
		}
		if got := w174Attrs(t, raw, id); got != pre {
			t.Fatalf("rollback attrs = %q, want the pre-migration bytes %q", got, pre)
		}
		if v := w174ProbeValue(t, raw); v != 4 {
			t.Fatalf("probe after the rollback = %d, want 4 (the pre-window shape is back)", v)
		}
		// A retry applies the whole window again.
		if err := w174ApplyTx(t, raw, loadSQLFileOf(t, w174SQLVer, "upgrade.sql")); err != nil {
			t.Fatalf("upgrade after the rollback: %v", err)
		}
		if v := w174ProbeValue(t, raw); v != 0 {
			t.Fatalf("probe after the round trip = %d, want 0", v)
		}
		w247RequireTables(t, raw)
	})
}
