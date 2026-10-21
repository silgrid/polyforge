package core

// upgrade_v250_test.go — the polyforge#157 window SQL (deploy/sql/v2.5.0):
// wi_repo_inventory is dropped and work_items.repo_inventory added. The
// fixture is a real v2.4.x database: the pre-#149 DDL with the v2.4.0
// upgrade applied (the shape production runs). Pinned: the probe's
// run-once answers, the current code on the upgraded file (inventory
// create + claim), the rollback restoring the v2.4.x shape the old code
// writes to, and a fresh database already being in the applied shape.

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// v24SchemaDB answers a populated v2.4.x database (data dir + raw handle).
func v24SchemaDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dataDir, raw := oldSchemaDB(t, true)
	applySQLText(t, raw, loadSQLFile(t, "upgrade.sql"))
	return dataDir, raw
}

// probe250 executes v2.5.0's probe.sql final statement.
func probe250(t *testing.T, db *sql.DB) int {
	t.Helper()
	stmts := upgradeStatements(loadSQLFileOf(t, sqlVer250, "probe.sql"))
	var v int
	if err := db.QueryRow(stmts[len(stmts)-1]).Scan(&v); err != nil {
		t.Fatalf("probe statement failed: %v", err)
	}
	return v
}

// assertV250Applied / assertV24Shape check the two structural states.
func assertV250Applied(t *testing.T, db *sql.DB) {
	t.Helper()
	if rawTableExists(t, db, "wi_repo_inventory") {
		t.Error("wi_repo_inventory still present after the v2.5.0 upgrade")
	}
	if !rawColumnExists(t, db, "work_items", "repo_inventory") {
		t.Error("work_items.repo_inventory missing after the v2.5.0 upgrade")
	}
}

func assertV24Shape(t *testing.T, db *sql.DB) {
	t.Helper()
	if !rawTableExists(t, db, "wi_repo_inventory") {
		t.Error("wi_repo_inventory missing in the v2.4.x shape")
	}
	if rawColumnExists(t, db, "work_items", "repo_inventory") {
		t.Error("work_items.repo_inventory present in the v2.4.x shape")
	}
}

// TestUpgradeV250ProbeRunOnce: apply on the v2.4.x shape, skip once
// applied, the upgrade itself is not re-runnable (the probe is the
// guard), apply again after the rollback.
func TestUpgradeV250ProbeRunOnce(t *testing.T) {
	_, raw := v24SchemaDB(t)
	assertV24Shape(t, raw)
	if v := probe250(t, raw); v == 0 {
		t.Fatal("probe answers 0 (applied) on the v2.4.x shape")
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer250, "upgrade.sql"))
	assertV250Applied(t, raw)
	if v := probe250(t, raw); v != 0 {
		t.Fatalf("probe answers %d after the upgrade, want 0", v)
	}
	failed := false
	for _, stmt := range upgradeStatements(loadSQLFileOf(t, sqlVer250, "upgrade.sql")) {
		if _, err := raw.Exec(stmt); err != nil {
			failed = true
			break
		}
	}
	if !failed {
		t.Fatal("re-running the v2.5.0 upgrade must fail (duplicate column) - the probe is the run-once guard")
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer250, "rollback.sql"))
	assertV24Shape(t, raw)
	if v := probe250(t, raw); v == 0 {
		t.Fatal("probe answers 0 after the rollback - a retry upgrade must be allowed")
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer250, "upgrade.sql"))
	assertV250Applied(t, raw)
}

// TestUpgradeV250RoundTrip: v2.4.x data -> upgrade -> the current code
// files a WI with an inventory and its claim serves only that repo ->
// rollback -> the v2.4.x code's inventory write works again (unique
// index included).
func TestUpgradeV250RoundTrip(t *testing.T) {
	dataDir, raw := v24SchemaDB(t)
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer250, "upgrade.sql"))
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	env := newEnvAt(t, dataDir)
	reposProject(t, env)
	req := invCreate(env, names("app"))
	req.Project = "bravo"
	wi, err := env.WIs.Create(req)
	if err != nil {
		t.Fatalf("post-upgrade create with inventory: %v", err)
	}
	res, err := NewSessionManager(env.DB).Claim(wi.ID, api.SessionClaimRequest{SessionID: strings.Repeat("8", 32)}, env.UserID)
	if err != nil {
		t.Fatalf("post-upgrade claim: %v", err)
	}
	if len(res.Repos) != 1 || res.Repos[0].Name != "app" {
		t.Fatalf("post-upgrade claim repos = %+v, want only app", res.Repos)
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
	applySQLText(t, db, loadSQLFileOf(t, sqlVer250, "rollback.sql"))
	assertV24Shape(t, db)
	// the v2.4.x create transaction's inventory insert
	ins := `INSERT INTO wi_repo_inventory(work_item_id, position, name, url, subdir, base) VALUES (?1,?2,?3,?4,NULL,?5)`
	if _, err := db.Exec(ins, oldWIID(2), 1, "app", "file:///srv/app.git", "abc"); err != nil {
		t.Fatalf("old-version inventory write after the rollback: %v", err)
	}
	if _, err := db.Exec(ins, oldWIID(2), 2, "app", "file:///srv/app.git", "abc"); err == nil {
		t.Fatal("duplicate inventory name accepted: the rollback did not restore idx_inventory_name")
	}
}

// TestFreshDBIsV250Shape: a database the current Open creates is already
// in the applied shape — the probe skips it.
func TestFreshDBIsV250Shape(t *testing.T) {
	env := newEnv(t)
	assertV250Applied(t, env.DB.DB)
	if v := probe250(t, env.DB.DB); v != 0 {
		t.Fatalf("probe answers %d on a fresh database, want 0", v)
	}
}
