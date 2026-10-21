-- deploy/sql/v2.5.0/rollback.sql — the automatic-rollback counterpart of
-- upgrade.sql (polyforge#157).
--
-- deploy.sh runs this ONLY for a version applied in the same deploy run,
-- as ONE SQLite transaction, BEFORE the old image is recreated.
--
-- Semantics: the column is dropped (an inventory filed during the failed
-- deploy is lost with it — the old binary has nowhere to read it from);
-- wi_repo_inventory and its unique index are rebuilt EMPTY from the
-- v2.4.x DDL. Old-version code reads and writes correctly on the result.
ALTER TABLE work_items DROP COLUMN repo_inventory;
CREATE TABLE IF NOT EXISTS wi_repo_inventory (
  work_item_id TEXT NOT NULL REFERENCES work_items(id),
  position     INTEGER NOT NULL,
  name         TEXT NOT NULL,
  url          TEXT NOT NULL,
  subdir       TEXT,
  base         TEXT,
  PRIMARY KEY (work_item_id, position)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_inventory_name ON wi_repo_inventory(work_item_id, name);
