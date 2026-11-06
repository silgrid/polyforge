import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const helpers = await jiti.import("./polyforge-config-helpers.ts");

test("items classify into source cards; an unknown key stays visible in the flat card", () => {
  const { configGroupOf } = helpers;
  for (const key of ["step_timeout", "stall_window", "harness_root", "operator_agent_dir", "python", "max_concurrent_jobs"]) {
    assert.equal(configGroupOf({ key }), "daemon", `${key} is a pf [daemon] key`);
  }
  for (const key of ["server", "api_key", "daemon_socket", "user_id", "claude_bin", "some_future_key"]) {
    assert.equal(configGroupOf({ key }), "flat", `${key} renders in the cli.toml card`);
  }
  assert.equal(configGroupOf({ key: "summarize.models" }), "summarize");
  // a named Cell's model.source renders in its file's [model] zone
  assert.equal(configGroupOf({ key: "model.source" }), "model");
  assert.deepEqual([...helpers.CONFIG_GROUPS], ["flat", "daemon", "model", "workspace", "summarize"]);
  assert.equal(configGroupOf({ key: '[workspace."/w"]', section: true }), "workspace");
});

test("groups follow the fixed card order, drop empty cards and keep pf's order inside a card", () => {
  const items = [
    { key: '[workspace."/a"]', section: true, value: "p1" },
    { key: "summarize.language" },
    { key: "stall_window" },
    { key: "server" },
    { key: "step_timeout" },
    { key: "api_key" },
  ];
  const groups = helpers.groupConfigItems(items);
  assert.deepEqual(groups.map((group) => group.id), ["flat", "daemon", "workspace", "summarize"]);
  assert.deepEqual(groups[0].items.map((item) => item.key), ["server", "api_key"]);
  assert.deepEqual(groups[1].items.map((item) => item.key), ["stall_window", "step_timeout"]);
  assert.deepEqual(helpers.groupConfigItems([{ key: "server" }]).map((group) => group.id), ["flat"]);
  assert.deepEqual(helpers.groupConfigItems([]), []);
});

test("a workspace section header parses to its path; a malformed one is null", () => {
  assert.equal(helpers.workspaceSectionPath('[workspace."/home/u/ws"]'), "/home/u/ws");
  assert.equal(helpers.workspaceSectionPath("[workspace./p]"), null);
  assert.equal(helpers.workspaceSectionPath("server"), null);
});

test("workspace projects split into chips; empty and non-string values give none", () => {
  assert.deepEqual(helpers.workspaceProjects("a,b"), ["a", "b"]);
  assert.deepEqual(helpers.workspaceProjects(" a , ,b "), ["a", "b"]);
  assert.deepEqual(helpers.workspaceProjects(""), []);
  assert.deepEqual(helpers.workspaceProjects(undefined), []);
});

test("unset values are null, undefined and the empty string only", () => {
  assert.equal(helpers.isUnsetValue(null), true);
  assert.equal(helpers.isUnsetValue(undefined), true);
  assert.equal(helpers.isUnsetValue(""), true);
  assert.equal(helpers.isUnsetValue(0), false);
  assert.equal(helpers.isUnsetValue(false), false);
  assert.equal(helpers.isUnsetValue("45m"), false);
});

test("the helper module is browser-safe: it imports nothing at runtime", async () => {
  const source = await readFile(new URL("./polyforge-config-helpers.ts", import.meta.url), "utf8");
  assert.doesNotMatch(source, /^\s*import\s+(?!type\b)/m, "no runtime import");
  assert.doesNotMatch(source, /\brequire\(/, "no require");
});

test("candidates group per project, sorted, with the project-less steps last as one group", () => {
  const steps = [
    { project: "zeta", step: "fix", declared: true, models: ["pi:zeta", "pi:alpha"] },
    { project: "alpha", step: "review", declared: false, models: null },
    { step: "plan", declared: true, models: ["m2", "m1"] },
    { project: "alpha", step: "code", declared: true, models: ["b", "a"] },
  ];
  const groups = helpers.groupStepsByProject(steps);
  assert.deepEqual(groups.map((group) => group.project), ["alpha", "zeta", null]);
  assert.deepEqual(groups[0].steps.map((step) => step.step), ["code", "review"], "steps sort by id");
  assert.deepEqual(groups[0].steps[0].models, ["b", "a"], "pf's model order is kept");
  assert.deepEqual(groups[1].steps[0].models, ["pi:zeta", "pi:alpha"]);
  assert.equal(groups[0].steps[1].declared, false);
  assert.equal(groups[0].steps[1].models, null, "an undeclared step carries no models");
  assert.deepEqual(steps.map((step) => step.step), ["fix", "review", "plan", "code"], "the input is not mutated");
  assert.deepEqual(helpers.groupStepsByProject([]), []);
});

test("one workspace path's memory_policy entries list default first, then the projects sorted", () => {
  assert.deepEqual(helpers.policyEntryNames({ zeta: {}, default: {}, alpha: {} }), ["default", "alpha", "zeta"]);
  assert.deepEqual(helpers.policyEntryNames({ beta: {} }), ["beta"], "a path without a default block keeps its projects");
});

test("the Add-Cell draft is checked like the server checks it", () => {
  const { cellAddDraftError } = helpers;
  const ok = { profile: "alpha", server: "https://hub.example", apiKey: "k", modelSource: "" };
  assert.equal(cellAddDraftError(ok), null);
  assert.equal(cellAddDraftError({ ...ok, modelSource: "shared-default" }), null);
  assert.equal(cellAddDraftError({ ...ok, modelSource: "/creds" }), null);
  assert.equal(cellAddDraftError({ ...ok, profile: "Alpha" }), "polyforge.cells.errProfile");
  assert.equal(cellAddDraftError({ ...ok, profile: "default" }), "polyforge.cells.errReserved");
  assert.equal(cellAddDraftError({ ...ok, server: "hub" }), "polyforge.cells.errServer");
  assert.equal(cellAddDraftError({ ...ok, apiKey: "" }), "polyforge.cells.errApiKey");
  assert.equal(cellAddDraftError({ ...ok, apiKey: "a b" }), "polyforge.cells.errApiKey");
  assert.equal(cellAddDraftError({ ...ok, modelSource: "rel/dir" }), "polyforge.cells.errModelSource");
});

test("the Host region reads and writes through the Host scope; api_key is never shown", () => {
  // the Host is the one layered region left (a Cell's keys are its file card)
  assert.equal(helpers.HOST_REGION_READ_URL, "/api/polyforge/config?host=1");
  assert.deepEqual(helpers.hostRegionWriteBody("set", "daemon_socket", "/run/pf.sock", "sha"),
    { action: "set", key: "daemon_socket", value: "/run/pf.sock", host: true, expect_state: { file_sha256: "sha" } });
  assert.deepEqual(helpers.hostRegionWriteBody("unset", "harness_root", "ignored", null),
    { action: "unset", key: "harness_root", host: true, expect_state: { file_sha256: null } }, "an unset carries no value");
  assert.equal(helpers.regionShownValue({ key: "api_key", value: "x", set: true, source: "file" }), "", "api_key is never shown");
  assert.equal(helpers.regionShownValue({ key: "step_timeout", value: "45m", source: "file" }), "45m");
  assert.equal(helpers.regionShownValue({ key: "step_timeout", value: "30m", source: "default" }), "", "a default is not the file's value");
  assert.equal(helpers.regionShownValue(undefined), "");
});
