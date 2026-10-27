-- deploy/sql/v2.20.7/rollback.sql — the automatic-rollback counterpart.
-- Restores the old single cursor from the delivery position (read state is
-- dropped): an old daemon then resumes delivery at the position its
-- replacement had reached and never replays delivered messages. Delete this
-- file with the release window's other rollback files.
ALTER TABLE conversation_members ADD COLUMN last_read_seq INTEGER NOT NULL DEFAULT 0;
UPDATE conversation_members SET last_read_seq = delivered_seq;
ALTER TABLE conversation_members DROP COLUMN delivered_seq;
ALTER TABLE conversation_members DROP COLUMN read_seq;
