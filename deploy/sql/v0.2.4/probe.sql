-- deploy/sql/v0.2.4/probe.sql — structural applied-check of
-- upgrade.sql (the split attrs migration, polyforge#174).
--
-- deploy.sh executes this read-only against the live database BEFORE
-- the switch window. The FINAL statement answers exactly one value:
--   0        the upgrade already ran (or the database never carried a
--            flat split key) — skip this version (run-once)
--   nonzero  rows still carry a flat `split.origin` / `split.unit`
--            key — apply upgrade.sql
-- No bookkeeping table is written: the probe counts the old shape
-- itself, so a re-deploy after a rollback answers apply again.
SELECT COUNT(*) FROM work_items
 WHERE json_type(attrs, '$."split.origin"') IS NOT NULL
    OR json_type(attrs, '$."split.unit"') IS NOT NULL;
