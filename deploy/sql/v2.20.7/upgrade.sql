-- deploy/sql/v2.20.7/upgrade.sql — workbench#56 delivery/read cursor split.
--
-- Applied by .polyforge/deploy.sh INSIDE the switch window as ONE SQLite
-- transaction (see docs/upgrade.md). conversation_members loses its single
-- last_read_seq and gains two independent cursors: delivered_seq (advanced
-- by machine delivery) and read_seq (advanced only by a human
-- acknowledgment). Existing members are backfilled from the old cursor: what
-- a human had read stays read, and delivery starts at the same position.
ALTER TABLE conversation_members ADD COLUMN delivered_seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE conversation_members ADD COLUMN read_seq INTEGER NOT NULL DEFAULT 0;
UPDATE conversation_members SET delivered_seq = last_read_seq, read_seq = last_read_seq;
ALTER TABLE conversation_members DROP COLUMN last_read_seq;
