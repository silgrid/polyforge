package test

// deploy_expected_deltas_test.go — the post-swap baseline guard of
// .polyforge/deploy.sh must accept a row-count DECREASE that the migrating
// version DECLARES (after the v2.20.8 heartbeat
// migration deletes the machine users it minted, the guard reads "rows lost"
// and auto-rolls-back a perfectly legitimate deploy, exit 10).
//
// The declaration this test pins: an OPTIONAL file deploy/sql/<ver>/expected_deltas
// next to upgrade/rollback/probe.sql, one "table: signed-delta" per line
// (blank lines and '#' comments ignored), e.g.
//
//	# the heartbeat migration deletes the machine users it minted
//	users: -2
//
// A declared table passes only when its ACTUAL delta equals the declared one
// (a delta is a checklist, not a free pass); every undeclared table keeps
// today's strict ">= frozen baseline" rule. The declaration is fetched from
// the target git tag like the three SQL files.
//
// The scenario is the real one at reduced size: the fixture db carries 2
// machine users (mac_*@machines.local) and 3 human users; the test
// release's upgrade.sql deletes exactly the machine rows, its rollback.sql
// puts them back.
//
// Tri-state (all three against the same fixture):
//
//	declared AND exact      -> deploy succeeds, the deletion stands
//	no declaration          -> exit 10, rolled back, rollback.sql ran
//	declared BUT mismatched -> exit 10, rolled back, rollback.sql ran
//
// The guard is exercised through the fake deploy world of deploy_image_test.go
// (ssh/docker/gh stubs, a real sqlite fixture, the real python3), the SQL
// through the git-tag sourcing path of deploy_sql_test.go's world builder.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The test instance of deploy/sql/v2.3.0/ (see the world builder below).
const (
	// the machine users are what the migration is allowed to remove
	deltaProbeSQL = `SELECT COUNT(*) FROM users WHERE email LIKE '%@machines.local';
`
	// deletes exactly the 2 machine rows, leaves the 3 human rows alone
	deltaUpgradeSQL = `DELETE FROM users WHERE email LIKE '%@machines.local';
`
	// restores the deleted rows (the old binary must see them again)
	deltaRollbackSQL = `INSERT INTO users(id, email) VALUES
  (1, 'mac_1@machines.local'),
  (2, 'mac_2@machines.local');
`
	// the declaration the migrating version ships
	deltaDeclaration = `# the heartbeat migration deletes the machine users it minted
users: -2
`
)

// newDeltaWorld: the deploy world whose deployer repo carries a tagged
// deploy/sql/v2.3.0/{upgrade,rollback,probe}.sql plus (when non-empty) the
// expected_deltas declaration, all committed BEFORE the tag and then removed
// from the working tree - the deploy sources them via `git show <tag>:...`.
func newDeltaWorld(t *testing.T, deltas string) *deployWorld {
	t.Helper()
	d := newDeployWorld(t, "v2.2.1")
	dir := filepath.Join(d.repo, "deploy", "sql", "v2.3.0")
	wf(t, filepath.Join(dir, "upgrade.sql"), deltaUpgradeSQL, 0o644)
	wf(t, filepath.Join(dir, "rollback.sql"), deltaRollbackSQL, 0o644)
	wf(t, filepath.Join(dir, "probe.sql"), deltaProbeSQL, 0o644)
	if deltas != "" {
		wf(t, filepath.Join(dir, "expected_deltas"), deltas, 0o644)
	}
	d.git("add", "-A")
	d.git("commit", "-q", "-m", "sql")
	d.release("v2.2.1", true, digOld, digOld)
	d.release("v2.3.0", true, digNew, digNew)
	if err := os.RemoveAll(filepath.Join(d.repo, "deploy", "sql")); err != nil {
		t.Fatal(err)
	}
	return d
}

// seedUsers fills the fixture's users table with machine+machine users: ids
// 1..machine are the machine ones (mac_<i>@machines.local), the human ones
// follow with team_<i>@example.com.
func seedUsers(t *testing.T, d *deployWorld, machine, human int) {
	t.Helper()
	py := `import sqlite3, sys
machine, human = int(sys.argv[2]), int(sys.argv[3])
c = sqlite3.connect(sys.argv[1])
c.execute("ALTER TABLE users ADD COLUMN email TEXT")
for i in range(1, machine + 1):
    c.execute("INSERT INTO users(id, email) VALUES (?, ?)", (i, "mac_%d@machines.local" % i))
for i in range(1, human + 1):
    c.execute("INSERT INTO users(id, email) VALUES (?, ?)", (machine + i, "team_%d@example.com" % i))
c.commit()`
	if out, err := exec.Command("python3", "-I", "-c", py, d.dbPath(), strconv.Itoa(machine), strconv.Itoa(human)).CombinedOutput(); err != nil {
		t.Fatalf("users fixture: %v\n%s", err, out)
	}
}

// usersShapes: (machine rows, human rows) currently in the fixture db.
func usersShapes(t *testing.T, dbPath string) (int, int) {
	t.Helper()
	py := `import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
m = c.execute("SELECT COUNT(*) FROM users WHERE email LIKE '%@machines.local'").fetchone()[0]
h = c.execute("SELECT COUNT(*) FROM users WHERE email NOT LIKE '%@machines.local'").fetchone()[0]
print(m, h)`
	out, err := exec.Command("python3", "-I", "-c", py, dbPath).CombinedOutput()
	if err != nil {
		t.Fatalf("users probe: %v\n%s", err, out)
	}
	f := strings.Fields(strings.TrimSpace(string(out)))
	if len(f) != 2 {
		t.Fatalf("users probe answered %q", out)
	}
	m, err := strconv.Atoi(f[0])
	if err != nil {
		t.Fatalf("users probe answered %q: %v", out, err)
	}
	h, err := strconv.Atoi(f[1])
	if err != nil {
		t.Fatalf("users probe answered %q: %v", out, err)
	}
	return m, h
}

// TestDeployAcceptsDeclaredRowDelta: the legitimate delete-row migration
// (2 machine users gone, declared as users: -2) must deploy, not roll back.
func TestDeployAcceptsDeclaredRowDelta(t *testing.T) {
	d := newDeltaWorld(t, deltaDeclaration)
	seedUsers(t, d, 2, 3)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("a declared, exact row delta was not accepted: %v\n%s", err, out)
	}
	if strings.Contains(out, "ROLLED_BACK") {
		t.Fatalf("the legitimate delete-row migration was rolled back\n%s", out)
	}
	for _, want := range []string{
		"upgrade SQL v2.3.0: not yet applied (probe: 2) - runs in the window",
		"deployed=v2.3.0 previous=v2.2.1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	// the migration really applied, and the guard let the decrease stand
	if m, h := usersShapes(t, d.dbPath()); m != 0 || h != 3 {
		t.Errorf("users after the deploy: machines=%d humans=%d, want 0/3\n%s", m, h, out)
	}
	// the guard did not call the rollback path
	if n := sqlRuns(d, "v2.3.0-rollback.sql"); n != 0 {
		t.Errorf("rollback SQL ran on the declared-delta path (%d)\n%s", n, d.logOf("log-ssh"))
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.3.0") {
		t.Errorf("compose not on the new image:\n%s", d.composeText())
	}
	if !d.lockFree() {
		t.Errorf("the lock is still held after the script exited")
	}
}

// TestDeployRefusesUndeclaredRowLoss: the same migration WITHOUT the
// declaration is still data loss to the guard - exit 10 and rolled back.
func TestDeployRefusesUndeclaredRowLoss(t *testing.T) {
	d := newDeltaWorld(t, "")
	seedUsers(t, d, 2, 3)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 10 {
		t.Fatalf("an undeclared row loss must roll back (exit 10), got %v\n%s", err, out)
	}
	for _, want := range []string{
		"row counts", // the guard is the reason it refused
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
	if m, h := usersShapes(t, d.dbPath()); m != 2 || h != 3 {
		t.Errorf("users after the rollback: machines=%d humans=%d, want 2/3\n%s", m, h, out)
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.2.1") {
		t.Errorf("compose not rolled back:\n%s", d.composeText())
	}
}

// TestDeployRefusesMismatchedDeclaredRowDelta: a declaration is a checklist,
// not an amnesty - declared -1 while the migration removes 2 rows is refused
// exactly like an undeclared loss.
func TestDeployRefusesMismatchedDeclaredRowDelta(t *testing.T) {
	d := newDeltaWorld(t, "users: -1\n")
	seedUsers(t, d, 2, 3)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 10 {
		t.Fatalf("a delta that does not match its declaration must roll back (exit 10), got %v\n%s", err, out)
	}
	for _, want := range []string{
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
	if m, h := usersShapes(t, d.dbPath()); m != 2 || h != 3 {
		t.Errorf("users after the rollback: machines=%d humans=%d, want 2/3\n%s", m, h, out)
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.2.1") {
		t.Errorf("compose not rolled back:\n%s", d.composeText())
	}
}
