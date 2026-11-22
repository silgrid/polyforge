package core

// carries: deploy/sql/v0.2.4/upgrade.sql, deploy/sql/v0.2.4/probe.sql, deploy/sql/v0.2.4/rollback.sql, internal/aihub/core/workitem.go, internal/aihub/core/upgrade_test.go, internal/aihub/core/upgrade_v024_test.go

// upgrade_v024_test.go — the deploy/sql/v0.2.4 window SQL (the split
// attrs migration, polyforge#174). The fixture is a real CURRENT-shape
// database with work items created through the live write path, then
// aged to the pre-fix flat attrs shapes by raw UPDATE — the create
// boundary now refuses a flat dotted key (u2), so the old rows are
// exactly what a raw write can still represent. Pinned: the probe's
// run-once answers (nonzero on the old shape, 0 after the upgrade,
// nonzero again after the rollback), that the upgrade moves only the
// two split keys of every project and status (the other attrs, the
// other dotted keys, the statuses and the ids stay), that the same
// dot-path list query the split check runs finds the ORIGINAL ids
// again (AC2), that a re-run is a no-op, that the rollback restores the
// pre-migration bytes of exactly the changed rows and drops the
// before-image table (a rollback→upgrade round trip works), and that a
// conflicting database (a same-key different value, or a non-object
// `split` parent) refuses the WHOLE batch — one transaction, nothing
// applied, the probe still nonzero.

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

// w174SQLVer is the release directory carrying polyforge#174's window
// SQL. sqlVers does not know it yet (u5 registers it), so the fixture
// reads the directory directly — the same discipline upgrade_v023_test
// keeps its fixture below its own release.
const w174SQLVer = "v0.2.4"

// w174BackupTable is the before-image table the upgrade writes and the
// rollback reads (its name is the upgrade.sql contract).
const w174BackupTable = "split_attrs_before_w174"

// w174ApplyTx applies one SQL script as ONE transaction — deploy's
// runner semantics — and answers the statement error (everything
// rolled back on one).
func w174ApplyTx(t *testing.T, db *sql.DB, sqlText string) error {
	t.Helper()
	stmts := upgradeStatements(sqlText)
	if len(stmts) == 0 {
		t.Fatalf("sql text carries no statements")
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// w174ProbeValue executes probe.sql's final statement and answers its
// scalar (deploy's contract: 0 = already applied, nonzero = apply).
func w174ProbeValue(t *testing.T, db *sql.DB) int {
	t.Helper()
	stmts := upgradeStatements(loadSQLFileOf(t, w174SQLVer, "probe.sql"))
	if len(stmts) == 0 {
		t.Fatal("probe.sql carries no statements")
	}
	var v int
	if err := db.QueryRow(stmts[len(stmts)-1]).Scan(&v); err != nil {
		t.Fatalf("probe statement failed: %v\n%s", err, stmts[len(stmts)-1])
	}
	return v
}

// w174Fixture answers a current-shape env plus a raw handle on its
// file, and files one work item whose attrs are then aged to oldJSON.
func w174Fixture(t *testing.T) (*testEnv, *sql.DB, string) {
	t.Helper()
	dataDir := t.TempDir()
	env := newEnvAt(t, dataDir)
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "aihub.db"))
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	return env, raw, dataDir
}

// w174Age files one work item through the live path and ages its attrs
// to oldJSON by raw UPDATE (the pre-fix writer's shape), answering id.
func w174Age(t *testing.T, env *testEnv, raw *sql.DB, goal, oldJSON string) string {
	t.Helper()
	id := mustCreateExecWI(t, env, goal, nil, "")
	if _, err := raw.Exec(`UPDATE work_items SET attrs=?1 WHERE id=?2`, oldJSON, id); err != nil {
		t.Fatalf("age %s: %v", goal, err)
	}
	return id
}

// w174Attrs answers one row's attrs text.
func w174Attrs(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRow(`SELECT attrs FROM work_items WHERE id=?1`, id).Scan(&s); err != nil {
		t.Fatalf("read %s attrs: %v", id, err)
	}
	return s.String
}

// w174JSON parses one row's attrs as a generic object.
func w174JSON(t *testing.T, db *sql.DB, id string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(w174Attrs(t, db, id)), &out); err != nil {
		t.Fatalf("%s attrs is not JSON: %v (%s)", id, err, w174Attrs(t, db, id))
	}
	return out
}

// w174FlatCount answers how many rows still carry a flat split key.
func w174FlatCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM work_items
		WHERE json_type(attrs, '$."split.origin"') IS NOT NULL
		   OR json_type(attrs, '$."split.unit"') IS NOT NULL`).Scan(&n); err != nil {
		t.Fatalf("flat count: %v", err)
	}
	return n
}

// w174TableExists answers whether table exists.
func w174TableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	return rawTableExists(t, db, table)
}

// TestW174SplitAttrsMigration — AC2: the one-time move of the old flat
// split keys to the nested dot paths, on the whole database, with the
// original ids listable by the same query the split check runs.
func TestW174SplitAttrsMigration(t *testing.T) {
	env, raw, _ := w174Fixture(t)

	// the aged rows: the flat pair with other attrs, a nested unit
	// beside a flat origin, the same-value double shape, another dotted
	// key that must NOT move, and a wrapped row (the migration covers
	// every status).
	w1 := w174Age(t, env, raw, "flat pair", `{"split.origin":"O1","split.unit":"ui","team":"x"}`)
	w2 := w174Age(t, env, raw, "nested unit flat origin", `{"split":{"unit":"rt"},"split.origin":"O2"}`)
	w3 := w174Age(t, env, raw, "same-value double shape", `{"split":{"origin":"O3"},"split.origin":"O3","note":"same"}`)
	w4 := w174Age(t, env, raw, "other dotted key", `{"deploy.env":"prod","split.unit":"cli","seq":7}`)
	if _, err := raw.Exec(`UPDATE work_items SET status='wrapped' WHERE id=?1`, w3); err != nil {
		t.Fatalf("wrap %s: %v", w3, err)
	}
	preW1, preW2, preW3, preW4 := w174Attrs(t, raw, w1), w174Attrs(t, raw, w2), w174Attrs(t, raw, w3), w174Attrs(t, raw, w4)

	// the probe: the old shape answers apply.
	if v := w174ProbeValue(t, raw); v != 4 {
		t.Fatalf("probe before the upgrade = %d, want 4 (four rows carry a flat split key)", v)
	}

	// the upgrade: one transaction, nothing refused.
	if err := w174ApplyTx(t, raw, loadSQLFileOf(t, w174SQLVer, "upgrade.sql")); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}

	// the moved shapes — every other attr, the other dotted key and the
	// status stay, the flat keys are gone.
	for id, want := range map[string]map[string]any{
		w1: {"split": map[string]any{"origin": "O1", "unit": "ui"}, "team": "x"},
		w2: {"split": map[string]any{"unit": "rt", "origin": "O2"}},
		w3: {"split": map[string]any{"origin": "O3"}, "note": "same"},
		w4: {"deploy.env": "prod", "split": map[string]any{"unit": "cli"}, "seq": float64(7)},
	} {
		if got := w174JSON(t, raw, id); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s attrs after the upgrade = %v, want %v", id, got, want)
		}
	}
	if w174FlatCount(t, raw) != 0 {
		t.Fatal("a row still carries a flat split key after the upgrade")
	}
	var w3Status string
	if err := raw.QueryRow(`SELECT status FROM work_items WHERE id=?1`, w3).Scan(&w3Status); err != nil || w3Status != "wrapped" {
		t.Fatalf("the migration must not touch statuses: %s is %q (%v)", w3, w3Status, err)
	}

	// the probe answers applied, and the before-image table holds
	// exactly the changed rows.
	if v := w174ProbeValue(t, raw); v != 0 {
		t.Fatalf("probe after the upgrade = %d, want 0", v)
	}
	if !w174TableExists(t, raw, w174BackupTable) {
		t.Fatalf("the %s before-image table is missing after the upgrade — the rollback reads it", w174BackupTable)
	}

	// AC2's own proof: the same dot-path query the split check runs
	// finds the ORIGINAL ids again — through the live manager.
	ms, err := ParseAttrFilters([]string{"split.origin:O1", "split.unit:ui"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := env.WIs.List(ListFilters{Project: "alpha", Attrs: ms})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != w1 {
		var got []string
		for _, it := range items {
			got = append(got, it.ID)
		}
		t.Fatalf("list by the split dot paths after the migration = %v, want exactly [%s] — the original id, back on the filter (polyforge#174 AC2)", got, w1)
	}

	// a re-run is a no-op: the probe already answered applied, and even
	// a direct re-apply changes nothing.
	before := w174Attrs(t, raw, w1)
	if err := w174ApplyTx(t, raw, loadSQLFileOf(t, w174SQLVer, "upgrade.sql")); err != nil {
		t.Fatalf("re-run of the upgrade failed (it must be idempotent): %v", err)
	}
	if got := w174Attrs(t, raw, w1); got != before || w174FlatCount(t, raw) != 0 {
		t.Fatalf("the re-run changed the row: %q -> %q", before, got)
	}

	// the rollback restores the pre-migration bytes of exactly the
	// changed rows and drops the before-image table; the probe answers
	// apply again, and a rollback→upgrade round trip re-migrates.
	if err := w174ApplyTx(t, raw, loadSQLFileOf(t, w174SQLVer, "rollback.sql")); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	for id, pre := range map[string]string{w1: preW1, w2: preW2, w3: preW3, w4: preW4} {
		if got := w174Attrs(t, raw, id); got != pre {
			t.Fatalf("rollback of %s = %q, want the pre-migration bytes %q", id, got, pre)
		}
	}
	if w174TableExists(t, raw, w174BackupTable) {
		t.Fatalf("the %s before-image table survives the rollback — it is this window's record only", w174BackupTable)
	}
	if v := w174ProbeValue(t, raw); v != 4 {
		t.Fatalf("probe after the rollback = %d, want 4 (the old shape is back)", v)
	}
	if err := w174ApplyTx(t, raw, loadSQLFileOf(t, w174SQLVer, "upgrade.sql")); err != nil {
		t.Fatalf("upgrade after the rollback failed (the round trip must work): %v", err)
	}
	if w174FlatCount(t, raw) != 0 {
		t.Fatal("the round-trip upgrade left a flat split key")
	}

	// conflicting databases refuse the WHOLE batch: a same-key
	// different value, and a non-object `split` parent. One transaction
	// each — nothing applied, the before-image table absent, the probe
	// still apply.
	for name, conflict := range map[string]string{
		"a nested origin of another value": `{"split":{"origin":"other"},"split.origin":"O1","split.unit":"ui"}`,
		"a scalar split parent":            `{"split":"scalar","split.unit":"u"}`,
	} {
		t.Run(name, func(t *testing.T) {
			cenv, craw, _ := w174Fixture(t)
			id := w174Age(t, cenv, craw, "conflict "+name, conflict)
			pre := w174Attrs(t, craw, id)
			err := w174ApplyTx(t, craw, loadSQLFileOf(t, w174SQLVer, "upgrade.sql"))
			if err == nil {
				t.Fatal("the conflicting database was migrated — the whole batch must be refused")
			}
			if got := w174Attrs(t, craw, id); got != pre {
				t.Fatalf("the refused upgrade changed the row: %q -> %q", pre, got)
			}
			if w174TableExists(t, craw, w174BackupTable) {
				t.Fatal("the refused upgrade left the before-image table behind")
			}
			if v := w174ProbeValue(t, craw); v == 0 {
				t.Fatal("the probe answers applied on a database the upgrade refused")
			}
		})
	}
}
