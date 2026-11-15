-- deploy/sql/v0.2.1/probe.sql — structural applied-check of upgrade.sql
-- (the project deactivation status, polyforge#30).
--
-- deploy.sh executes this read-only against the live database BEFORE the
-- switch window. The FINAL statement answers exactly one value:
--   0        the upgrade already ran — skip this version (run-once)
--   nonzero  the v0.2.0 shape is still present (projects without status)
--            — apply upgrade.sql
-- No bookkeeping table is written: the probe is purely structural.
SELECT 1 - (SELECT COUNT(*) FROM pragma_table_info('projects')
             WHERE name='status');
