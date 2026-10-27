-- deploy/sql/v2.4.0/rollback.sql — the automatic-rollback counterpart of
-- upgrade.sql (polyforge#149).
--
-- deploy.sh runs this ONLY for a version applied in the same deploy run,
-- in descending version order, each script as ONE SQLite transaction,
-- BEFORE the old image is recreated: the old binary must never see the
-- post-upgrade schema.
--
-- Semantics: the dropped columns are re-added WITH DEFAULTS (existing rows
-- carry the default; SQLite ADD COLUMN NOT NULL requires one); the dropped
-- tables are rebuilt as EMPTY tables from the 85cf926 DDL. Old-version code
-- reads and writes correctly on the result. No backup restore is involved.
ALTER TABLE projects ADD COLUMN scenario TEXT;
ALTER TABLE projects ADD COLUMN identifier_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE work_items ADD COLUMN bundle_digest TEXT;
ALTER TABLE work_items ADD COLUMN workflow_budget_baseline TEXT;
ALTER TABLE work_items ADD COLUMN session_owner TEXT;
CREATE TABLE IF NOT EXISTS wi_ship_ledger (
  work_item_id   TEXT NOT NULL REFERENCES work_items(id),
  repo           TEXT NOT NULL,
  branch         TEXT NOT NULL DEFAULT '',
  branch_top_sha TEXT NOT NULL,
  pushed_at      TEXT NOT NULL,
  PRIMARY KEY (work_item_id, repo)
);
CREATE TABLE IF NOT EXISTS core_meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
