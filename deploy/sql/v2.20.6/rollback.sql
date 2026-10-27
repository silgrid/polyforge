-- deploy/sql/v2.20.6/rollback.sql — the automatic-rollback counterpart.
-- Drops the child tables first, then the additive machine columns.
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversation_members;
DROP TABLE IF EXISTS conversations;
ALTER TABLE machines DROP COLUMN capability;
ALTER TABLE machines DROP COLUMN superseded_at;
ALTER TABLE machines DROP COLUMN operator_id;
ALTER TABLE machines DROP COLUMN role;
ALTER TABLE machines DROP COLUMN launcher;
ALTER TABLE machines DROP COLUMN public_key;
