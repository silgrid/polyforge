package test

// deploy_sql_test.go — the in-window upgrade SQL of .polyforge/deploy.sh
// (polyforge#154 / wi_xKcIlUlXhPSX): the switch-window step order (old
// container stop → transactional SQL → new container up → HTTP poll), the
// SQL-failure and post-swap-failure rollback paths (the latter must run the
// rollback SQL BEFORE the old image starts), the run-once probe skip, and
// the SQLite < 3.35 preflight refusal.
//
// HOW THE PATHS ARE SIMULATED LOCALLY (the written note the memo asks for):
//
//   - The fake environment is the shared deploy_image_test.go world: ssh,
//     docker, gh, scp, curl are stubs on PATH and the "server" is a local
//     temp tree. `rssh python3 - <db> <file>` therefore executes the REAL
//     local python3 (stdlib sqlite3) against the REAL sqlite fixture db —
//     the upgrade/rollback/probe SQL genuinely executes, transactionally,
//     on a real file, through the exact code path production uses.
//   - The "old schema" shape is simulated by ALTER TABLE ADD COLUMN
//     identifier_hash on the fixture's projects table (the column the
//     pre-#149 schema carried, the probe looks for, and the window's
//     upgrade.sql drops); the test rollback.sql re-adds it with a default,
//     exactly like the real v2.4.0 files.
//   - The deployer-side git repo is a throwaway repo (newDeployWorld; newSQLWorld adds the SQL): deploy.sh
//     is copied verbatim from the repo, the test SQL files are committed and
//     tagged v2.3.0 and then REMOVED from the working tree, so the
//     `git show <TAG>:deploy/sql/<TAG>/...` sourcing path is what runs — the
//     working-tree fallback is never hit.
//   - The server's python3 sqlite library version (the ≥ 3.35 gate) is
//     faked by FAKE_SQLITE_VERSION: the ssh stub intercepts exactly the
//     gate's one-line python program; every other python3 program runs for
//     real.
//   - Failure injection is the harness's own: FAKE_BAD_IMG (the new
//     container never becomes healthy → post-swap verification failure) and
//     a deliberately broken upgrade.sql (DROP COLUMN of a nonexistent
//     column → the transaction fails inside the window).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The test instance of deploy/sql/v2.3.0/ — one column stands in for the
// whole #149 dead-shape set (the real v2.4.0 files are pinned by the core
// suite in internal/aihub/core/upgrade_test.go).
const (
	testProbeSQL = `SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name = 'identifier_hash';
`
	testUpgradeSQL = `ALTER TABLE projects DROP COLUMN identifier_hash;
`
	testRollbackSQL = `ALTER TABLE projects ADD COLUMN identifier_hash TEXT NOT NULL DEFAULT '';
`
)

// sqlVer is one release's deploy/sql/<ver>/ directory in the test world.
type sqlVer struct{ ver, upgrade, rollback, probe string }

// newSQLWorld builds the deploy world whose deployer repo carries a tagged
// deploy/sql/v2.3.0 (removed from the working tree afterwards: git-tag
// sourcing). oldShape adds the pre-#149 column to the fixture db so the
// probe reports "not yet applied"; without it the probe skips.
func newSQLWorld(t *testing.T, upgradeSQL string, oldShape bool) *deployWorld {
	t.Helper()
	var cols []string
	if oldShape {
		cols = []string{"identifier_hash"}
	}
	return newSQLWorldVers(t, "v2.2.1", []string{"v2.3.0"},
		[]sqlVer{{"v2.3.0", upgradeSQL, testRollbackSQL, testProbeSQL}}, cols)
}

// newSQLWorldVers: the general form. The server runs oldVer; every version
// in releases (and oldVer) is a published release and a local git tag; the
// deploy/sql dirs of vers are committed BEFORE the tags and then removed
// from the working tree, so only `git show <tag>:deploy/sql/...` finds them.
// oldCols are added to the fixture's projects table (the old shapes).
func newSQLWorldVers(t *testing.T, oldVer string, releases []string, vers []sqlVer, oldCols []string) *deployWorld {
	t.Helper()
	d := newDeployWorld(t, oldVer)
	for _, v := range vers {
		dir := filepath.Join(d.repo, "deploy", "sql", v.ver)
		wf(t, filepath.Join(dir, "upgrade.sql"), v.upgrade, 0o644)
		wf(t, filepath.Join(dir, "rollback.sql"), v.rollback, 0o644)
		wf(t, filepath.Join(dir, "probe.sql"), v.probe, 0o644)
	}
	d.git("add", "-A")
	d.git("commit", "-q", "-m", "sql")
	d.release(oldVer, true, digOld, digOld)
	for _, r := range releases {
		d.release(r, true, digNew, digNew)
	}
	// the working tree no longer carries the SQL: only the tags do
	if err := os.RemoveAll(filepath.Join(d.repo, "deploy", "sql")); err != nil {
		t.Fatal(err)
	}
	for _, c := range oldCols {
		py := `import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute("ALTER TABLE projects ADD COLUMN %s TEXT" % sys.argv[2])
c.commit()`
		if out, err := exec.Command("python3", "-I", "-c", py, d.dbPath(), c).CombinedOutput(); err != nil {
			t.Fatalf("old-shape fixture: %v\n%s", err, out)
		}
	}
	return d
}

// dbPath is the fixture db the fake "server" serves.
func (d *deployWorld) dbPath() string {
	return filepath.Join(d.w, "srv", "data", "aihub", "aihub.db")
}

// dbHasColumn: does the fixture db still carry the simulated pre-#149
// column (i.e. is the schema in the old shape)?
func dbHasColumn(t *testing.T, dbPath string) bool {
	t.Helper()
	return dbHasCol(t, dbPath, "identifier_hash")
}

// dbHasCol: does the fixture's projects table carry column col?
func dbHasCol(t *testing.T, dbPath, col string) bool {
	t.Helper()
	py := `import sqlite3, sys
cols = [r[1] for r in sqlite3.connect(sys.argv[1]).execute("pragma table_info(projects)")]
print("yes" if sys.argv[2] in cols else "no")`
	out, err := exec.Command("python3", "-I", "-c", py, dbPath, col).CombinedOutput()
	if err != nil {
		t.Fatalf("column probe: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out)) == "yes"
}

// sqlRuns counts how often deploy.sh EXECUTED (not scp'd) one staged SQL
// file on the "server" — the ssh log's `python3 - <db> <file>` lines.
func sqlRuns(d *deployWorld, file string) int {
	n := 0
	for _, ln := range strings.Split(d.logOf("log-ssh"), "\n") {
		if strings.HasPrefix(ln, "ssh python3 - ") && strings.Contains(ln, file) {
			n++
		}
	}
	return n
}

// sqlRunIndex: position in the ssh log of the first EXECUTION (python3 -)
// of <file>; -1 when it never ran. The stop/up window snippets run through
// `sh -s --` and are logged there too, so one log carries the whole order.
func sqlRunIndex(sshLog, file string) int {
	for _, ln := range strings.Split(sshLog, "\n") {
		if strings.HasPrefix(ln, "ssh python3 - ") && strings.Contains(ln, file) {
			return strings.Index(sshLog, ln)
		}
	}
	return -1
}

func TestDeployWindowAppliesUpgradeSQLInOrder(t *testing.T) {
	d := newSQLWorld(t, testUpgradeSQL, true)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"server python3 sqlite3: 3.5", // >= 3.35 gate passed
		"upgrade SQL v2.3.0: not yet applied (probe: 1) - runs in the window",
		"upgrade SQL of v2.3.0 applied: SQL-OK",
		"downtime: ", "upgrade SQL:    applied v2.3.0",
		"deployed=v2.3.0 previous=v2.2.1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	if dbHasColumn(t, d.dbPath()) {
		t.Error("identifier_hash still present after the window's upgrade SQL")
	}
	if n := sqlRuns(d, "v2.3.0-rollback.sql"); n != 0 {
		t.Errorf("rollback SQL executed on the happy path (%d)\n%s", n, d.logOf("log-ssh"))
	}
	// the window step order (one timeline: the ssh log also records the
	// stop/up `sh -s --` snippets): probe (read-only, pre-window) -> old
	// container stop -> upgrade SQL -> new container up
	sshLog := d.logOf("log-ssh")
	iProbe := sqlRunIndex(sshLog, "v2.3.0-probe.sql")
	iStop := strings.Index(sshLog, " aihub-app aihub 15\n") // stop snippet: compose project service drain
	iSQL := sqlRunIndex(sshLog, "v2.3.0-upgrade.sql")
	iUp := strings.Index(sshLog, " aihub-app aihub 8080 5") // up snippet: compose project service port wait
	if !(iProbe >= 0 && iProbe < iStop && iStop < iSQL && iSQL < iUp) {
		t.Errorf("wrong window order probe=%d stop=%d sql=%d up=%d\n%s", iProbe, iStop, iSQL, iUp, sshLog)
	}
}

func TestDeploySQLFailureRollsBackWithoutRollbackSQL(t *testing.T) {
	// a broken upgrade.sql: DROP COLUMN of a column that does not exist.
	// The transaction fails -> NOTHING of this script applied -> the
	// rollback is exactly today's (compose back to the old image), no
	// rollback.sql runs, and the db is unchanged.
	d := newSQLWorld(t, "ALTER TABLE projects DROP COLUMN no_such_column;\n", true)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 10 {
		t.Fatalf("want exit 10, got %v\n%s", err, out)
	}
	for _, want := range []string{
		"upgrade SQL of v2.3.0 failed (its transaction rolled back",
		"AUTOMATIC ROLLBACK to v2.2.1",
		"schema: untouched by the rollback (no upgrade SQL was applied this run)",
		"deployed=ROLLED_BACK previous=v2.2.1 attempted=v2.3.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	if n := sqlRuns(d, "v2.3.0-rollback.sql"); n != 0 {
		t.Errorf("rollback SQL ran although nothing was applied (%d)\n%s", n, d.logOf("log-ssh"))
	}
	if !dbHasColumn(t, d.dbPath()) {
		t.Error("the db changed despite the failed transaction")
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.2.1") {
		t.Errorf("compose not rolled back:\n%s", d.composeText())
	}
	// the new container was never started: the only `up` is the rollback's
	if n := strings.Count(d.logOf("log-docker"), " up -d --no-deps aihub"); n != 1 {
		t.Errorf("want exactly 1 compose up (the rollback's), got %d\n%s", n, d.logOf("log-docker"))
	}
	if !d.lockFree() {
		t.Errorf("the lock is still held after the script exited")
	}
}

func TestDeployPostSwapFailureRunsRollbackSQLBeforeOldImage(t *testing.T) {
	d := newSQLWorld(t, testUpgradeSQL, true)
	d.env = append(d.env, "FAKE_BAD_IMG=polyforge/aihub:v2.3.0") // new image: verification fails after the swap
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 10 {
		t.Fatalf("want exit 10, got %v\n%s", err, out)
	}
	for _, want := range []string{
		"upgrade SQL of v2.3.0 applied: SQL-OK",
		"rollback SQL: applying deploy/sql/v2.3.0/rollback.sql (one transaction)",
		"schema: rolled back by the rollback.sql of v2.3.0",
		"deployed=ROLLED_BACK previous=v2.2.1 attempted=v2.3.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	// the schema is restored: the old shape (column back, with the default)
	if !dbHasColumn(t, d.dbPath()) {
		t.Error("identifier_hash missing after the rollback SQL - the old binary would not run")
	}
	// order (one timeline, the ssh log): upgrade SQL -> rollback SQL ->
	// compose back to the old image -> the old image's `up`. The old binary
	// must never see the new schema.
	sshLog := d.logOf("log-ssh")
	iSQL := sqlRunIndex(sshLog, "v2.3.0-upgrade.sql")
	iRb := sqlRunIndex(sshLog, "v2.3.0-rollback.sql")
	iSed := strings.Index(sshLog, "sed -i 's|image: polyforge/aihub:v2.3.0|image: polyforge/aihub:v2.2.1|'")
	iUpOld := strings.LastIndex(sshLog, " aihub-app aihub 8080 5")
	if !(iSQL >= 0 && iSQL < iRb && iRb < iSed && iSed < iUpOld) {
		t.Errorf("wrong rollback order sql=%d rollbacksql=%d sed=%d up=%d\n%s", iSQL, iRb, iSed, iUpOld, sshLog)
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.2.1") {
		t.Errorf("compose not rolled back:\n%s", d.composeText())
	}
}

func TestDeployRefusesOldSQLiteBeforeWindow(t *testing.T) {
	d := newSQLWorld(t, testUpgradeSQL, true)
	d.env = append(d.env, "FAKE_SQLITE_VERSION=3.31.1")
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 1 || !strings.Contains(out, "3.35") || !strings.Contains(out, "refusing BEFORE the window") {
		t.Fatalf("want exit 1 + the >= 3.35 refusal, got %v\n%s", err, out)
	}
	d.assertNothingMutated("v2.2.1")
	if m := d.serverDockerMutations(); len(m) != 0 {
		t.Errorf("server docker was changed: %v", m)
	}
	if strings.Contains(d.logOf("log-docker"), " stop -t ") {
		t.Errorf("the switch window was entered despite the refusal:\n%s", d.logOf("log-docker"))
	}
	if n := sqlRuns(d, "v2.3.0-upgrade.sql"); n != 0 {
		t.Errorf("upgrade SQL executed despite the refusal (%d)\n%s", n, d.logOf("log-ssh"))
	}
	if !dbHasColumn(t, d.dbPath()) {
		t.Error("the db changed despite the refusal")
	}
}

func TestDeployProbeSkipsAlreadyAppliedSQL(t *testing.T) {
	// no old-shape column: the probe answers 0 -> the SQL is skipped even
	// though the target version ships a deploy/sql dir.
	d := newSQLWorld(t, testUpgradeSQL, false)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "upgrade SQL v2.3.0: already applied (probe) - skipped") {
		t.Errorf("no probe skip note\n%s", out)
	}
	if n := sqlRuns(d, "v2.3.0-upgrade.sql"); n != 0 {
		t.Errorf("upgrade SQL executed although the probe reported applied (%d)\n%s", n, d.logOf("log-ssh"))
	}
	if dbHasColumn(t, d.dbPath()) {
		t.Error("the db changed although the SQL was skipped")
	}
	// a --force re-deploy of the SAME version is a no-op for the SQL too:
	// the candidates set is {v2.3.0} and the probe still says applied.
	out2, err2 := d.run("", "v2.3.0", "--confirm", "v2.3.0", "--force")
	if err2 != nil {
		t.Fatalf("forced re-deploy failed: %v\n%s", err2, out2)
	}
	if n := sqlRuns(d, "v2.3.0-upgrade.sql"); n != 0 {
		t.Errorf("the --force re-deploy re-ran the upgrade SQL (%d)\n%s", n, d.logOf("log-ssh"))
	}
	if dbHasColumn(t, d.dbPath()) {
		t.Error("the db changed during the --force re-deploy")
	}
}

func TestDeployDryRunStagesAndRunsNoSQL(t *testing.T) {
	d := newSQLWorld(t, testUpgradeSQL, true)
	out, err := d.run("", "v2.3.0", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "upgrade SQL candidates (dry-run - the probes run at deploy time): v2.3.0") {
		t.Errorf("no dry-run SQL candidates note\n%s", out)
	}
	// strictly read-only: no staging dir, no probe execution, no SQL
	if strings.Contains(d.logOf("log-ssh"), "aihub-sql-") {
		t.Errorf("the dry-run staged SQL files on the server:\n%s", d.logOf("log-ssh"))
	}
	if n := sqlRuns(d, "v2.3.0-probe.sql"); n != 0 {
		t.Errorf("the dry-run executed a probe (%d)\n%s", n, d.logOf("log-ssh"))
	}
	d.assertNothingMutated("v2.2.1")
}

// ---- the multi-version gap (two schema-changing releases) ----
//
// v2.9.0 drops col_a, v2.10.0 drops col_b; the server runs v2.8.0. The
// pair is chosen so that a LEXICAL sort ("v2.10.0" < "v2.9.0") would get the
// order wrong: the plan must sort numerically.

func colProbe(c string) string {
	return "SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name = '" + c + "';\n"
}

func newGapWorld(t *testing.T, oldCols ...string) *deployWorld {
	t.Helper()
	return newSQLWorldVers(t, "v2.8.0", []string{"v2.9.0", "v2.10.0"}, []sqlVer{
		{"v2.9.0", "ALTER TABLE projects DROP COLUMN col_a;\n", "ALTER TABLE projects ADD COLUMN col_a TEXT;\n", colProbe("col_a")},
		{"v2.10.0", "ALTER TABLE projects DROP COLUMN col_b;\n", "ALTER TABLE projects ADD COLUMN col_b TEXT;\n", colProbe("col_b")},
	}, oldCols)
}

// stagingLeft: server-side SQL staging dirs still present after the run.
func stagingLeft(d *deployWorld) []string {
	m, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(d.dbPath())), "aihub-sql-*"))
	return m
}

func TestDeploySQLGapAppliesAscending(t *testing.T) {
	d := newGapWorld(t, "col_a", "col_b")
	out, err := d.run("", "v2.10.0", "--confirm", "v2.10.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "upgrade SQL:    applied v2.9.0 v2.10.0") {
		t.Errorf("summary lacks the ascending applied list\n%s", out)
	}
	sshLog := d.logOf("log-ssh")
	i9, i10 := sqlRunIndex(sshLog, "v2.9.0-upgrade.sql"), sqlRunIndex(sshLog, "v2.10.0-upgrade.sql")
	if !(i9 >= 0 && i9 < i10) {
		t.Errorf("want v2.9.0 upgrade before v2.10.0 (got %d, %d)\n%s", i9, i10, sshLog)
	}
	if dbHasCol(t, d.dbPath(), "col_a") || dbHasCol(t, d.dbPath(), "col_b") {
		t.Error("a gap version's upgrade SQL did not apply")
	}
	if left := stagingLeft(d); len(left) != 0 {
		t.Errorf("SQL staging dir left on the server: %v", left)
	}
}

func TestDeploySQLGapProbeSkipsAppliedVersion(t *testing.T) {
	d := newGapWorld(t, "col_b") // v2.9.0 already applied (col_a gone), v2.10.0 not
	out, err := d.run("", "v2.10.0", "--confirm", "v2.10.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"upgrade SQL v2.9.0: already applied (probe) - skipped",
		"upgrade SQL v2.10.0: not yet applied (probe: 1) - runs in the window",
		"upgrade SQL:    applied v2.10.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	if n := sqlRuns(d, "v2.9.0-upgrade.sql"); n != 0 {
		t.Errorf("the probe-applied v2.9.0 upgrade SQL ran (%d)", n)
	}
	if dbHasCol(t, d.dbPath(), "col_b") {
		t.Error("v2.10.0's upgrade SQL did not apply")
	}
}

// assertGapRolledBack: both versions' rollback.sql ran, newest first, before
// the compose file pointed back at the old image; both old shapes are back.
func assertGapRolledBack(t *testing.T, d *deployWorld, out string) {
	t.Helper()
	sshLog := d.logOf("log-ssh")
	r10, r9 := sqlRunIndex(sshLog, "v2.10.0-rollback.sql"), sqlRunIndex(sshLog, "v2.9.0-rollback.sql")
	iSed := strings.Index(sshLog, "sed -i 's|image: polyforge/aihub:v2.10.0|image: polyforge/aihub:v2.8.0|'")
	if !(r10 >= 0 && r10 < r9 && r9 < iSed) {
		t.Errorf("want rollback v2.10.0 -> v2.9.0 -> old image (got %d, %d, %d)\n%s", r10, r9, iSed, sshLog)
	}
	if !dbHasCol(t, d.dbPath(), "col_a") || !dbHasCol(t, d.dbPath(), "col_b") {
		t.Error("the old shapes are not restored by the rollback SQL")
	}
	if !strings.Contains(out, "deployed=ROLLED_BACK previous=v2.8.0 attempted=v2.10.0") {
		t.Errorf("no rolled-back result line\n%s", out)
	}
	if left := stagingLeft(d); len(left) != 0 {
		t.Errorf("SQL staging dir left on the server after the rollback: %v", left)
	}
}

func TestDeploySQLGapRollbackDescending(t *testing.T) {
	d := newGapWorld(t, "col_a", "col_b")
	d.env = append(d.env, "FAKE_BAD_IMG=polyforge/aihub:v2.10.0")
	out, err := d.run("", "v2.10.0", "--confirm", "v2.10.0")
	if exitCode(err) != 10 {
		t.Fatalf("want exit 10, got %v\n%s", err, out)
	}
	assertGapRolledBack(t, d, out)
}

func TestDeploySQLConnectionDropAfterCommitStillRollsBack(t *testing.T) {
	// v2.10.0's upgrade COMMITS, then the ssh connection drops: the runner's
	// exit says failure, the re-run probe says applied -> its rollback.sql
	// must run along with v2.9.0's.
	d := newGapWorld(t, "col_a", "col_b")
	d.env = append(d.env, "FAKE_SSH_DROP_AFTER=v2.10.0-upgrade.sql")
	out, err := d.run("", "v2.10.0", "--confirm", "v2.10.0")
	if exitCode(err) != 10 {
		t.Fatalf("want exit 10, got %v\n%s", err, out)
	}
	if !strings.Contains(out, "upgrade SQL of v2.10.0 reported failure but the probe says it IS applied") {
		t.Errorf("no probe re-check note\n%s", out)
	}
	assertGapRolledBack(t, d, out)
}

func TestDeployRefusesTargetTagMissingLocally(t *testing.T) {
	d := newGapWorld(t, "col_a", "col_b")
	d.git("tag", "-d", "v2.10.0") // a stale clone: released, but not fetched
	out, err := d.run("", "v2.10.0", "--confirm", "v2.10.0")
	if exitCode(err) != 1 || !strings.Contains(out, "tag v2.10.0 not in the local repo - git fetch --tags") {
		t.Fatalf("want exit 1 + the missing-tag refusal, got %v\n%s", err, out)
	}
	d.assertNothingMutated("v2.8.0")
	if strings.Contains(d.logOf("log-ssh"), "aihub-sql-") {
		t.Errorf("SQL staged despite the refusal:\n%s", d.logOf("log-ssh"))
	}
	if !dbHasCol(t, d.dbPath(), "col_a") || !dbHasCol(t, d.dbPath(), "col_b") {
		t.Error("the db changed despite the refusal")
	}
}

func TestDeployRefusesDowngradeAcrossSQLVersion(t *testing.T) {
	// running v2.10.0 (schema already without col_b); a --force downgrade to
	// v2.9.0 would start the v2.9.0 binary on v2.10.0's schema.
	d := newSQLWorldVers(t, "v2.10.0", []string{"v2.9.0"}, []sqlVer{
		{"v2.10.0", "ALTER TABLE projects DROP COLUMN col_b;\n", "ALTER TABLE projects ADD COLUMN col_b TEXT;\n", colProbe("col_b")},
	}, nil)
	out, err := d.run("", "v2.9.0", "--confirm", "v2.9.0", "--force")
	if exitCode(err) != 1 || !strings.Contains(out, "REFUSED: the downgrade v2.10.0 -> v2.9.0 crosses the schema change of v2.10.0") {
		t.Fatalf("want exit 1 + the downgrade refusal, got %v\n%s", err, out)
	}
	d.assertNothingMutated("v2.10.0")
}

// ---- tables an applied upgrade.sql drops on purpose (v2.4.1 incident) ----
//
// The v2.4.0 upgrade.sql drops wi_ship_ledger and core_meta; step 7 used to
// compare EVERY baseline table against the post-swap counts, reported the
// dropped tables "missing" and auto-rolled back a good deploy. A table a
// DROP TABLE of an applied upgrade.sql names is now left out of the check;
// every other table still must hold its rows.

// seedTable creates table name in the fixture db and inserts rows rows.
func seedTable(t *testing.T, dbPath, name string, rows int) {
	t.Helper()
	py := `import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute('create table if not exists "%s"(id integer)' % sys.argv[2])
c.executemany('insert into "%s"(id) values (?)' % sys.argv[2], [(i,) for i in range(int(sys.argv[3]))])
c.commit()`
	if out, err := exec.Command("python3", "-I", "-c", py, dbPath, name, strconv.Itoa(rows)).CombinedOutput(); err != nil {
		t.Fatalf("seed %s: %v\n%s", name, err, out)
	}
}

// newDropWorld: the server runs v2.2.1 with a populated wi_ship_ledger and
// Core_Meta (mixed case on purpose) plus rows in machines; v2.3.0's
// upgrade.sql is upgradeSQL, its probe reports "not yet applied" while
// wi_ship_ledger exists.
func newDropWorld(t *testing.T, upgradeSQL string) *deployWorld {
	t.Helper()
	d := newSQLWorldVers(t, "v2.2.1", []string{"v2.3.0"}, []sqlVer{{"v2.3.0", upgradeSQL,
		"CREATE TABLE wi_ship_ledger(id integer);\nCREATE TABLE \"Core_Meta\"(id integer);\n",
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'wi_ship_ledger';\n"}}, nil)
	seedTable(t, d.dbPath(), "wi_ship_ledger", 94)
	seedTable(t, d.dbPath(), "Core_Meta", 2)
	seedTable(t, d.dbPath(), "machines", 3)
	return d
}

// dropUpgradeSQL: the shapes the extraction must see through — IF EXISTS,
// a quoted mixed-case name, a statement over two lines — and the ones it
// must NOT take as a drop (a -- comment, a /* */ comment). (The SQL runner
// itself refuses two statements on one line, so none is used here.)
const dropUpgradeSQL = `-- drops the dead ledger; DROP TABLE machines would be a comment here
/* DROP TABLE machines; */
DROP TABLE IF EXISTS wi_ship_ledger;
DROP TABLE
  IF EXISTS "Core_Meta";
`

func TestDeployVerifyExcludesTablesTheUpgradeDrops(t *testing.T) {
	d := newDropWorld(t, dropUpgradeSQL)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"table wi_ship_ledger dropped by the upgrade SQL (was 94) - expected",
		"table Core_Meta dropped by the upgrade SQL (was 2) - expected",
		"row counts >= baseline",
		"deployed=v2.3.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	for _, bad := range []string{"missing (was", "BELOW the baseline", "ROLLED_BACK", "dropped by the upgrade SQL (was 3)"} {
		if strings.Contains(out, bad) {
			t.Errorf("output carries %q\n%s", bad, out)
		}
	}
	if n := sqlRuns(d, "v2.3.0-rollback.sql"); n != 0 {
		t.Errorf("rollback SQL ran %d times on a good deploy", n)
	}
}

func TestDeployVerifyStillStrictForTablesTheUpgradeKeeps(t *testing.T) {
	// the same drops, but machines (not dropped - only named in comments)
	// loses its rows: that is still a loss and must roll back.
	d := newDropWorld(t, dropUpgradeSQL+"DELETE FROM machines;\n")
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 10 {
		t.Fatalf("want exit 10, got %v\n%s", err, out)
	}
	for _, want := range []string{
		"table machines LOST rows: 0 < 3",
		"row counts after the swap are BELOW the baseline",
		"deployed=ROLLED_BACK previous=v2.2.1 attempted=v2.3.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "table wi_ship_ledger missing") || strings.Contains(out, "table Core_Meta missing") {
		t.Errorf("a dropped table was still compared\n%s", out)
	}
	if n := sqlRuns(d, "v2.3.0-rollback.sql"); n != 1 {
		t.Errorf("rollback SQL ran %d times, want 1", n)
	}
}

// ---- a table rebuild (drop + re-create under the same name) ----
//
// The SQLite table-rebuild idiom DROPs the table, but the table exists again
// after the swap: it is NOT exempt and its rows are compared like any other.

// rebuildUpgradeSQL rebuilds audit_log keeping the rows matched by where
// (one statement per line: the runner refuses two on one line), plus the
// test column drop that makes the probe report "not yet applied".
func rebuildUpgradeSQL(where string) string {
	return "ALTER TABLE projects DROP COLUMN identifier_hash;\n" +
		"CREATE TABLE audit_log_new(id integer);\n" +
		"INSERT INTO audit_log_new SELECT id FROM audit_log" + where + ";\n" +
		"DROP TABLE audit_log;\n" +
		"ALTER TABLE audit_log_new RENAME TO audit_log;\n"
}

func newRebuildWorld(t *testing.T, upgradeSQL string) *deployWorld {
	t.Helper()
	d := newSQLWorldVers(t, "v2.2.1", []string{"v2.3.0"},
		[]sqlVer{{"v2.3.0", upgradeSQL, testRollbackSQL, testProbeSQL}}, []string{"identifier_hash"})
	seedTable(t, d.dbPath(), "audit_log", 5)
	return d
}

func TestDeployVerifyChecksRebuiltTables(t *testing.T) {
	t.Run("rebuild loses rows", func(t *testing.T) {
		d := newRebuildWorld(t, rebuildUpgradeSQL(" WHERE id >= 2"))
		out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
		if exitCode(err) != 10 {
			t.Fatalf("want exit 10, got %v\n%s", err, out)
		}
		for _, want := range []string{
			"table audit_log LOST rows: 3 < 5",
			"row counts after the swap are BELOW the baseline",
			"deployed=ROLLED_BACK previous=v2.2.1 attempted=v2.3.0",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q\n%s", want, out)
			}
		}
		if strings.Contains(out, "table audit_log dropped by the upgrade SQL") {
			t.Errorf("a rebuilt table was exempted as dropped\n%s", out)
		}
		if n := sqlRuns(d, "v2.3.0-rollback.sql"); n != 1 {
			t.Errorf("rollback SQL ran %d times, want 1", n)
		}
	})
	t.Run("rebuild keeps rows", func(t *testing.T) {
		d := newRebuildWorld(t, rebuildUpgradeSQL(""))
		out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
		if err != nil {
			t.Fatalf("deploy failed: %v\n%s", err, out)
		}
		for _, want := range []string{"row counts >= baseline", "deployed=v2.3.0"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q\n%s", want, out)
			}
		}
		for _, bad := range []string{"dropped by the upgrade SQL", "ROLLED_BACK"} {
			if strings.Contains(out, bad) {
				t.Errorf("output carries %q\n%s", bad, out)
			}
		}
	})
}

// ---- the real v2.5.0 window SQL (polyforge#157) through deploy.sh ----
//
// The deploy drill the ticket asks for: the repo's own deploy/sql/v2.5.0
// files run on a fixture holding an (empty, as in production)
// wi_repo_inventory. The good deploy passes the post-swap check with the
// table exempted as dropped; a failed deploy runs the rollback SQL and the
// old shape is back.

// realSQL reads one file of the repo's deploy/sql/v2.5.0.
func realSQL(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "deploy", "sql", "v2.5.0", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func newV250World(t *testing.T) *deployWorld {
	t.Helper()
	d := newSQLWorldVers(t, "v2.4.2", []string{"v2.5.0"}, []sqlVer{{"v2.5.0",
		realSQL(t, "upgrade.sql"), realSQL(t, "rollback.sql"), realSQL(t, "probe.sql")}}, nil)
	seedTable(t, d.dbPath(), "wi_repo_inventory", 0)
	return d
}

// dbTableCols answers the column names of table in the fixture db.
func dbTableCols(t *testing.T, dbPath, table string) []string {
	t.Helper()
	py := `import sqlite3, sys
print(" ".join(r[1] for r in sqlite3.connect(sys.argv[1]).execute("pragma table_info(%s)" % sys.argv[2])))`
	out, err := exec.Command("python3", "-I", "-c", py, dbPath, table).CombinedOutput()
	if err != nil {
		t.Fatalf("columns of %s: %v\n%s", table, err, out)
	}
	return strings.Fields(string(out))
}

func hasCol(cols []string, c string) bool {
	for _, x := range cols {
		if x == c {
			return true
		}
	}
	return false
}

func TestDeployV250RepoInventorySQL(t *testing.T) {
	t.Run("good deploy", func(t *testing.T) {
		d := newV250World(t)
		out, err := d.run("", "v2.5.0", "--confirm", "v2.5.0")
		if err != nil {
			t.Fatalf("deploy failed: %v\n%s", err, out)
		}
		for _, want := range []string{
			"upgrade SQL v2.5.0: not yet applied (probe: 2) - runs in the window",
			"table wi_repo_inventory dropped by the upgrade SQL (was 0) - expected",
			"row counts >= baseline",
			"deployed=v2.5.0",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q\n%s", want, out)
			}
		}
		if !hasCol(dbTableCols(t, d.dbPath(), "work_items"), "repo_inventory") {
			t.Error("work_items.repo_inventory missing after the deploy")
		}
		if len(dbTableCols(t, d.dbPath(), "wi_repo_inventory")) != 0 {
			t.Error("wi_repo_inventory still present after the deploy")
		}
	})
	t.Run("failed deploy rolls the schema back", func(t *testing.T) {
		d := newV250World(t)
		d.env = append(d.env, "FAKE_BAD_IMG=polyforge/aihub:v2.5.0")
		out, err := d.run("", "v2.5.0", "--confirm", "v2.5.0")
		if exitCode(err) != 10 {
			t.Fatalf("want exit 10, got %v\n%s", err, out)
		}
		if !strings.Contains(out, "schema: rolled back by the rollback.sql of v2.5.0") {
			t.Errorf("no schema rollback line\n%s", out)
		}
		if hasCol(dbTableCols(t, d.dbPath(), "work_items"), "repo_inventory") {
			t.Error("work_items.repo_inventory still present after the rollback")
		}
		if !hasCol(dbTableCols(t, d.dbPath(), "wi_repo_inventory"), "base") {
			t.Error("wi_repo_inventory not rebuilt by the rollback")
		}
	})
}

// ---- the real heartbeat migration dir (workbench#66) ----
//
// deploy.sh selects window SQL only by the LOCAL release tags in
// (OLDVER, TAG]; a deploy/sql dir with no tag of the same name is never
// visited. The heartbeat migration first landed as deploy/sql/v2.21.0, a
// minor tag release.sh refuses to cut, so no v2.20.* deploy would apply it:
// a v2.20.7 cut from that main would run the workbench#57 code on the
// v2.20.6 schema, where a new machine's first registration fails with
// machines.user_id NOT NULL (root-cause artifact E1). No v2.20.7 has been
// cut; the v2.20.6 deploy did apply its own dir. The dir must be named after
// the patch tag that carries it (v2.20.8).

// realHeartbeatSQLDirs: the deploy/sql dirs whose upgrade.sql is the
// workbench#57 machines.user_id migration.
func realHeartbeatSQLDirs(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "deploy", "sql")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read the real deploy/sql: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, e.Name(), "upgrade.sql"))
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), "DROP COLUMN user_id") {
			out = append(out, e.Name())
		}
	}
	return out
}

// copyRealSQLTree copies the repo's whole deploy/sql tree into the deployer
// repo (the working-tree fallback sql_fetch reads). The tree is taken as
// is: the test must see the real dir names, not a renamed copy.
func copyRealSQLTree(t *testing.T, d *deployWorld) {
	t.Helper()
	root := filepath.Join("..", "deploy", "sql")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(d.repo, "deploy", "sql", rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o644)
	})
	if err != nil {
		t.Fatalf("copy the real deploy/sql tree: %v", err)
	}
}

// TestDeployV2208HeartbeatSQLSelected: the heartbeat migration lives in
// deploy/sql/v2.20.8 and a v2.20.6 -> v2.20.8 deploy selects it, in order
// with the v2.20.7 migration #56 added (both tags cut, as a consecutive
// double release does — skipping v2.20.7 would drop #56's SQL). Before the
// fix the dir was v2.21.0: the first assertion found v2.21.0 and the dry-run
// reported no SQL candidate. Both fail on the old tree.
func TestDeployV2208HeartbeatSQLSelected(t *testing.T) {
	if got := realHeartbeatSQLDirs(t); len(got) != 1 || got[0] != "v2.20.8" {
		t.Fatalf("the machines.user_id migration must live in exactly one deploy/sql dir named v2.20.8 (the patch tag that carries it), found %v", got)
	}
	d := newDeployWorld(t, "v2.20.6")
	copyRealSQLTree(t, d)
	d.git("add", "-A")
	d.git("commit", "-q", "-m", "real deploy/sql")
	d.release("v2.20.6", true, digOld, digOld)
	d.release("v2.20.7", true, digOld, digOld)
	d.release("v2.20.8", true, digNew, digNew)
	out, err := d.run("", "v2.20.8", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run deploy failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "upgrade SQL candidates (dry-run - the probes run at deploy time): v2.20.7 v2.20.8") {
		t.Fatalf("a v2.20.6 -> v2.20.8 deploy must select deploy/sql/v2.20.7 then deploy/sql/v2.20.8 (the #56 and heartbeat migrations run in the window, in order); output:\n%s", out)
	}
}
