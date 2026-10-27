-- deploy/sql/v2.20.6/probe.sql — structural applied-check.
-- 0 = already applied (machines.public_key exists), nonzero = apply.
SELECT 1 - (SELECT COUNT(*) FROM pragma_table_info('machines')
            WHERE name='public_key');
