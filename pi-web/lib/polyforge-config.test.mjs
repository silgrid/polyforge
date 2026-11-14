import "#test-env";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-config.ts");

test("the list shape gate requires the CAS hash and a class on every item", () => {
  assert.equal(lib.hasConfigListShape({ file: { sha256: "a" }, items: [{ key: "server", class: "restart-only" }] }), true);
  assert.equal(lib.hasConfigListShape({ items: [{ key: "server", class: "restart-only" }] }), false, "no file hash");
  assert.equal(lib.hasConfigListShape({ file: { sha256: "a" }, items: [{ key: "server", value: "x" }] }), false, "no class");
  assert.equal(lib.hasConfigListShape({ file: { sha256: "a" }, items: [] }), false);
});

test("api_key sanitizes to set/source/class only and never carries a value", () => {
  const item = lib.sanitizeConfigItem({ key: "api_key", value: "SENTINEL", set: true, source: "file", class: "restart-only", schema: { kind: "secret" } });
  assert.equal(item.value, undefined);
  assert.equal(item.set, true);
  assert.equal(item.class, "restart-only");
  assert.ok(!JSON.stringify(item).includes("SENTINEL"));
});

test("stale classification names the four plan reasons", () => {
  assert.equal(lib.classifyStaleReason("max_concurrent_jobs", 7, 6, { pending: ["max_concurrent_jobs"], overridden: [] }, 6), "pending restart");
  assert.equal(lib.classifyStaleReason("max_concurrent_jobs", 7, 6, { pending: [], overridden: ["max_concurrent_jobs"] }, 6), "daemon flag override");
  assert.equal(lib.classifyStaleReason("max_concurrent_jobs", 7, 2, { pending: [], overridden: [] }, 6), "pf ops max override");
  assert.equal(lib.classifyStaleReason("step_timeout", "45m0s", "30m0s", { pending: [], overridden: [] }, 6), "not reloaded");
  assert.equal(lib.classifyStaleReason("step_timeout", "30m0s", "30m0s", undefined, undefined), undefined);
});

test("the capability floor is the panel's own, above the dock floor", async () => {
  assert.equal(lib.POLYFORGE_CONFIG_PANEL_MINIMUM_PF_VERSION, "2.20.6");
  const detect = await jiti.import("./polyforge-detect.ts");
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.5", lib.POLYFORGE_CONFIG_PANEL_MINIMUM_PF_VERSION), true);
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.6", lib.POLYFORGE_CONFIG_PANEL_MINIMUM_PF_VERSION), false);
});

// the workspace read's new facts ride the config read.
const WORKSPACE_216 = {
  mapped: true, workspace: "/ws", yaml: { path: "/ws/.polyforge.yaml", sha256: "abc", exists: true },
  summarize: {},
  steps: {
    alpha: {
      // an older pf's attrs (removed them) are dropped
      grill: { declared: true, models: ["claude:x"], source: "file", class: "live", attrs: { question_batch_size: { value: 2, declared: true, source: "file", class: "live", default: 0, schema: { kind: "int" } } } },
      review: { declared: true, models: [], source: "file", class: "live" },
      default: { declared: false, models: null, source: "default", class: "live" },
      task: { declared: false, models: null, source: "default", class: "live" },
    },
  },
  steps_complete: true,
};

test("step entries carry source/class and the default/task rows", () => {
  const ws = lib.sanitizeWorkspace(WORKSPACE_216);
  const byId = Object.fromEntries(ws.steps.map((step) => [step.step, step]));
  assert.deepEqual(Object.keys(byId).sort(), ["default", "grill", "review", "task"]);
  assert.equal(byId.grill.source, "file");
  assert.equal(byId.grill.class, "live");
  assert.equal(byId.grill.attrs, undefined, "no attrs survive the sanitizer");
  assert.deepEqual(byId.review.models, [], "declared-empty stays an empty list");
  assert.equal(byId.review.declared, true);
  assert.equal(byId.default.declared, false);
  assert.equal(byId.default.models, null);
});

// summarize and memory_policy are `pf config list` facts. The
// wire is a real pf 2.20.21 recording (lib/__fixtures__/…/README.md).
const LIST_2_20_21 = JSON.parse(readFileSync(new URL("./__fixtures__/polyforge-config-list-2.20.21/list.json", import.meta.url), "utf8"));
const ALPHA = "/tmp/pi-web-fixture/ws-alpha";
const BETA = "/tmp/pi-web-fixture/ws-beta";

test("the recorded 2.20.21 list passes the shape gate and its summarize items keep value, source, class and schema", () => {
  assert.equal(lib.hasConfigListShape(LIST_2_20_21), true);
  const items = LIST_2_20_21.items.map(lib.sanitizeConfigItem).filter(Boolean);
  const { items: rows, reason } = lib.withListSummarize(items, "2.20.21");
  assert.equal(reason, undefined, "at the floor no hint");
  const byKey = Object.fromEntries(rows.map((row) => [row.key, row]));
  assert.deepEqual(byKey["summarize.models"], { key: "summarize.models", value: ["pi:fixture/summarize-a", "pi:fixture/summarize-b"], source: "file", class: "live", schema: { kind: "model-list" } });
  assert.deepEqual(byKey["summarize.language"], { key: "summarize.language", value: "Chinese", source: "file", class: "live", schema: { kind: "string" } });
  assert.deepEqual(byKey["summarize.interval_sec"], { key: "summarize.interval_sec", value: 120, source: "file", class: "live", schema: { kind: "int", max: 3600, min: 10 } });
  assert.equal(rows.length, items.length, "every list item survives at the floor");
});

test("memory_policy is read per workspace path from the recorded list: default's effective lists, each project's own", () => {
  const policy = lib.sanitizeMemoryPolicy(LIST_2_20_21.memory_policy);
  assert.deepEqual(Object.keys(policy).sort(), [ALPHA, BETA]);
  assert.deepEqual(policy[ALPHA].default, { record: ["pitfalls and their root cause", "the owner's rulings"], skip: ["transient state"], declared: true });
  assert.deepEqual(policy[ALPHA].alpha, { record: [], skip: ["alpha scratch notes"], declared: true }, "a project's own lists, never merged with default's");
  assert.deepEqual(policy[ALPHA].gamma, { record: ["gamma release facts"], skip: [], declared: true });
  assert.equal(policy[BETA].default.record.length, 3, "an undeclared default list is the built-in default pf reports");
  assert.deepEqual(policy[BETA].beta, { record: [], skip: [], declared: false });
  assert.deepEqual(lib.sanitizeMemoryPolicy({}), {}, "an empty object stays empty: no placeholder entries");
  assert.deepEqual(lib.sanitizeMemoryPolicy({ "/ws": { alpha: "drift" }, "": { default: {} } }), { "/ws": {} }, "malformed entries are dropped");
  assert.equal(lib.sanitizeMemoryPolicy(undefined), null, "an older pf reports no memory_policy");
});

test("the workspace read carries neither summarize nor memory_policy, even from a drifted document", () => {
  const ws = lib.sanitizeWorkspace({
    ...WORKSPACE_216,
    summarize: { language: { value: "Japanese", source: "file", class: "live" } },
    memory_policy: { default: { record: ["yaml"], skip: [], declared: true }, projects: {} },
  });
  assert.deepEqual(Object.keys(ws).sort(), ["mapped", "steps", "steps_complete", "workspaceFile"]);
  assert.ok(!JSON.stringify(ws).includes("Japanese"));
  assert.ok(!JSON.stringify(ws).includes("yaml\""));
});

test("below the 2.20.21 summarize floor — 2.20.20, null, garbage — the summarize rows are dropped with the upgrade hint", () => {
  assert.equal(lib.POLYFORGE_CONFIG_SUMMARIZE_MINIMUM_PF_VERSION, "2.20.21");
  const items = LIST_2_20_21.items.map(lib.sanitizeConfigItem).filter(Boolean);
  const nonSummarize = items.filter((row) => !row.key.startsWith("summarize.")).map((row) => row.key);
  for (const [version, found] of [["2.20.20", "found 2\\.20\\.20"], [null, "found an unparseable version"], ["not-a-version", "found not-a-version"]]) {
    const below = lib.withListSummarize(items, version);
    assert.deepEqual(below.items.map((row) => row.key), nonSummarize, `no summarize row for ${version}`);
    assert.match(below.reason, /need pf 2\.20\.21 or newer/);
    assert.match(below.reason, new RegExp(found));
  }
  const missing = lib.withListSummarize(items.filter((row) => !row.key.startsWith("summarize.")), "2.21.0");
  assert.ok(missing.reason, "a list without summarize items shows the hint, never a fallback");
});
