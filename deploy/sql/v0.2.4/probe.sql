-- deploy/sql/v0.2.4/probe.sql — structural applied-check of
-- upgrade.sql (the split attrs migration polyforge#174 AND the generic
-- lease base polyforge#245).
--
-- deploy.sh executes this read-only against the live database BEFORE
-- the switch window. The FINAL statement answers exactly one value:
--   0        the upgrade already ran (or the database never carried a
--            flat split key) — skip this version (run-once)
--   nonzero  rows still carry a flat `split.origin` / `split.unit`
--            key, or one of the window's lock tables is missing — apply
--            upgrade.sql
-- No bookkeeping table is written: the probe counts the old shapes
-- themselves, so a re-deploy after a rollback answers apply again. The
-- second half is what makes a database that already ran the OLD v0.2.4
-- (the split migrated, the lock schema never created) apply this one.
SELECT (SELECT COUNT(*) FROM work_items
         WHERE json_type(attrs, '$."split.origin"') IS NOT NULL
            OR json_type(attrs, '$."split.unit"') IS NOT NULL)
     + (3 - (SELECT COUNT(*) FROM sqlite_master
              WHERE type = 'table'
                AND name IN ('leases', 'lease_waiters', 'repo_aliases')));
