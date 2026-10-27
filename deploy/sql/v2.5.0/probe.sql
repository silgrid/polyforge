-- deploy/sql/v2.5.0/probe.sql — the structural applied-check of
-- upgrade.sql (polyforge#157).
--
-- deploy.sh executes this read-only against the live database BEFORE the
-- switch window. The FINAL statement answers exactly one value:
--   0        the upgrade already ran — skip this version (run-once)
--   nonzero  the old shape is still present (the wi_repo_inventory table,
--            or work_items without repo_inventory) — apply upgrade.sql
-- No bookkeeping table is written: the probe is purely structural.
SELECT (SELECT COUNT(*) FROM sqlite_master
        WHERE type='table' AND name='wi_repo_inventory')
     + (1 - (SELECT COUNT(*) FROM pragma_table_info('work_items')
             WHERE name='repo_inventory'));
