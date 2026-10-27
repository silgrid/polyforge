-- deploy/sql/v2.5.0/upgrade.sql — polyforge#157.
--
-- Applied by .polyforge/deploy.sh INSIDE the switch window as ONE SQLite
-- transaction (see docs/upgrade.md).
--
-- The per-WI repo inventory (02 §4.6) moves from its own table into one
-- JSON column on work_items ([{name}] of project repos, NULL = none). The
-- table held 0 rows in production, so nothing is carried over; its
-- unique index goes with it.
DROP TABLE IF EXISTS wi_repo_inventory;
ALTER TABLE work_items ADD COLUMN repo_inventory TEXT;
