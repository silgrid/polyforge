package core

// upgrade_v2140_test.go — the deploy/sql/v2.14.0 window SQL: the
// memories freshness columns (fs_mtime_ns / fs_size) and the
// claim_idempotency table are dropped. The fixture is a real v2.13.x
// database: the pre-#149 DDL with every earlier upgrade applied plus the
// memories.content column v2.13.0 adds, carrying a memory row and a
// cached claim. Pinned: the probe's run-once answers, the current code
// on the upgraded file, the rollback restoring the shape v2.13.0 writes
// to, and a fresh database already being in the applied shape.

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// v213SchemaDB answers a populated v2.13.x database (data dir + raw handle).
func v213SchemaDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dataDir, raw := oldSchemaDB(t, true)
	applySQLText(t, raw, loadSQLFile(t, "upgrade.sql"))
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer250, "upgrade.sql"))
	now := Now()
	for _, stmt := range []string{
		`ALTER TABLE memories ADD COLUMN content TEXT`,
		`INSERT INTO memories(id, project_id, owner_user_id, type, visibility, authority_path,
			content_sha, content_len, status, created_at, updated_at, content, fs_mtime_ns, fs_size)
			VALUES('mem_old','p-old','u-old','experience.debug','project','memory/p-old/mem_old',
			'sha',3,'active','` + now + `','` + now + `','doc',1,3)`,
		`INSERT INTO claim_idempotency(idempotency_key, work_item_id, caller_identity, response_json, created_at)
			VALUES('k-old','` + oldWIID(2) + `','u-old','{}','` + now + `')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("fixture: %v\n%s", err, stmt)
		}
	}
	return dataDir, raw
}

// probe2140 executes v2.14.0's probe.sql final statement.
func probe2140(t *testing.T, db *sql.DB) int {
	t.Helper()
	stmts := upgradeStatements(loadSQLFileOf(t, sqlVer2140, "probe.sql"))
	var v int
	if err := db.QueryRow(stmts[len(stmts)-1]).Scan(&v); err != nil {
		t.Fatalf("probe statement failed: %v", err)
	}
	return v
}

// assertV2140Applied / assertV213Shape check the two structural states.
func assertV2140Applied(t *testing.T, db *sql.DB) {
	t.Helper()
	if rawTableExists(t, db, "claim_idempotency") {
		t.Error("claim_idempotency still present after the v2.14.0 upgrade")
	}
	for _, c := range []string{"fs_mtime_ns", "fs_size"} {
		if rawColumnExists(t, db, "memories", c) {
			t.Errorf("memories.%s still present after the v2.14.0 upgrade", c)
		}
	}
}

func assertV213Shape(t *testing.T, db *sql.DB) {
	t.Helper()
	if !rawTableExists(t, db, "claim_idempotency") {
		t.Error("claim_idempotency missing in the v2.13.x shape")
	}
	for _, c := range []string{"fs_mtime_ns", "fs_size"} {
		if !rawColumnExists(t, db, "memories", c) {
			t.Errorf("memories.%s missing in the v2.13.x shape", c)
		}
	}
}

// TestUpgradeV2140ProbeRunOnce: apply on the v2.13.x shape, skip once
// applied, the upgrade itself is not re-runnable (the probe is the
// guard), apply again after the rollback.
func TestUpgradeV2140ProbeRunOnce(t *testing.T) {
	_, raw := v213SchemaDB(t)
	assertV213Shape(t, raw)
	if v := probe2140(t, raw); v == 0 {
		t.Fatal("probe answers 0 (applied) on the v2.13.x shape")
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2140, "upgrade.sql"))
	assertV2140Applied(t, raw)
	if v := probe2140(t, raw); v != 0 {
		t.Fatalf("probe answers %d after the upgrade, want 0", v)
	}
	failed := false
	for _, stmt := range upgradeStatements(loadSQLFileOf(t, sqlVer2140, "upgrade.sql")) {
		if _, err := raw.Exec(stmt); err != nil {
			failed = true
			break
		}
	}
	if !failed {
		t.Fatal("re-running the v2.14.0 upgrade must fail (missing column) - the probe is the run-once guard")
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2140, "rollback.sql"))
	assertV213Shape(t, raw)
	if v := probe2140(t, raw); v == 0 {
		t.Fatal("probe answers 0 after the rollback - a retry upgrade must be allowed")
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2140, "upgrade.sql"))
	assertV2140Applied(t, raw)
}

// TestUpgradeV2140RoundTrip: v2.13.x data -> upgrade -> the memory row
// keeps its document and the current code claims -> rollback -> the
// v2.13.x claim cache write and memory columns work again.
func TestUpgradeV2140RoundTrip(t *testing.T) {
	dataDir, raw := v213SchemaDB(t)
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2140, "upgrade.sql"))
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	env := newEnvAt(t, dataDir)
	var content string
	if err := env.DB.QueryRow(`SELECT content FROM memories WHERE id='mem_old'`).Scan(&content); err != nil || content != "doc" {
		t.Fatalf("memory row after the upgrade: %q (%v), want its document", content, err)
	}
	id := mustCreateExecWI(t, env, "v2.14.0 smoke ticket", nil, "")
	if _, err := NewSessionManager(env.DB).Claim(id, api.SessionClaimRequest{
		SessionID: strings.Repeat("9", 32), MachineID: "m1",
	}, env.UserID); err != nil {
		t.Fatalf("post-upgrade claim: %v", err)
	}
	if err := env.DB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "aihub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	applySQLText(t, db, loadSQLFileOf(t, sqlVer2140, "rollback.sql"))
	assertV213Shape(t, db)
	if _, err := db.Exec(`INSERT INTO claim_idempotency(idempotency_key, work_item_id, caller_identity, response_json, created_at)
		VALUES('k-rt',?1,'u-old','{}',?2)`, id, Now()); err != nil {
		t.Fatalf("v2.13.x claim cache write after the rollback: %v", err)
	}
	var fsSize sql.NullInt64
	if err := db.QueryRow(`SELECT fs_size FROM memories WHERE id='mem_old'`).Scan(&fsSize); err != nil || fsSize.Valid {
		t.Fatalf("memories.fs_size after the rollback: %+v (%v), want NULL", fsSize, err)
	}
}

// TestFreshDBIsV2140Shape: a database the current Open creates is already
// in the applied shape — the probe skips it.
func TestFreshDBIsV2140Shape(t *testing.T) {
	env := newEnv(t)
	assertV2140Applied(t, env.DB.DB)
	if v := probe2140(t, env.DB.DB); v != 0 {
		t.Fatalf("probe answers %d on a fresh database, want 0", v)
	}
}
