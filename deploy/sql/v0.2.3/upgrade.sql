-- deploy/sql/v0.2.3/upgrade.sql — the memory writer columns
-- (polyforge#107): the writer attribution every memory read face serves —
-- writer_kind (the daemon-stamped writer class, 'session' or 'step') and
-- writer_details (the writer's JSON detail object). Purely additive on the
-- v0.2.2 shape: both columns are nullable and no pre-existing row is
-- rewritten, except the one-time backfill below — rows written by a step
-- dispatch carry the daemon's idempotency key
-- pf-step-memory:<wi>:<step>:<attempt>, the only reliable provenance the
-- old database holds, so exactly those rows are marked as step writes
-- with the wi / step / attempt parsed out of the key (owner ruling Q2);
-- every other row keeps NULL on both columns. The v0.2.2 binary names
-- its columns on every read and write, so it keeps working on the
-- migrated database unchanged (Q7's premise).
ALTER TABLE memories ADD COLUMN writer_kind TEXT CHECK (writer_kind IN ('session','step'));
ALTER TABLE memories ADD COLUMN writer_details TEXT;
UPDATE memories AS m SET writer_kind = 'step',
    writer_details = json_object(
        'work_item', s.wi, 'step', s.step, 'attempt', s.attempt)
  FROM (SELECT id,
          substr(k, 1, instr(k, ':') - 1) AS wi,
          substr(substr(k, instr(k, ':') + 1), 1,
                 instr(substr(k, instr(k, ':') + 1), ':') - 1) AS step,
          CAST(substr(k,
                 instr(k, ':') + instr(substr(k, instr(k, ':') + 1), ':') + 1)
               AS INTEGER) AS attempt
          FROM (SELECT id, substr(idempotency_key, 16) AS k
                  FROM memories
                 WHERE idempotency_key LIKE 'pf-step-memory:%') AS keyrows
         WHERE instr(k, ':') > 0
           AND instr(substr(k, instr(k, ':') + 1), ':') > 0) AS s
 WHERE m.id = s.id;
