-- deploy/sql/v2.17.0/upgrade.sql — the admin audit trail of project hard
-- deletes.
--
-- Applied by .polyforge/deploy.sh INSIDE the switch window as ONE SQLite
-- transaction (see docs/upgrade.md).
--
-- admin_audit records every performed project deletion: the actor (the
-- deleting admin's user id), the time, the deleted project's id and name,
-- and the deleted work-item ids as a JSON array (the startup replay's
-- repo-removal intent). It carries no FK to projects: the cascade of a
-- later delete must never touch it. No read route or verb exposes it.
CREATE TABLE IF NOT EXISTS admin_audit (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  actor        TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  project_id   TEXT NOT NULL,
  project_name TEXT NOT NULL,
  wi_ids       TEXT NOT NULL DEFAULT '[]'
);