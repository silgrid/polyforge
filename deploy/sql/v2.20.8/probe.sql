-- deploy/sql/v2.20.8/probe.sql — structural applied-check.
-- 0 = already applied (machines.user_id is gone), nonzero = apply.
-- The upgrade DROPS the marker column, so the count itself is the answer
-- (the inverse of the additive releases' `1 - count`).
SELECT (SELECT COUNT(*) FROM pragma_table_info('machines')
         WHERE name='user_id');
