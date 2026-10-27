-- deploy/sql/v2.20.6/upgrade.sql — workbench#11 cross-machine identity,
-- query relay and user-to-user messaging.
--
-- Applied by .polyforge/deploy.sh INSIDE the switch window as ONE SQLite
-- transaction (see docs/upgrade.md). Query responses are relayed in
-- memory only; the durable state added here is machine identity and the
-- message history.
ALTER TABLE machines ADD COLUMN public_key TEXT;
ALTER TABLE machines ADD COLUMN launcher TEXT;
ALTER TABLE machines ADD COLUMN role TEXT NOT NULL DEFAULT 'runner' CHECK (role IN ('interactive','runner'));
ALTER TABLE machines ADD COLUMN operator_id TEXT REFERENCES users(id);
ALTER TABLE machines ADD COLUMN superseded_at TEXT;
ALTER TABLE machines ADD COLUMN capability TEXT;
CREATE TABLE IF NOT EXISTS conversations (
  id         TEXT PRIMARY KEY,
  kind       TEXT NOT NULL DEFAULT 'direct' CHECK (kind IN ('direct')),
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
