-- deploy/sql/v2.14.0/probe.sql — the structural applied-check of
-- upgrade.sql (polyforge#194).
--
-- deploy.sh executes this read-only against the live database BEFORE the
-- switch window. The FINAL statement answers exactly one value:
--   0        the upgrade already ran — skip this version (run-once)
--   nonzero  memories.fs_mtime_ns / fs_size or claim_idempotency are
--            still present — apply upgrade.sql
-- No bookkeeping table is written: the probe is purely structural.
SELECT (SELECT COUNT(*) FROM pragma_table_info('memories')
        WHERE name IN ('fs_mtime_ns','fs_size'))
     + (SELECT COUNT(*) FROM sqlite_master
        WHERE type='table' AND name='claim_idempotency');
