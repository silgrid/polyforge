package test

// deploy_expected_deltas_window_test.go — the declared-delta check is
// measured across the upgrade SQL itself, not against the step-3 baseline.
// A live write to a declared table between the online snapshot and the
// switch window is not migration work, so it must not turn a correct
// declaration into a mismatch.
//
// The write is injected by the fake docker's compose-stop hook,
// FAKE_BEFORE_STOP_SQL: it lands after the step-3 snapshot and before the
// upgrade SQL, exactly the gap the frozen baseline would otherwise fold
// into the migration's delta.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const deltaLiveWriteSQL = `import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute("INSERT INTO users(id, email) VALUES (99, 'late_human@example.com')")
c.commit()
`

func TestDeployDeclaredDeltaIsMeasuredAcrossTheUpgradeSQL(t *testing.T) {
	d := newDeltaWorld(t, deltaDeclaration)
	seedUsers(t, d, 2, 3) // 2 machine rows (deleted by the SQL) + 3 human
	d.env = append(d.env, "FAKE_BEFORE_STOP_SQL="+deltaLiveWriteSQL)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("a live write to a declared table broke a correct declaration: %v\n%s", err, out)
	}
	if strings.Contains(out, "ROLLED_BACK") {
		t.Fatalf("the deploy was rolled back\n%s", out)
	}
	// the migration still removed exactly the 2 machine rows; the live human
	// row survives (the SQL deletes the machine pattern only)
	if m, h := usersShapes(t, d.dbPath()); m != 0 || h != 4 {
		t.Errorf("users after the deploy: machines=%d humans=%d, want 0/4\n%s", m, h, out)
	}
	if !strings.Contains(out, "table users: declared delta -2 matched") {
		t.Errorf("the declared delta was not checked in the window\n%s", out)
	}
	if n := sqlRuns(d, "v2.3.0-rollback.sql"); n != 0 {
		t.Errorf("rollback SQL ran on the declared-delta path (%d)\n%s", n, d.logOf("log-ssh"))
	}
}

const deltaAfterSwapLossSQL = `import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute("DELETE FROM users WHERE id = (SELECT id FROM users WHERE email NOT LIKE '%@machines.local' ORDER BY id LIMIT 1)")
c.commit()
`

// TestDeployDeclaredTableStaysGuardedAfterTheSwap: the declared table's
// baseline is rebased to its post-SQL count rather than removed, so a row
// lost after the exact window check (here: the new binary's start) still
// trips the post-swap >= check and rolls the deploy back. The exact check
// itself passes (the SQL moved users by the declared -2); the loss comes
// afterwards, from FAKE_AFTER_UP_SQL.
func TestDeployDeclaredTableStaysGuardedAfterTheSwap(t *testing.T) {
	d := newDeltaWorld(t, deltaDeclaration)
	seedUsers(t, d, 2, 3) // 2 machine rows (deleted by the SQL) + 3 human
	d.env = append(d.env, "FAKE_AFTER_UP_SQL="+deltaAfterSwapLossSQL)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 10 {
		t.Fatalf("a loss on a declared table after the SQL must roll back (exit 10), got %v\n%s", err, out)
	}
	for _, want := range []string{
		"table users: declared delta -2 matched",
		"table users LOST rows: 2 < 3",
		"row counts after the swap are BELOW the baseline",
		"AUTOMATIC ROLLBACK",
		"deployed=ROLLED_BACK previous=v2.2.1 attempted=v2.3.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	if n := sqlRuns(d, "v2.3.0-rollback.sql"); n != 1 {
		t.Errorf("rollback SQL must run exactly once (%d)\n%s", n, d.logOf("log-ssh"))
	}
}

// newMultiDeltaWorld: two pending versions, each deleting one machine user
// and declaring users: -1, so the applied declarations must SUM to -2.
func newMultiDeltaWorld(t *testing.T) *deployWorld {
	t.Helper()
	d := newDeployWorld(t, "v2.2.1")
	for i, v := range []string{"v2.3.0", "v2.4.0"} {
		n := i + 1
		dir := filepath.Join(d.repo, "deploy", "sql", v)
		wf(t, filepath.Join(dir, "upgrade.sql"), fmt.Sprintf(
			"CREATE TABLE marker_%d(id integer);\nDELETE FROM users WHERE email = 'mac_%d@machines.local';\n", n, n), 0o644)
		wf(t, filepath.Join(dir, "rollback.sql"), fmt.Sprintf(
			"DROP TABLE IF EXISTS marker_%d;\nINSERT INTO users(id, email) VALUES (%d, 'mac_%d@machines.local');\n", n, n, n), 0o644)
		wf(t, filepath.Join(dir, "probe.sql"), fmt.Sprintf(
			"SELECT CASE WHEN EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='marker_%d') THEN 0 ELSE 1 END;\n", n), 0o644)
		wf(t, filepath.Join(dir, "expected_deltas"), "users: -1\n", 0o644)
	}
	d.git("add", "-A")
	d.git("commit", "-q", "-m", "multi-sql")
	d.release("v2.2.1", true, digOld, digOld)
	d.release("v2.3.0", true, digNew, digNew)
	d.release("v2.4.0", true, digNew, digNew)
	if err := os.RemoveAll(filepath.Join(d.repo, "deploy", "sql")); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestDeploySumsDeclarationsOfEveryAppliedVersion: a gap deploy applies
// several versions; their declarations combine per table, so -1 + -1 must
// be checked as -2 (not last-wins and not each against the other).
func TestDeploySumsDeclarationsOfEveryAppliedVersion(t *testing.T) {
	d := newMultiDeltaWorld(t)
	seedUsers(t, d, 2, 3)
	out, err := d.run("", "v2.4.0", "--confirm", "v2.4.0")
	if err != nil {
		t.Fatalf("two summing declarations were not accepted: %v\n%s", err, out)
	}
	for _, want := range []string{
		"table users: declared delta -2 matched",
		"deployed=v2.4.0 previous=v2.2.1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	if m, h := usersShapes(t, d.dbPath()); m != 0 || h != 3 {
		t.Errorf("users after the deploy: machines=%d humans=%d, want 0/3\n%s", m, h, out)
	}
	if n := sqlRuns(d, "v2.3.0-rollback.sql") + sqlRuns(d, "v2.4.0-rollback.sql"); n != 0 {
		t.Errorf("rollback SQL ran on the summed-delta path (%d)\n%s", n, d.logOf("log-ssh"))
	}
}

// TestDeployRefusesMalformedExpectedDeltas: the declaration is validated in
// step 0, before any server mutation.
func TestDeployRefusesMalformedExpectedDeltas(t *testing.T) {
	d := newDeltaWorld(t, "users -2\n") // missing the ':'
	seedUsers(t, d, 2, 3)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 1 {
		t.Fatalf("a malformed declaration must be refused before the window (exit 1), got %v\n%s", err, out)
	}
	if !strings.Contains(out, "expected_deltas is malformed") {
		t.Errorf("output lacks the malformed-declaration refusal\n%s", out)
	}
	for _, bad := range []string{"AUTOMATIC ROLLBACK", "deployed=ROLLED_BACK"} {
		if strings.Contains(out, bad) {
			t.Errorf("the window ran despite the refusal (%q)\n%s", bad, out)
		}
	}
	if n := sqlRuns(d, "v2.3.0-upgrade.sql"); n != 0 {
		t.Errorf("upgrade SQL ran despite the refusal (%d)\n%s", n, d.logOf("log-ssh"))
	}
}

// TestDeployRefusesDeclaredTableAbsentBeforeTheSQL: a declaration naming a
// table the database does not have before the SQL is a refusal, not a
// silent no-op.
func TestDeployRefusesDeclaredTableAbsentBeforeTheSQL(t *testing.T) {
	d := newDeltaWorld(t, "ghost: -1\n")
	seedUsers(t, d, 2, 3)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 10 {
		t.Fatalf("a declared-but-absent table must roll back (exit 10), got %v\n%s", err, out)
	}
	for _, want := range []string{
		"table ghost is declared but absent before the upgrade SQL",
		"AUTOMATIC ROLLBACK",
		"deployed=ROLLED_BACK previous=v2.2.1 attempted=v2.3.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	if n := sqlRuns(d, "v2.3.0-rollback.sql"); n != 1 {
		t.Errorf("rollback SQL must run exactly once (%d)\n%s", n, d.logOf("log-ssh"))
	}
}

// TestDeployDeclaredTablesReachTheServerByName: the in-window sample must hand
// the remote python the declared table NAMES, not a file path. A real
// $AIHUB_HOST cannot read the deployer's mktemp work dir; the fake ssh now
// refuses any argument under pf-deploy.*, so the tree that passed
// $WORK/delta.tables fails here (the count dies after SWAPPED=1 and the deploy
// auto-rolls back) while argv names work.
func TestDeployDeclaredTablesReachTheServerByName(t *testing.T) {
	d := newDeltaWorld(t, deltaDeclaration)
	seedUsers(t, d, 2, 3) // 2 machine rows (deleted by the SQL) + 3 human
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("the in-window declared-table sample did not run against the server: %v\n%s", err, out)
	}
	if !strings.Contains(out, "table users: declared delta -2 matched") {
		t.Errorf("the declared delta was not checked in the window\n%s", out)
	}
	sawByName := false
	for _, ln := range strings.Split(d.logOf("log-ssh"), "\n") {
		if !strings.HasPrefix(ln, "ssh ") {
			continue // scp legitimately takes deployer-local sources
		}
		if strings.Contains(ln, "/pf-deploy.") {
			t.Errorf("a remote command was handed a deployer-local work path: %s", ln)
		}
		if strings.Contains(ln, "python3 - ") && strings.HasSuffix(ln, " users") {
			sawByName = true
		}
	}
	if !sawByName {
		t.Errorf("no remote count by table name was logged\n%s", d.logOf("log-ssh"))
	}
}
