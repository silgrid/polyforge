-- deploy/sql/v0.2.1/upgrade.sql — the project deactivation status
-- (polyforge#30): one global per-project state, enforced server-side.
--
-- Applied by .polyforge/deploy.sh INSIDE the switch window as ONE SQLite
-- transaction (see docs/upgrade.md); the v0.2.1 tag is a public one, so
-- a v0.2.0 -> v0.2.1 deploy picks this directory automatically. Purely
-- additive: every existing project row answers 'active' — the only state
-- a pre-v0.2.1 database can hold — no row is rewritten and nothing is
-- lost. Deactivating a project is an API action afterwards, never a
-- migration default.
ALTER TABLE projects ADD COLUMN status TEXT NOT NULL DEFAULT 'active'
  CHECK (status IN ('active','deactivated'));
