-- deploy/sql/v2.14.0/upgrade.sql — memory database-only cleanup and the
-- claim idempotency cache (polyforge#194).
--
-- Applied by .polyforge/deploy.sh INSIDE the switch window as ONE SQLite
-- transaction (see docs/upgrade.md).
--
-- memories.fs_mtime_ns / fs_size were the file-authority store's
-- freshness stamp; no release since v2.13.0 reads them. claim_idempotency
-- was the claim replay cache; claims replay on the session secret and
-- nothing reads the table.
DROP TABLE IF EXISTS claim_idempotency;
ALTER TABLE memories DROP COLUMN fs_mtime_ns;
ALTER TABLE memories DROP COLUMN fs_size;
