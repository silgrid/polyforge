import "#test-env";
import test from "node:test";
import assert from "node:assert/strict";
import { createJiti } from "jiti";
const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const { modelReferenceImpact, providerReferenceImpact } = await jiti.import("./model-reference-impact.ts");

test("removal names the saved default, all tier candidates and real subagent models without rewriting them", async () => {
  const candidates = ["pi:gateway/one:high", "claude:one:max", "pi:other/one:low", "pi:missing/two"];
  const read = async (url) => {
    if (url.startsWith("/api/harness/pi/model")) return { data: { defaultProvider: "gateway", defaultModel: "one" } };
    if (url.startsWith("/api/polyforge/config")) return { pfPresent: false, items: [{ key: "step_tiers.fast", value: candidates }] };
    if (url.startsWith("/api/subagents")) return { profiles: [{ name: "review", scope: "global", model: "gateway/one:high" }, { name: "inherits", scope: "project" }] };
    return { cells: [] };
  };
  const references = await modelReferenceImpact(["gateway/one"], "/fixture", read);
  assert.deepEqual(references, ["default · pi:gateway/one", "Host · fast · pi:gateway/one:high", "subagent · global/review · gateway/one:high"]);
  assert.deepEqual(candidates, ["pi:gateway/one:high", "claude:one:max", "pi:other/one:low", "pi:missing/two"]);
});

test("Claude identities match only Claude defaults and candidates", async () => {
  const read = async (url) => {
    if (url.startsWith("/api/harness/claude/model")) return { data: { defaultModel: "custom" } };
    if (url.startsWith("/api/polyforge/config")) return { pfPresent: false, items: [{ key: "step_tiers.fast", value: ["claude:custom:high", "pi:gateway/custom"] }] };
    return { cells: [] };
  };
  assert.deepEqual(await modelReferenceImpact(["custom"], null, read, "claude"), ["default · claude:custom", "Host · fast · claude:custom:high"]);
});

test("provider disconnect checks retired models in profiles and workspace chains outside the catalog", async () => {
  const read = async (url) => {
    if (url.startsWith("/api/harness")) return { data: {} };
    if (url.startsWith("/api/models?")) return { modelList: [] };
    if (url.startsWith("/api/polyforge/config")) return { pfPresent: true, items: [] };
    if (url.startsWith("/api/settings/account")) return { cells: [{ profile: "beta" }] };
    if (url.startsWith("/api/subagents")) return { profiles: [{ name: "old", scope: "global", model: "gateway/retired" }] };
    return { workspaces: [{ workspace: "/fixture", declared: { yaml: { path: "/fixture/.polyforge.yaml", sha256: "h" }, steps: { product: { task: { declared: true, models: ["pi:gateway/retired:high"] } } } }, workflows: { workspace: "/fixture", projects: ["product"], bundles: [], task: {} } }] };
  };
  assert.deepEqual(await providerReferenceImpact("gateway", "/fixture", read), ["subagent · global/old · gateway/retired", "Cell beta · product/task · pi:gateway/retired:high"]);
});

test("missing project refuses destructive pi checks without assuming global profiles are empty", async () => {
  let reads = 0;
  const read = async () => { reads++; return {}; };
  await assert.rejects(modelReferenceImpact(["gateway/one"], null, read), /model-reference-project-required/);
  await assert.rejects(providerReferenceImpact("gateway", null, read), /model-reference-project-required/);
  assert.equal(reads, 0);
});

test("failed reference reads reject instead of authorizing deletion", async () => {
  await assert.rejects(modelReferenceImpact(["gateway/one"], "/fixture", async () => { throw new Error("fixture unavailable"); }), /fixture unavailable/);
});

test("workspace references retain Cell/project/step identity and actual effective chains", async () => {
  const read = async (url) => {
    if (url.startsWith("/api/harness")) return { data: {} };
    if (url.startsWith("/api/polyforge/config")) return { pfPresent: true, items: [] };
    if (url.startsWith("/api/settings/account")) return { cells: [{ profile: "beta" }] };
    if (url.startsWith("/api/subagents")) return { profiles: [] };
    return { workspaces: [{ workspace: "/fixture", declared: { yaml: { path: "/fixture/.polyforge.yaml", sha256: "h" }, steps: { product: { task: { declared: true, models: ["pi:gateway/one:high"] } } } }, workflows: { workspace: "/fixture", projects: ["product"], bundles: [], task: {} } }] };
  };
  assert.deepEqual(await modelReferenceImpact(["gateway/one"], "/fixture", read), ["Cell beta · product/task · pi:gateway/one:high"]);
});
