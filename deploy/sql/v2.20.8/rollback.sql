-- deploy/sql/v2.20.8/rollback.sql — the automatic-rollback counterpart.
-- The v2.20.6 binary selects and INSERTs machines.user_id NOT NULL, so the
-- column comes back NOT NULL and is filled from operator_id, the one
-- machine identity that survives. The heartbeat-minted users the upgrade
-- deleted are deliberately NOT resurrected: the column is a plain id
-- string the old binary never joins on, and it mints a fresh user on its
-- next registration. No REFERENCES clause on purpose — the repo's Open
-- turns foreign_keys ON and the old binary never relies on that relation.
ALTER TABLE machines ADD COLUMN user_id TEXT NOT NULL DEFAULT '';
UPDATE machines SET user_id = COALESCE(operator_id, '');
