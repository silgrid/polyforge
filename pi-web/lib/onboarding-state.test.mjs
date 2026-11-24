// carries: lib/onboarding-state.ts
// polyforge#167 (91-onboarding) AC1: the versioned completion state of the
// four onboarding steps and #162's first-boot record projection. Expected
// values come from the ticket's acceptance ("完成或跳过以后不再弹出", "跳过的
// 步骤标为「未完成」", "状态 API 不返回任何凭据值") and docs/workstation.md's
// published first-boot field list, never from this implementation's output.
import "#test-env";
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { interopDefault: true });
const state = await jiti.import("./onboarding-state.ts");

const { ONBOARDING_STEPS } = state;

const empty = () => ({
  version: 1,
  steps: { aihub: false, account: false, workspace: false, ticket: false },
  dismissed: false,
});

test("the four steps are the ticket's order, and the empty state is four unfinished steps", () => {
  assert.deepEqual([...ONBOARDING_STEPS], ["aihub", "account", "workspace", "ticket"]);
  assert.deepEqual(state.emptyOnboardingState(), empty());
  assert.equal(state.nextOnboardingStep(state.emptyOnboardingState()), "aihub");
});

test("the merge keeps only true known steps and rewrites an unknown version to the current one", () => {
  // A future version's shape: known steps survive, an unknown step id is not
  // materialized, a non-boolean value is incomplete (Q2: never clear a known
  // record, mark unknown steps 「未完成」).
  const merged = state.mergeOnboardingState({
    version: 99,
    steps: { aihub: true, account: "yes", workspace: false, ticket: true, futureStep: true },
    dismissed: true,
    extra: { credential: "SENTINEL" },
  });
  assert.deepEqual(merged, {
    version: 1,
    steps: { aihub: true, account: false, workspace: false, ticket: true },
    dismissed: true,
  });
  assert.equal("futureStep" in merged.steps, false, "an unknown step is not materialized");
  assert.equal("extra" in merged, false, "an undeclared top-level field is dropped, not carried");
});

test("a missing, torn, array or scalar record reads as the empty state", () => {
  for (const raw of [null, undefined, "x", 7, [], [{ aihub: true }], { steps: [] }, { steps: "aihub" }]) {
    assert.deepEqual(state.mergeOnboardingState(raw), empty(), JSON.stringify(raw));
  }
  assert.deepEqual(state.mergeOnboardingState({ version: 1, steps: { aihub: true } }), {
    ...empty(),
    steps: { ...empty().steps, aihub: true },
  }, "a missing dismissed key is false");
});

test("next step is the first unfinished one; with all four finished it reviews from the first (Q6)", () => {
  const base = state.emptyOnboardingState();
  assert.equal(state.nextOnboardingStep(state.completeOnboardingStep(base, "aihub")), "account");
  assert.equal(state.nextOnboardingStep(state.completeOnboardingStep(state.completeOnboardingStep(base, "aihub"), "account")), "workspace");
  let all = base;
  for (const step of ONBOARDING_STEPS) all = state.completeOnboardingStep(all, step);
  assert.equal(state.nextOnboardingStep(all), "aihub", "reopening after completion starts at the first step, reviewable");
  assert.equal(state.followingOnboardingStep("ticket"), null);
});

test("skipping records no completion, and dismiss/reopen are the popup switch", () => {
  const base = state.emptyOnboardingState();
  assert.deepEqual(state.skipOnboardingStep(base), base, "a skipped step stays 「未完成」");
  assert.deepEqual(state.onboardingStepStatuses(base), {
    aihub: "incomplete", account: "incomplete", workspace: "incomplete", ticket: "incomplete",
  });
  assert.equal(state.dismissOnboarding(base).dismissed, true);
  assert.equal(state.reopenOnboarding(state.dismissOnboarding(base)).dismissed, false);
  assert.equal(state.reopenOnboarding(base).dismissed, false, "reopening an open wizard is idempotent");
});

test("the first-boot projection keeps the published fields and drops every credential or undeclared key", () => {
  const record = {
    phase: "done",
    mode: "external",
    defaultCell: "default",
    server: "https://aihub.example.test:8420",
    project: "polyforge",
    userReady: true,
    keyReady: true,
    error: "pf: token=SECRET-ERROR-SENTINEL",
    apiKey: "SECRET-API-KEY-SENTINEL",
    key: "SECRET-KEY-SENTINEL",
    access_token: "SECRET-ACCESS-TOKEN-SENTINEL",
    updatedAt: "2026-11-23T00:00:00Z",
  };
  const connection = state.projectFirstBootRecord(record);
  assert.deepEqual(connection, {
    phase: "done",
    mode: "external",
    defaultCell: "default",
    server: "https://aihub.example.test:8420",
    project: "polyforge",
    userReady: true,
    keyReady: true,
  });
  const serialized = JSON.stringify(connection);
  for (const sentinel of ["SECRET-ERROR-SENTINEL", "SECRET-API-KEY-SENTINEL", "SECRET-KEY-SENTINEL", "SECRET-ACCESS-TOKEN-SENTINEL", "updatedAt"]) {
    assert.equal(serialized.includes(sentinel), false, `the projection leaked ${sentinel}`);
  }
});

test("the projection's closed vocabularies drop values they do not own", () => {
  assert.deepEqual(state.projectFirstBootRecord({ mode: "local", server: "file:///etc/passwd", phase: "identity/failed" }), {
    phase: "identity/failed",
    mode: "local",
    defaultCell: null,
    server: null,
    project: null,
    userReady: false,
    keyReady: false,
  });
  assert.equal(state.projectFirstBootRecord({ mode: "unknown" }), null, "not a record: no phase, no known mode");
  assert.equal(state.projectFirstBootRecord({ server: "https://x.test" }), null, "a bare server is not a first-boot record");
  assert.equal(state.projectFirstBootRecord(null), null);
  assert.equal(state.projectFirstBootRecord("done"), null);
});

test("the connection is read from the data dir's published first-boot path and degrades to null", () => {
  const work = mkdtempSync(join(tmpdir(), "pf-onboarding-state-connection-"));
  test.after(() => rmSync(work, { recursive: true, force: true }));
  assert.equal(state.readFirstBootConnection(work), null, "absent record reads null");
  const dir = join(work, "workstation");
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "first-boot.json"), JSON.stringify({ phase: "done", mode: "local", defaultCell: "default", userReady: true, keyReady: true }));
  assert.deepEqual(state.readFirstBootConnection(work), {
    phase: "done",
    mode: "local",
    defaultCell: "default",
    server: null,
    project: null,
    userReady: true,
    keyReady: true,
  });
  writeFileSync(join(dir, "first-boot.json"), "{not json");
  assert.equal(state.readFirstBootConnection(work), null, "a torn record reads null, never throws");
});

test("one write merges per step, keeps the other step's record, and writes 0600", async () => {
  const agentDir = mkdtempSync(join(tmpdir(), "pf-onboarding-state-write-"));
  test.after(() => rmSync(agentDir, { recursive: true, force: true }));
  assert.deepEqual(state.readOnboardingState(agentDir), empty(), "no file reads the empty state");

  await state.applyOnboardingAction("complete", "account", agentDir);
  assert.equal(state.readOnboardingState(agentDir).steps.account, true);
  await state.applyOnboardingAction("complete", "workspace", agentDir);
  const after = state.readOnboardingState(agentDir);
  assert.deepEqual(after.steps, { aihub: false, account: true, workspace: true, ticket: false }, "the second write kept the first step");
  const mode = statSync(join(agentDir, "pi-web-onboarding.json")).mode & 0o777;
  assert.equal(mode, 0o600, "the state file is private");

  await state.applyOnboardingAction("dismiss", null, agentDir);
  assert.equal(state.readOnboardingState(agentDir).dismissed, true);
  await state.applyOnboardingAction("reopen", null, agentDir);
  assert.equal(state.readOnboardingState(agentDir).dismissed, false);
});

test("concurrent step completions both survive: the write is a read-merge-write, not last-writer-wins", async () => {
  const agentDir = mkdtempSync(join(tmpdir(), "pf-onboarding-state-concurrent-"));
  test.after(() => rmSync(agentDir, { recursive: true, force: true }));
  await Promise.all([
    state.writeOnboardingState((current) => state.completeOnboardingStep(current, "aihub"), agentDir),
    state.writeOnboardingState((current) => state.completeOnboardingStep(current, "ticket"), agentDir),
  ]);
  const stored = state.readOnboardingState(agentDir);
  assert.equal(stored.steps.aihub, true, "one tab's step was lost");
  assert.equal(stored.steps.ticket, true, "one tab's step was lost");
});

test("a torn file is preserved as .corrupt before the first fresh write, never silently erased", async () => {
  const agentDir = mkdtempSync(join(tmpdir(), "pf-onboarding-state-corrupt-"));
  test.after(() => rmSync(agentDir, { recursive: true, force: true }));
  const path = join(agentDir, "pi-web-onboarding.json");
  writeFileSync(path, "{torn");
  assert.deepEqual(state.readOnboardingState(agentDir), empty(), "a torn file reads the empty state");
  await state.applyOnboardingAction("complete", "aihub", agentDir);
  assert.equal(readFileSync(`${path}.corrupt`, "utf8"), "{torn", "the unparsable record is kept beside the new one");
  assert.equal(state.readOnboardingState(agentDir).steps.aihub, true);
  assert.equal(existsSync(path), true);
});
