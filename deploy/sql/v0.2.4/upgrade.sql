-- deploy/sql/v0.2.4/upgrade.sql — the split attrs migration
-- (polyforge#174): the zero-value stub of the red stage; the
-- implementation commit fills the one-time move of the flat
-- split.origin / split.unit keys to the nested dot paths.
SELECT 1;
