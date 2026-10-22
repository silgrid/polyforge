-- deploy/sql/v2.17.0/probe.sql — the structural applied-check of
-- upgrade.sql.
--
-- deploy.sh executes this read-only against the live database BEFORE the
-- switch window. The FINAL statement answers exactly one value:
--   0        the upgrade already ran — skip this version (run-once)
--   nonzero  admin_audit is still absent — apply upgrade.sql
-- No bookkeeping table is written: the probe is purely structural.
SELECT 1 - (SELECT COUNT(*) FROM sqlite_master
        WHERE type='table' AND name='admin_audit');