-- deploy/sql/v0.2.3/probe.sql — structural applied-check of upgrade.sql
-- (the memory writer columns, polyforge#107).
--
-- deploy.sh executes this read-only against the live database BEFORE the
-- switch window. The FINAL statement answers exactly one value:
--   0        the upgrade already ran — skip this version (run-once)
--   nonzero  the v0.2.2 shape is still present (memories without the
--            writer columns) — apply upgrade.sql
-- No bookkeeping table is written: the probe is purely structural.
SELECT 2 - (SELECT COUNT(*) FROM pragma_table_info('memories')
             WHERE name IN ('writer_kind','writer_details'));
