// carries: lib/mcp-unified.ts, lib/harness/concepts.ts, lib/harness/pi.ts, lib/harness/claude.ts
// polyforge#144 (#85 85c, AC1/AC3, Q2 A / Q8 A): the merge of both sides'
// entries into one unified list — one row per (unified scope, name), the
// missing harnesses named, differences on the shared fields only, the
// same name's other scopes hinted — over a generic core 85d reuses as is,
// plus the adapters' mcp capability declarations (#85 c2: scopes, transports,
// cliAdd, enableToggle, toolFilter; Q1 A claude has no enable toggle, Q3 A
// the group switch is pi's alone).
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { moduleCache: false });
const { mergeUnifiedEntries, unifiedMcpRows, unifiedMcpPiFields, unifiedMcpClaudeFields } = await jiti.import("./mcp-unified.ts");
const { createPiAdapter } = await jiti.import("./harness/pi.ts");
const { createClaudeAdapter } = await jiti.import("./harness/claude.ts");

// ---------------------------------------------------------------------------
// The generic core (Q8 A): harnesses and fields of nothing to do with MCP.

const CORE_HARNESSES = [
  { harness: "alpha", scopes: ["global", "project"] },
  { harness: "beta", scopes: ["global", "project", "local"] },
];

test("one row per (scope, name), each harness's entry kept, the missing named", () => {
  const rows = mergeUnifiedEntries(CORE_HARNESSES, [
    { harness: "alpha", scope: "global", name: "shared", fields: { color: "red" } },
    { harness: "beta", scope: "global", name: "shared", fields: { color: "red" } },
    { harness: "beta", scope: "global", name: "beta-only", fields: { color: "blue" } },
    { harness: "beta", scope: "local", name: "claude-only-shape", fields: {} },
  ]);
  const shared = rows.find((row) => row.name === "shared");
  assert.equal(shared.scope, "global");
  assert.equal(shared.entries.alpha !== undefined, true);
  assert.equal(shared.entries.beta !== undefined, true);
  assert.deepEqual(shared.missing, []);
  assert.deepEqual(shared.inconsistent, []);
  const betaOnly = rows.find((row) => row.name === "beta-only");
  assert.deepEqual(betaOnly.entries.alpha, undefined);
  assert.deepEqual(betaOnly.missing, ["alpha"]);
  // alpha supports no local scope, so a local row misses no one.
  const local = rows.find((row) => row.scope === "local");
  assert.deepEqual(local.missing, []);
});

test("a difference is a shared field's difference only — one side's own field never counts", () => {
  const rows = mergeUnifiedEntries(CORE_HARNESSES, [
    // pi's `exposure` and claude's `type` are each one side's own: risk 8 —
    // counting them would show every row inconsistent for good.
    { harness: "alpha", scope: "project", name: "a", fields: { command: "npx", args: ["-y", "one"], exposure: "direct" } },
    { harness: "beta", scope: "project", name: "a", fields: { command: "npx", args: ["-y", "one"], type: "http" } },
    { harness: "alpha", scope: "project", name: "b", fields: { command: "npx", args: ["-y", "one"] } },
    { harness: "beta", scope: "project", name: "b", fields: { command: "npx", args: ["-y", "two"] } },
  ]);
  const same = rows.find((row) => row.name === "a");
  assert.deepEqual(same.inconsistent, [], "fields only one side defines are not differences");
  const diff = rows.find((row) => row.name === "b");
  assert.deepEqual(diff.inconsistent, [{ field: "args", values: { alpha: ["-y", "one"], beta: ["-y", "two"] } }]);
});

test("the same name in another scope is hinted, never merged away (Q2 A)", () => {
  const rows = mergeUnifiedEntries(CORE_HARNESSES, [
    { harness: "alpha", scope: "global", name: "dual", fields: {} },
    { harness: "alpha", scope: "project", name: "dual", fields: {} },
  ]);
  const global = rows.find((row) => row.scope === "global" && row.name === "dual");
  assert.deepEqual(global.otherScopes, ["project"]);
  assert.equal(rows.filter((row) => row.name === "dual").length, 2, "one row per scope");
});

// ---------------------------------------------------------------------------
// The MCP application.

function piServer(overrides = {}) {
  return { name: "demo", scope: "global", sourcePath: "/agent/mcp.json", enabled: true, validated: true, envNames: [], headerNames: [], usesOAuth: false, ...overrides };
}

function claudeServer(overrides = {}) {
  return { name: "demo", scope: "global", sourcePath: "/home/.claude.json", envNames: [], headerNames: [], ...overrides };
}

test("both sides' entries of one (scope, name) merge into one row that applies to all", () => {
  const rows = unifiedMcpRows(
    [piServer({ command: "npx", args: ["-y", "s"], envNames: ["A"] })],
    [claudeServer({ command: "npx", args: ["-y", "s"], envNames: ["A"] })],
  );
  assert.deepEqual(rows, [{
    scope: "global",
    name: "demo",
    pi: true,
    claude: true,
    missing: [],
    inconsistent: [],
    otherScopes: [],
  }]);
});

test("one side alone names the other missing; claude's local group is pi's nowhere", () => {
  const rows = unifiedMcpRows(
    [piServer({ name: "pi-own", scope: "project" })],
    [claudeServer({ name: "claude-own", scope: "global" }), claudeServer({ name: "local-one", scope: "local" })],
  );
  const piOwn = rows.find((row) => row.name === "pi-own");
  assert.equal(piOwn.pi, true);
  assert.equal(piOwn.claude, false);
  assert.deepEqual(piOwn.missing, ["claude"]);
  const claudeOwn = rows.find((row) => row.name === "claude-own");
  assert.deepEqual(claudeOwn.missing, ["pi"]);
  const local = rows.find((row) => row.scope === "local");
  assert.deepEqual(local.missing, [], "pi holds no local scope, so nothing is missing there");
});

test("a shared field that differs is marked with each side's current value as text", () => {
  const rows = unifiedMcpRows(
    [piServer({ command: "npx", args: ["-y", "v1"], envNames: ["A", "B"] })],
    [claudeServer({ command: "npx", args: ["-y", "v2"], envNames: ["A"] })],
  );
  assert.deepEqual(rows[0].inconsistent, [
    { field: "args", values: { pi: "-y v1", claude: "-y v2" } },
    { field: "envNames", values: { pi: "A, B", claude: "A" } },
  ]);
  // The field projections the comparison runs on.
  assert.deepEqual(unifiedMcpPiFields(piServer({ command: "npx", args: ["-y", "v1"], envNames: ["A", "B"] })), {
    command: "npx",
    args: ["-y", "v1"],
    envNames: ["A", "B"],
  });
  assert.deepEqual(unifiedMcpClaudeFields(claudeServer({ command: "npx", args: ["-y", "v2"], envNames: ["A"] })), {
    command: "npx",
    args: ["-y", "v2"],
    envNames: ["A"],
  });
});

// ---------------------------------------------------------------------------
// The mcp capability declarations (#85 c2).

test("pi declares its mcp capabilities: no local scope, no sse/ws, a toggle of its own, no CLI add", () => {
  const caps = createPiAdapter().capabilities;
  assert.deepEqual(caps.mcp?.scopes, ["user", "project"]);
  assert.deepEqual(caps.mcp?.transports, ["stdio", "http"]);
  assert.equal(caps.mcp?.cliAdd, false, "pi writes its mcp.json itself (Q4 A)");
  assert.equal(caps.mcp?.enableToggle, true);
  assert.equal(caps.mcp?.toolFilter, true, "pi's exposure and toolExposure");
});

test("claude declares its mcp capabilities: local scope, sse and ws, CLI adds, no toggle (Q1 A)", () => {
  const caps = createClaudeAdapter().capabilities;
  assert.deepEqual(caps.mcp?.scopes, ["user", "project", "local"]);
  assert.deepEqual(caps.mcp?.transports, ["stdio", "http", "sse", "ws"]);
  assert.equal(caps.mcp?.cliAdd, true, "claude is written only through its CLI (AC2)");
  assert.equal(caps.mcp?.enableToggle, false, "claude has no enable/disable command (Q1 A)");
  assert.equal(caps.mcp?.toolFilter, false);
});
