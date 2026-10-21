-- deploy/sql/v2.14.0/rollback.sql — the automatic-rollback counterpart of
-- upgrade.sql (polyforge#194).
--
-- deploy.sh runs this ONLY for a version applied in the same deploy run,
-- as ONE SQLite transaction, BEFORE the old image is recreated.
--
-- Semantics: the two columns come back NULL (v2.13.0 never reads them);
-- claim_idempotency is rebuilt EMPTY from the v2.13.0 DDL (v2.13.0 reads
-- and writes it: a claim replayed after the rollback whose cache row was
-- dropped is answered as a fresh claim of an already-held work item).
ALTER TABLE memories ADD COLUMN fs_mtime_ns INTEGER;
ALTER TABLE memories ADD COLUMN fs_size INTEGER;
CREATE TABLE IF NOT EXISTS claim_idempotency (
  idempotency_key TEXT PRIMARY KEY,
  work_item_id    TEXT NOT NULL REFERENCES work_items(id),
  caller_identity TEXT NOT NULL,
  response_json   TEXT NOT NULL,
  created_at      TEXT NOT NULL
);
