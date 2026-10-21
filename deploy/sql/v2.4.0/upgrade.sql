-- deploy/sql/v2.4.0/upgrade.sql — polyforge#149 (audit subtraction C).
--
-- Applied by .polyforge/deploy.sh INSIDE the seconds-level switch window
-- (old container exited, new container not yet started), as ONE SQLite
-- transaction: a failure rolls the whole script back and the deploy
-- auto-rolls-back to the old image.
--
-- The whole schema delta of polyforge#149: the delivery ledger, the dead
-- tables and columns are gone. The optional `waiting_on` cleanup is NOT
-- here — it stays optional and manual (docs/upgrade.md).
DROP TABLE IF EXISTS wi_ship_ledger;
DROP TABLE IF EXISTS core_meta;
ALTER TABLE projects DROP COLUMN scenario;
ALTER TABLE projects DROP COLUMN identifier_hash;
ALTER TABLE work_items DROP COLUMN bundle_digest;
ALTER TABLE work_items DROP COLUMN workflow_budget_baseline;
ALTER TABLE work_items DROP COLUMN session_owner;
