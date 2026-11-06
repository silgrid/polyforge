// carries: lib/polyforge-integration.ts, app/api/polyforge/integration/route.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-integration.ts");

// The Work › 待集成 read: one `pf --json config list`, then per
// mapped project `pf --json queue --project <p> --attr integration.state=landed`
// and `pf --json queue --project <p> --bundle integrate --limit 5`, and one
// `pf --json watch <wi> --once --demand --events 40` per batch that is not
// terminal. The fixtures are the real shapes of a landed record, a waived
// one, a red batch's blame write and an integrate batch's watch, reshaped to
// neutral names (projects alpha/beta, repo app).

const SHA_A = "1111111111111111111111111111111111111111";
const SHA_B = "2222222222222222222222222222222222222222";
const SHA_C = "3333333333333333333333333333333333333333";

/** A hub list row carrying every field the passport must drop. */
function hubRow(id, slug, extra) {
  return {
    id, slug, seq: 1, project_id: "prj_SENTINEL", project: "alpha", goal: "GOAL_SENTINEL", content: "CONTENT_SENTINEL",
    status: "wrapped", priority: "normal", labels: [], reporter: "REPORTER_SENTINEL", bundle_name: "chore",
    created_at: "2026-11-12T00:00:00Z", session_fingerprint: "SESSION_SENTINEL", ...extra,
  };
}

// The blame write's shape: landed, then attributed by a red batch.
const BLAMED = hubRow("wi_Blamed21", "alpha#21", {
  attrs: {
    other: "OTHER_ATTR_SENTINEL",
    integration: {
      state: "landed",
      repos: [{ repo: "app", pr: "https://github.com/example/app/pull/21", head: "HEAD_SENTINEL", light_verified_head: "LVH_SENTINEL", merge_commit: SHA_A }],
      merge_commits: { app: [SHA_A] },
      batch: "wi_BatchRed26",
      red_rounds: 1,
      red_heads: { app: "RED_HEADS_SENTINEL" },
      fix_tickets: ["wi_FixTicket27"],
    },
  },
});
// The waived record's shape.
const WAIVED = hubRow("wi_Waived16", "alpha#16", {
  attrs: {
    integration: {
      state: "landed",
      repos: [{ repo: "app", pr: "https://github.com/example/app/pull/16", head: "HEAD_SENTINEL", light_verified_head: "LVH_SENTINEL", merge_commit: SHA_B }],
      merge_commits: { app: [SHA_B] },
      waived: true,
    },
  },
});
// A plain landed record whose PR is not an http(s) link.
const UNSAFE_PR = hubRow("wi_Unsafe22", "alpha#22", {
  attrs: {
    integration: {
      state: "landed",
      repos: [{ repo: "app", pr: "javascript:alert(1)", head: "HEAD_SENTINEL", light_verified_head: "LVH_SENTINEL", merge_commit: SHA_C }],
      merge_commits: { app: [SHA_C] },
    },
  },
});

/** The watch of an integrate batch mid-run (the real batch watch's shape,
 *  reshaped), with a pending question. */
const RUNNING_WATCH = {
  work_item: "wi_BatchRun30",
  slug: "alpha#30",
  known: true,
  execution: {
    workflow_status: "running",
    current_step: "full-gate",
    updated_at: "2026-11-13T01:30:00Z",
    steps: {
      close: { status: "pending" },
      collect: { status: "done", started_at: "2026-11-13T01:28:19Z" },
      "full-gate": { status: "running", started_at: "2026-11-13T01:29:00Z" },
      revert: { status: "done", started_at: "2026-11-13T01:28:41Z" },
    },
    declared_steps: [{ id: "collect", on_fail: null }, { id: "revert", on_fail: null }, { id: "full-gate", on_fail: "revert" }, { id: "close", on_fail: null }],
    rewinds: { agent: 0, max_rewinds: 3, owner: 0 },
    pending_question: { id: "q-30-1", step: "full-gate", question: "The gate is red twice; retry it?", asked_at: "2026-11-13T01:31:00Z", session: "ASKER_SESSION_SENTINEL" },
  },
  hub_status: "running",
  events: [],
  last_seq: 9,
};

const STATE = {
  config: { items: [{ key: '[workspace."/srv/one"]', value: "beta, alpha", section: true }, { key: "user_id", value: "USER_SENTINEL" }] },
  queue: {
    "alpha|landed": { items: [BLAMED, WAIVED, UNSAFE_PR], total: 3 },
    "alpha|batches": {
      items: [
        hubRow("wi_BatchRun30", "alpha#30", { status: "running", bundle_name: "integrate" }),
        hubRow("wi_BatchLost31", "alpha#31", { status: "paused", bundle_name: "integrate" }),
        hubRow("wi_BatchRed26", "alpha#26", { status: "wrapped", bundle_name: "integrate" }),
      ],
      total: 3,
    },
  },
  failProject: "beta",
  watch: { wi_BatchRun30: RUNNING_WATCH },
  failWatch: "wi_BatchLost31",
};

const work = mkdtempSync(join(tmpdir(), "pf-integration-"));
const log = join(work, "argv.log");
const statePath = join(work, "state.json");
const pf = join(work, "pf");
writeFileSync(statePath, JSON.stringify(STATE));
writeFileSync(log, "");
writeFileSync(pf, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(args) + "\\n");
const state = JSON.parse(fs.readFileSync(${JSON.stringify(statePath)}, "utf8"));
const out = (value) => { process.stdout.write(JSON.stringify(value)); process.exit(0); };
const same = (expected) => expected.length === args.length && expected.every((arg, i) => arg === args[i]);
if (same(["--version"])) { process.stdout.write("pf version v2.21.0\\n"); process.exit(0); }
if (same(["--json", "config", "list"])) out(state.config);
if (args[1] === "queue") {
  const project = args[3];
  if (project === state.failProject) {
    process.stderr.write("queue: project " + project + ": hub unreachable token=SECRETVALUE " + "x".repeat(2000) + "\\n");
    process.exit(1);
  }
  if (same(["--json", "queue", "--project", project, "--attr", "integration.state=landed"])) out(state.queue[project + "|landed"] ?? { items: [], total: 0 });
  if (same(["--json", "queue", "--project", project, "--bundle", "integrate", "--limit", "5"])) out(state.queue[project + "|batches"] ?? { items: [], total: 0 });
}
if (args[1] === "watch" && same(["--json", "watch", args[2], "--once", "--demand", "--events", "40"])) {
  if (args[2] === state.failWatch) { process.stderr.write("watch: daemon unreachable\\n"); process.exit(1); }
  out(state.watch[args[2]] ?? { known: false });
}
process.stderr.write("unexpected argv\\n");
process.exit(2);
`);
chmodSync(pf, 0o755);

const previousPfBin = process.env.PF_BIN;
process.env.PF_BIN = pf;
const read = await lib.readPolyforgeIntegration();
if (previousPfBin === undefined) delete process.env.PF_BIN; else process.env.PF_BIN = previousPfBin;
const ledger = readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
rmSync(work, { force: true, recursive: true });

const project = (name) => read.projects.find((entry) => entry.project === name);

test("the landed read lists each mapped project's attrs.integration landed items through the passport", () => {
  assert.equal(read.pfPresent, true);
  assert.deepEqual(read.projects.map((entry) => entry.project), ["alpha", "beta"], "every mapped project, name-ordered");
  const alpha = project("alpha");
  assert.ok(!Object.hasOwn(alpha, "error"), "alpha reads cleanly");
  assert.deepEqual(alpha.landed, [
    {
      id: "wi_Blamed21", slug: "alpha#21",
      repos: [{ repo: "app", pr: "https://github.com/example/app/pull/21", merge_commit: SHA_A }],
      batch: "wi_BatchRed26", red_rounds: 1, fix_tickets: ["wi_FixTicket27"],
    },
    {
      id: "wi_Waived16", slug: "alpha#16",
      repos: [{ repo: "app", pr: "https://github.com/example/app/pull/16", merge_commit: SHA_B }],
      waived: true,
    },
    {
      id: "wi_Unsafe22", slug: "alpha#22",
      repos: [{ repo: "app", pr: null, merge_commit: SHA_C }],
    },
  ], "pf's order; slug, PR, merge commit, waived, batch, red rounds and fix tickets field by field; a javascript: PR is dropped");
  const serialized = JSON.stringify(alpha.landed);
  for (const dropped of ["HEAD_SENTINEL", "LVH_SENTINEL", "RED_HEADS_SENTINEL", "red_heads", "light_verified_head", "\"head\"", "merge_commits",
    "CONTENT_SENTINEL", "GOAL_SENTINEL", "prj_SENTINEL", "REPORTER_SENTINEL", "SESSION_SENTINEL", "OTHER_ATTR_SENTINEL", "javascript:", "\"state\""]) {
    assert.ok(!serialized.includes(dropped), `a landed row must not carry ${dropped}`);
  }
});

test("a project whose pf queue fails carries its own masked, bounded error and the others read on", () => {
  const beta = project("beta");
  assert.ok(beta, "the failed project is still listed");
  assert.deepEqual(beta.landed, []);
  assert.deepEqual(beta.batches, []);
  assert.match(beta.error, /^queue: project beta: hub unreachable token=\*\*\*/, "pf's own stderr, credentials masked");
  assert.ok(!beta.error.includes("SECRETVALUE"));
  assert.ok(beta.error.length <= 512, "capped at 512 characters");
  assert.ok(!Object.hasOwn(project("alpha"), "error"), "alpha carries no error");
  assert.ok(!Object.hasOwn(read, "error"), "no read-wide error when the mapping read succeeds");
});

test("a batch that is not terminal carries its watch's steps, current step and pending question; a terminal one reads no watch", () => {
  const alpha = project("alpha");
  assert.deepEqual(alpha.batches, [
    {
      id: "wi_BatchRun30", slug: "alpha#30", status: "running",
      workflowStatus: "running", currentStep: "full-gate",
      steps: [
        { name: "collect", status: "done", startedAt: "2026-11-13T01:28:19Z" },
        { name: "revert", status: "done", startedAt: "2026-11-13T01:28:41Z" },
        { name: "full-gate", status: "running", startedAt: "2026-11-13T01:29:00Z" },
        { name: "close", status: "pending" },
      ],
      declared_steps: [{ id: "collect" }, { id: "revert" }, { id: "full-gate" }, { id: "close" }],
      pendingQuestion: { id: "q-30-1", step: "full-gate", question: "The gate is red twice; retry it?", askedAt: "2026-11-13T01:31:00Z" },
    },
    { id: "wi_BatchLost31", slug: "alpha#31", status: "paused", error: "watch: daemon unreachable" },
    { id: "wi_BatchRed26", slug: "alpha#26", status: "wrapped" },
  ]);
  assert.ok(!JSON.stringify(alpha.batches).includes("ASKER_SESSION_SENTINEL"));
  const watched = ledger.filter((argv) => argv[1] === "watch").map((argv) => argv[2]).sort();
  assert.deepEqual(watched, ["wi_BatchLost31", "wi_BatchRun30"], "only the batches that are not terminal are watched, once each");
});

test("the argv ledger holds only the config, queue and watch reads — never ops status, ops integrate or integration=1", () => {
  const seen = ledger.map((argv) => JSON.stringify(argv)).sort();
  const expected = [
    ["--version"],
    ["--json", "config", "list"],
    ["--json", "queue", "--project", "alpha", "--attr", "integration.state=landed"],
    ["--json", "queue", "--project", "alpha", "--bundle", "integrate", "--limit", "5"],
    ["--json", "queue", "--project", "beta", "--attr", "integration.state=landed"],
    ["--json", "queue", "--project", "beta", "--bundle", "integrate", "--limit", "5"],
    ["--json", "watch", "wi_BatchRun30", "--once", "--demand", "--events", "40"],
    ["--json", "watch", "wi_BatchLost31", "--once", "--demand", "--events", "40"],
  ].map((argv) => JSON.stringify(argv)).sort();
  assert.deepEqual(seen, expected, "the presence probe, one mapping read, two queue reads per project, one watch per live batch");
  const flat = ledger.flat().join(" ");
  assert.doesNotMatch(flat, /\bops\b/, "no ops verb (status or integrate)");
  assert.doesNotMatch(flat, /integration=1/);
});
