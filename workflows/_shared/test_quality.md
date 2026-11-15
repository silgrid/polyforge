## What counts as a test

Adapted from spec-superflow's `skills/build-executor/writing-good-tests.md`
(MageByte-Zero/spec-superflow, commit fb5d4d5; MIT — Copyright (c) 2026 MageByte),
rewritten for this workflow's steps.

A test that counts pins an observable behaviour, and its author can say three things
about it:

1. **The behaviour it observes.** Which observable behaviour of the change the test
   exercises — what a caller, a user or an output sees — not which lines of the
   implementation it reaches.
2. **Expected values derived independently.** Every expected value comes from the
   requirement — the ticket, its acceptance, a specification, a hand-computed table —
   never copied from the implementation's own output.
3. **The change it catches.** One reasonable production-code change — a wrong branch,
   an off-by-one, a dropped case — that would turn the test red. A test no reasonable
   change can turn red pins nothing.

Three duties follow, for every test a step writes:

- **Text existence is not behaviour.** A test that only asserts that some source text
  exists (a regex over the file, a substring match) is not a behaviour test — unless the
  acceptance is itself that text or document, where the text is the behaviour.
- **A first red says why.** A test expected to run red states, before the run, which
  assertion should fail and why; a red from any other cause — a missing symbol, a typo,
  a compile error — is not the red that was meant.
- **Await the settled state before asserting it.** A state that settles asynchronously —
  the effect of a UI action, a streamed update, a background task — is asserted only
  after a bounded poll has reached the expected state the test names. Reading it once
  right after the action and asserting on that read is a race with the settling, and is
  `test_weakened`. Such a test is fixed by polling, never by relaxing the assertion or
  weakening an exact expectation to "at least".

Every unit of the plan carries the three statements above as its test-quality record, in
its `## Units` entry: the behaviour its test observes, why its red is expected, the change
the test catches. The steps that judge the change read that record.
