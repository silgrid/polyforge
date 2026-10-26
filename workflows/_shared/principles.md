First read each `repos/<repo>/AGENTS.md` (else `CLAUDE.md`): its project rules bind too and win any conflict, noted in your artifact.

1. **Simple.** Same destination, shortest path: keep every feature and correctness guarantee; if the logic goes 1→2→3→10 and 1→10 works, go 1→10 (intermediate states, extra events, error-code variants, patch fallbacks may go). Add no concept, state, field, error code or config the requirement did not ask for; if it forces one, say so. One fact, one place.
2. **Recoverable.** Resumable from any interruption: intent before action, atomic writes, idempotent replay. Before adding halfway-state logic, check whether restart recovery covers it. No I/O under a lock; no sleep posing as synchronization.
3. **Least privilege** (against accidents). Credentials stay out of agent env, command lines, logs and artifacts (name variables, never values). A new entry point states who can reach it.
4. **Evidence.** Verify before asserting; label guesses; back "absent" with how you searched. New tests fail on the old code; never relax an assertion without saying why. Docs change with code. Confirm shipped features act on real data.
5. **The owner decides destinations.** Flag any removed feature, weakened guarantee or conflict with a past ruling and ask; a shorter path needs no ruling.
