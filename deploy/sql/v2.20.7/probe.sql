-- deploy/sql/v2.20.7/probe.sql — structural applied-check.
-- 0 = already applied (both delivered_seq and read_seq exist), nonzero = apply.
SELECT CASE WHEN (SELECT COUNT(*) FROM pragma_table_info('conversation_members')
                  WHERE name IN ('delivered_seq','read_seq')) = 2 THEN 0 ELSE 1 END;
