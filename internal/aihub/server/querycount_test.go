package server

// querycount_test.go: the statement-counting driver the
// search/list query-cost criteria assert with (the owner rejected
// timing assertions — counts are the evidence). It wraps modernc's
// sqlite driver and records every prepared statement's SQL text; a
// *core.DB opened through it behaves exactly like core.Open's (same DSN
// pragmas, one connection), so a test can assert "SQL statement count
// and project-membership query count stay constant" on the real handler
// path.

import (
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"modernc.org/sqlite"
)

// stmtCounts is the process-wide recorder every counting connection
// writes to (server tests run serialized inside the package).
type stmtCounts struct {
	mu         sync.Mutex
	statements []string
}

var counts = &stmtCounts{}

// reset drops every recorded statement (call right before the measured
// request, after the env is seeded).
func (c *stmtCounts) reset() {
	c.mu.Lock()
	c.statements = nil
	c.mu.Unlock()
}

// snapshot answers the total prepared-statement count and how many of
// them touch project_members (the membership-lookup criterion).
func (c *stmtCounts) snapshot() (total, membership int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, q := range c.statements {
		if strings.Contains(q, "project_members") {
			membership++
		}
	}
	return len(c.statements), membership
}

// having answers how many recorded statements contain substr (the
// per-candidate entry loads are counted this way).
func (c *stmtCounts) having(substr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, q := range c.statements {
		if strings.Contains(q, substr) {
			n++
		}
	}
	return n
}

// countingDriver delegates to modernc's zero Driver — the same
// amalgamation (FTS5 trigram included) the production "sqlite"
// registration runs on; this repo registers no custom functions or
// collations a zero Driver would miss, so connections it opens are
// equivalent to the production ones.
type countingDriver struct{ inner driver.Driver }

func (d countingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn}, nil
}

// countingConn forwards the base driver.Conn interface only: database/sql
// then takes its Prepare fallback for every Exec/Query, which is exactly
// what gets counted.
type countingConn struct{ driver.Conn }

func (c *countingConn) Prepare(query string) (driver.Stmt, error) {
	counts.mu.Lock()
	counts.statements = append(counts.statements, query)
	counts.mu.Unlock()
	return c.Conn.Prepare(query)
}

func init() {
	sql.Register("sqlite-stmtcount", countingDriver{inner: &sqlite.Driver{}})
}

// newCountingSrvEnv answers a server env whose database runs through the
// counting driver: the core schema is installed with the production
// driver (core.Open also creates the file), then the same data dir is
// reopened with core.Open's DSN pragmas through the counter.
func newCountingSrvEnv(t *testing.T) *srvEnv {
	t.Helper()
	dataDir := t.TempDir()
	seed, err := core.Open(dataDir)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
	dsn := "file:" + filepath.Join(dataDir, "aihub.db") +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)"
	sqlDB, err := sql.Open("sqlite-stmtcount", dsn)
	if err != nil {
		t.Fatalf("open counting db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	db := &core.DB{DB: sqlDB}
	env := newSrvEnvWithDB(t, dataDir, db)
	counts.reset()
	return env
}
