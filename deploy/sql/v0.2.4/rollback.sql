-- deploy/sql/v0.2.4/rollback.sql — the automatic-rollback counterpart
-- of upgrade.sql (the split attrs migration, polyforge#174).
--
-- deploy.sh runs this ONLY for the version applied in the same deploy
-- run, as ONE SQLite transaction, BEFORE the old image is recreated
-- (the window is stopped: no write races the restore). It restores the
-- pre-migration attrs of EXACTLY the rows this migration changed —
-- read from the before-image table upgrade.sql wrote — and never
-- flattens a row the migration did not move. A database the upgrade
-- never touched (a fresh one, a no-op deploy) carries no before-image
-- table: the defensive empty one restores nothing. Data restore
-- outside the window (a later manual rollback) stays a person's own
-- stop-the-service operation; the before-image table is kept by the
-- upgrade until this rollback drops it.
CREATE TABLE IF NOT EXISTS split_attrs_before_w174 (
  id    TEXT PRIMARY KEY,
  attrs TEXT NOT NULL
);
UPDATE work_items
   SET attrs = (SELECT b.attrs FROM split_attrs_before_w174 b
                WHERE b.id = work_items.id)
 WHERE id IN (SELECT id FROM split_attrs_before_w174);
DROP TABLE split_attrs_before_w174;

-- 4. the generic lease base (polyforge#245) is this window's, not a
--    durable provenance table: the rollback drops it so the old version
--    starts on the pre-window shape (drain every lock cycle first; an
--    old daemon never acquires).
DROP TABLE IF EXISTS leases;
DROP TABLE IF EXISTS lease_waiters;
DROP TABLE IF EXISTS repo_aliases;
