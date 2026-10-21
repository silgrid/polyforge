-- deploy/sql/v2.4.0/probe.sql — the structural applied-check of
-- upgrade.sql (polyforge#149).
--
-- deploy.sh executes this read-only against the live database BEFORE the
-- switch window. The FINAL statement answers exactly one value:
--   0        the upgrade already ran — skip this version (run-once)
--   nonzero  dead shapes are still present — apply upgrade.sql
-- No bookkeeping table is written: the probe is purely structural.
-- (After rollback.sql ran, the dead shapes are back and the probe answers
-- nonzero again — a retry upgrade is allowed.)
SELECT (SELECT COUNT(*) FROM pragma_table_info('projects')
        WHERE name IN ('scenario','identifier_hash'))
     + (SELECT COUNT(*) FROM pragma_table_info('work_items')
        WHERE name IN ('bundle_digest','workflow_budget_baseline','session_owner'))
     + (SELECT COUNT(*) FROM sqlite_master
        WHERE type='table' AND name IN ('wi_ship_ledger','core_meta'));
