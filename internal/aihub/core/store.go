// Package core is the v1 AIHub persistent layer: SQLite store (single
// writer, WAL), work-item state machine, claim/generation arbitration with
// attempt-scoped push credentials, the ready queue and the admin face.
//
// Current behavior: docs/design/polyforge-vnext/reference.md §2–§5 (the v1
// and v0 records it was built from are under docs/design/polyforge-vnext/archive/). The v0
// revision chain (capsule_revisions / capsule_heads / SYNC-*) is deleted:
// git IS the revision chain; aihub serves one bare repo per WI.
package core

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB is the Core database handle. All writes go through one connection
// (single writer); reads are concurrent under WAL.
type DB struct {
	*sql.DB
}

// DDL is the Core schema. First boot installs it as-is; Open runs no
// column migrations (existing databases already carry every column) —
// an index added later is created on the next Open (IF NOT EXISTS). The
// one exception is memories.content, added by the memory store's
// startup content migration (memory/migrate.go), not by Open. Seq is
// numbered per project: UNIQUE(project_id, seq). Ownership lives ON the
// work_items row itself (the session model — session_hash/
// session_fingerprint/clean_pause).
const DDL = `
CREATE TABLE IF NOT EXISTS users (
  id           TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  email        TEXT,
  kind         TEXT NOT NULL CHECK (kind IN ('human','machine')),
  role         TEXT NOT NULL DEFAULT 'writer' CHECK (role IN ('admin','writer')),
  created_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS api_keys (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id),
  project_id  TEXT REFERENCES projects(id),
  name        TEXT NOT NULL DEFAULT '',
  key_hash    TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  revoked_at  TEXT
);

CREATE TABLE IF NOT EXISTS machines (
  id          TEXT PRIMARY KEY,
  runtime_version TEXT,
  -- M3 heartbeat wiring (lane hb-m3, additive): the daemon's liveness
  -- payload carries its current mode and the WI ids it holds live, so the
  -- machines read face (GET /api/v1/machines) answers not just "alive?"
  -- but "alive and running what?". live_wi_ids is a JSON array string.
  mode        TEXT,
  live_wi_ids TEXT,
  last_heartbeat_at TEXT NOT NULL,
  -- workbench#11 machine identity: the daemon's Ed25519 public key
  -- (base64), launcher kind, negotiated role, the operator user this
  -- machine speaks for, and the supersession stamp. capability is the
  -- JSON capability card the daemon advertised. workbench#57: the
  -- operator is the machine's one identity — there is no machine user
  -- column (registration mints no users row).
  public_key    TEXT,
  launcher      TEXT,
  role          TEXT NOT NULL DEFAULT 'runner' CHECK (role IN ('interactive','runner')),
  operator_id   TEXT REFERENCES users(id),
  superseded_at TEXT,
  capability    TEXT
);

CREATE TABLE IF NOT EXISTS conversations (
  id         TEXT PRIMARY KEY,
  kind       TEXT NOT NULL DEFAULT 'direct' CHECK (kind IN ('direct')),
  -- sorted user-id pair: one direct conversation per pair, forever.
  direct_key TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS conversation_members (
  conversation_id TEXT NOT NULL REFERENCES conversations(id),
  user_id         TEXT NOT NULL REFERENCES users(id),
  last_read_seq   INTEGER NOT NULL DEFAULT 0,
  joined_at       TEXT NOT NULL,
  PRIMARY KEY (conversation_id, user_id)
);

CREATE TABLE IF NOT EXISTS messages (
  seq             INTEGER PRIMARY KEY AUTOINCREMENT,
  id              TEXT NOT NULL UNIQUE,
  conversation_id TEXT NOT NULL REFERENCES conversations(id),
  sender_id       TEXT NOT NULL REFERENCES users(id),
  text            TEXT NOT NULL,
  about_wi        TEXT,
  reply_to        TEXT REFERENCES messages(id),
  created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_conv_seq ON messages(conversation_id, seq);

CREATE TABLE IF NOT EXISTS projects (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  repos       TEXT NOT NULL DEFAULT '[]',
  visible     INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS project_members (
  project_id TEXT NOT NULL REFERENCES projects(id),
  user_id    TEXT NOT NULL REFERENCES users(id),
  role       TEXT NOT NULL CHECK (role IN ('viewer','writer','maintainer')),
  created_at TEXT NOT NULL,
  PRIMARY KEY (project_id, user_id)
);

CREATE TABLE IF NOT EXISTS work_items (
  id           TEXT PRIMARY KEY,
  -- seq is the per-project display number (<project>#<seq>): unique per
  -- project, NOT globally (the legacy library numbers per project —
  -- aiterm#1 and defswitch#1 both exist). The constraint lives at the
  -- bottom of this table definition.
  seq          INTEGER NOT NULL,
  project_id   TEXT NOT NULL REFERENCES projects(id),
  goal         TEXT NOT NULL,
  content      TEXT,
  parent_id    TEXT REFERENCES work_items(id),
  status       TEXT NOT NULL DEFAULT 'queued'
               CHECK (status IN ('queued','running','paused','blocked',
                                 'wrapped','failed','canceled')),
  priority     TEXT NOT NULL DEFAULT 'normal'
               CHECK (priority IN ('low','normal','high','urgent')),
  milestone    TEXT,
  labels       TEXT NOT NULL DEFAULT '[]',
  attrs        TEXT NOT NULL DEFAULT '{}',
  reporter     TEXT NOT NULL REFERENCES users(id),
  assignee     TEXT REFERENCES users(id),
  waiting_on   TEXT,
  bundle_name  TEXT,
  -- §4.6 repo inventory: JSON array of project repo names [{"name":..}]
  -- frozen at create (no UPDATE path; URLs are resolved from the project
  -- at claim); NULL = no inventory (the claim then serves every project
  -- repo). deploy/sql/v2.5.0 adds it to older databases.
  repo_inventory TEXT,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  closed_at    TEXT,
  -- v1.5 session model (v1.5-session-model.md / v1.5-api-contract.md):
  -- the ownership epoch lives ON the row. session_hash is
  -- hex(sha256(session_id)) — the SECRET itself never lands here;
  -- session_fingerprint is its 16-hex public prefix (B3 split).
  -- clean_pause marks an OPERATOR pause (M3): a restart must not
  -- auto-resume it, unlike a daemon-shutdown pause (0).
  session_hash        TEXT,
  session_fingerprint TEXT,
  session_machine     TEXT,
  session_started_at  TEXT,
  clean_pause         INTEGER NOT NULL DEFAULT 0,
  -- per-project display seq (seq-fix lane owner ruling).
  UNIQUE (project_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_wi_status     ON work_items(status, priority DESC, created_at);
CREATE INDEX IF NOT EXISTS idx_wi_assignee   ON work_items(assignee) WHERE status IN ('running','paused','blocked');
CREATE INDEX IF NOT EXISTS idx_wi_waiting_on ON work_items(waiting_on) WHERE waiting_on IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_wi_parent     ON work_items(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_wi_milestone  ON work_items(project_id, milestone) WHERE milestone IS NOT NULL;

CREATE TABLE IF NOT EXISTS wi_dependencies (
  blocking_wi_id TEXT NOT NULL REFERENCES work_items(id),
  blocked_wi_id  TEXT NOT NULL REFERENCES work_items(id),
  kind           TEXT NOT NULL CHECK (kind IN ('blocks','supersedes','related')),
  created_at     TEXT NOT NULL,
  PRIMARY KEY (blocking_wi_id, blocked_wi_id, kind),
  CHECK (blocking_wi_id <> blocked_wi_id)
);
CREATE INDEX IF NOT EXISTS idx_dep_blocked ON wi_dependencies(blocked_wi_id, kind);

CREATE TABLE IF NOT EXISTS wi_events (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  work_item_id TEXT NOT NULL REFERENCES work_items(id),
  created_at   TEXT NOT NULL,
  actor        TEXT REFERENCES users(id),
  event_type   TEXT NOT NULL,
  payload      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_events_wi ON wi_events(work_item_id, id);

CREATE TABLE IF NOT EXISTS memories (
  id           TEXT PRIMARY KEY,
  project_id   TEXT NOT NULL REFERENCES projects(id),
  owner_user_id TEXT NOT NULL REFERENCES users(id),
  work_item_id TEXT REFERENCES work_items(id),
  type         TEXT NOT NULL,
  title        TEXT,
  visibility   TEXT NOT NULL,
  -- memory/<project>/<id>: where a database from before the content
  -- column kept the row's memory.md; read only by the content migration
  -- (memory/migrate.go).
  authority_path TEXT NOT NULL,
  content_sha  TEXT NOT NULL,
  content_len  INTEGER NOT NULL,
  tags         TEXT NOT NULL DEFAULT '[]',
  related      TEXT NOT NULL DEFAULT '[]',
  idempotency_key TEXT UNIQUE,
  status       TEXT NOT NULL DEFAULT 'active',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  -- The stored document (frontmatter + body); content_sha/content_len
  -- describe it. NULL only on a row the memory content migration could
  -- not fill (memory/migrate.go): such a row is never served.
  content      TEXT
);

CREATE INDEX IF NOT EXISTS idx_mem_work_item ON memories(work_item_id) WHERE work_item_id IS NOT NULL;

CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
  mem_id UNINDEXED, body,
  tokenize='trigram'
);

CREATE TABLE IF NOT EXISTS admin_audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  -- One row per performed project hard delete (DELETE
  -- /api/v1/projects/{id}): the actor (user id), the deleted project's
  -- id and name, and the deleted work-item ids as a JSON array — the
  -- durable record of the per-WI bare repos to remove from disk (the
  -- startup replay converges a delete that died between its commit
  -- and the removal). No FK to anything: the table must survive the
  -- delete cascade it records. No read route or verb exposes it;
  -- operators inspect it with sqlite directly.
  actor        TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  project_id  TEXT NOT NULL,
  project_name TEXT NOT NULL,
  wi_ids      TEXT NOT NULL DEFAULT '[]'
);
`

// Open opens (creating if needed) the Core database at
// <data_dir>/aihub.db and installs the schema.
func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("core: create data dir: %w", err)
	}
	path := filepath.Join(dataDir, "aihub.db")
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("core: open: %w", err)
	}
	// Single writer: one connection serializes every write; WAL still
	// serves concurrent external readers on the same file.
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(DDL); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("core: schema: %w", err)
	}
	return &DB{sqlDB}, nil
}

// Now returns the canonical RFC3339 UTC timestamp for all Core columns.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// NewID returns prefix + 12 random alphanumeric characters.
func NewID(prefix string) (string, error) {
	var b [12]byte
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("core: rand: %w", err)
	}
	out := make([]byte, 12)
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return prefix + string(out), nil
}

// NewSecret returns n cryptographically random bytes, hex encoded.
func NewSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("core: rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}
