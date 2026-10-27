package core

// upgrade_v2207_test.go: the workbench#56 migration's own proof on a
// POPULATED v2.20.6-shaped database (grill row 7). The fixture DDL is the
// shipped v2.20.6 messaging shape copied verbatim; the real
// deploy/sql/v2.20.7/*.sql files run against it. Assertions: the split
// cursors are backfilled, the old column is gone, the live list's unread
// excludes the caller's own messages, rollback restores the old shape from
// the delivery position, and the probe answers before/after/after-rollback.

import (
	"database/sql"
	"path/filepath"
	"testing"
)

const v2206MessagingDDL = `
CREATE TABLE users (
  id           TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  email        TEXT,
  kind         TEXT NOT NULL CHECK (kind IN ('human','machine')),
  role         TEXT NOT NULL DEFAULT 'writer' CHECK (role IN ('admin','writer')),
  created_at   TEXT NOT NULL
);
CREATE TABLE conversations (
  id         TEXT PRIMARY KEY,
  kind       TEXT NOT NULL DEFAULT 'direct' CHECK (kind IN ('direct')),
  direct_key TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);
CREATE TABLE conversation_members (
  conversation_id TEXT NOT NULL REFERENCES conversations(id),
  user_id         TEXT NOT NULL REFERENCES users(id),
  last_read_seq   INTEGER NOT NULL DEFAULT 0,
  joined_at       TEXT NOT NULL,
  PRIMARY KEY (conversation_id, user_id)
);
CREATE TABLE messages (
  seq             INTEGER PRIMARY KEY AUTOINCREMENT,
  id              TEXT NOT NULL UNIQUE,
  conversation_id TEXT NOT NULL REFERENCES conversations(id),
  sender_id       TEXT NOT NULL REFERENCES users(id),
  text            TEXT NOT NULL,
  about_wi        TEXT,
  reply_to        TEXT REFERENCES messages(id),
  created_at      TEXT NOT NULL
);
CREATE INDEX idx_messages_conv_seq ON messages(conversation_id, seq);
`

// v2207Probe answers probe.sql's final statement: 0 = applied.
func v2207Probe(t *testing.T, db *sql.DB) int {
	t.Helper()
	stmts := upgradeStatements(loadSQLFileOf(t, sqlVer2207, "probe.sql"))
	if len(stmts) == 0 {
		t.Fatal("probe.sql carries no statements")
	}
	var v int
	if err := db.QueryRow(stmts[len(stmts)-1]).Scan(&v); err != nil {
		t.Fatalf("probe statement failed: %v\n%s", err, stmts[len(stmts)-1])
	}
	return v
}

func v2207ColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?1) WHERE name=?2`, table, column).Scan(&n); err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	return n > 0
}

// v2207Fixture builds the populated v2.20.6-shaped database and answers its
// path plus the open raw handle.
func v2207Fixture(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "aihub.db"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	db.SetMaxOpenConns(1)
	applySQLText(t, db, v2206MessagingDDL)
	for _, stmt := range []string{
		`INSERT INTO users(id, display_name, kind, role, created_at) VALUES
		 ('u-alice','Alice','human','writer','2026-10-01T00:00:00Z'),
		 ('u-bob','Bob','human','writer','2026-10-01T00:00:00Z'),
		 ('u-carol','Carol','human','writer','2026-10-01T00:00:00Z'),
		 ('u-dave','Dave','human','writer','2026-10-01T00:00:00Z')`,
		`INSERT INTO conversations(id, kind, direct_key, created_at) VALUES
		 ('conv_old','direct','u-alice' || char(0) || 'u-bob','2026-10-01T00:00:00Z')`,
		// Alice has read through seq 2; Bob through seq 4.
		`INSERT INTO conversation_members(conversation_id,user_id,last_read_seq,joined_at) VALUES
		 ('conv_old','u-alice',2,'2026-10-01T00:00:00Z'),
		 ('conv_old','u-bob',4,'2026-10-01T00:00:00Z')`,
		// Own and other-participant messages above Alice's old cursor.
		`INSERT INTO messages(id,conversation_id,sender_id,text,created_at) VALUES
		 ('msg_1','conv_old','u-alice','one','2026-10-01T00:00:01Z'),
		 ('msg_2','conv_old','u-bob','two','2026-10-01T00:00:02Z'),
		 ('msg_3','conv_old','u-alice','three','2026-10-01T00:00:03Z'),
		 ('msg_4','conv_old','u-bob','four','2026-10-01T00:00:04Z')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed fixture: %v\n%s", err, stmt)
		}
	}
	return dataDir, db
}

func TestUpgradeV2207CursorSplitOnPopulatedDB(t *testing.T) {
	dataDir, raw := v2207Fixture(t)
	defer raw.Close()

	if v := v2207Probe(t, raw); v == 0 {
		t.Fatal("probe answers 0 (already applied) on a v2.20.6-shaped database")
	}
	if !v2207ColumnExists(t, raw, "conversation_members", "last_read_seq") {
		t.Fatal("fixture is not v2.20.6-shaped: last_read_seq is absent")
	}

	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2207, "upgrade.sql"))
	if v := v2207Probe(t, raw); v != 0 {
		t.Fatalf("probe answers %d (apply) after the upgrade", v)
	}
	if v2207ColumnExists(t, raw, "conversation_members", "last_read_seq") {
		t.Fatal("upgrade left the old last_read_seq column in place (no dual path)")
	}
	for _, member := range []string{"u-alice", "u-bob"} {
		var delivered, read, old int64
		if err := raw.QueryRow(`SELECT delivered_seq, read_seq FROM conversation_members
			WHERE conversation_id='conv_old' AND user_id=?1`, member).Scan(&delivered, &read); err != nil {
			t.Fatalf("cursor row for %s: %v", member, err)
		}
		if member == "u-alice" {
			old = 2
		} else {
			old = 4
		}
		if delivered != old || read != old {
			t.Fatalf("%s cursors = delivered %d / read %d, want %d / %d", member, delivered, read, old, old)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	// The live list on the upgraded file counts only the other
	// participant's messages above the read cursor.
	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("open upgraded db: %v", err)
	}
	list, err := NewAdminManager(db).ListConversations("u-alice", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Unread != 1 {
		t.Fatalf("upgraded unread = %+v, want 1 (only Bob's msg_4; own msg_3 excluded)", list)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Rollback: the old single cursor takes the DELIVERY position, both new
	// columns disappear, and the old insert shape works again.
	raw, err = sql.Open("sqlite", "file:"+filepath.Join(dataDir, "aihub.db"))
	if err != nil {
		t.Fatalf("reopen for rollback: %v", err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`UPDATE conversation_members SET delivered_seq=4, read_seq=3
		WHERE conversation_id='conv_old' AND user_id='u-alice'`); err != nil {
		t.Fatal(err)
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2207, "rollback.sql"))
	if v := v2207Probe(t, raw); v == 0 {
		t.Fatal("probe answers 0 (already applied) after the rollback")
	}
	if v2207ColumnExists(t, raw, "conversation_members", "delivered_seq") ||
		v2207ColumnExists(t, raw, "conversation_members", "read_seq") {
		t.Fatal("rollback left a split-cursor column behind")
	}
	var last int64
	if err := raw.QueryRow(`SELECT last_read_seq FROM conversation_members
		WHERE conversation_id='conv_old' AND user_id='u-alice'`).Scan(&last); err != nil {
		t.Fatalf("rolled-back old cursor: %v", err)
	}
	if last != 4 {
		t.Fatalf("rolled-back last_read_seq = %d, want the delivery position 4", last)
	}
	if _, err := raw.Exec(`INSERT INTO conversation_members(conversation_id,user_id,last_read_seq,joined_at)
		VALUES ('conv_old','u-carol',1,'2026-10-02T00:00:00Z')`); err != nil {
		t.Fatalf("old-shape member insert after rollback: %v", err)
	}

	// A freshly created database carries the new shape and not the old one.
	freshDir := t.TempDir()
	fresh, err := Open(freshDir)
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	defer fresh.Close()
	if !v2207ColumnExists(t, fresh.DB, "conversation_members", "delivered_seq") ||
		!v2207ColumnExists(t, fresh.DB, "conversation_members", "read_seq") {
		t.Fatal("a fresh database does not carry the split cursors")
	}
	if v2207ColumnExists(t, fresh.DB, "conversation_members", "last_read_seq") {
		t.Fatal("a fresh database still carries last_read_seq")
	}
}
