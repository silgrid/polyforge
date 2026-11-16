// carries: app/api/polyforge/tasks/route.ts, app/api/polyforge/tasks/[id]/route.ts, lib/polyforge-tasks.ts, lib/polyforge-status.ts
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// polyforge#73 AC1–AC3, pinned at unit level with a stub pf (PF_BIN) that
// logs every argv: on a multi-Cell pf the Tasks list read becomes one
// `pf --profile <cell> --json task list` per valid, in-service Cell of the
// Host aggregate (the same `pf --json ops status` the Jobs tab polls — no
// new probe), each row named with its Cell by the route's outer layer (the
// nine-key row passport unchanged); a Cell that fails degrades to its
// cellErrors entry while the rest list; a pf without the multi-Cell
// protocol (and an aggregate that cannot be read) keeps the one unflagged
// `pf --json task list` of today. The detail face (`?profile=`) is pinned
// in the same file below.
const work = mkdtempSync(join(tmpdir(), "pf-tasks-cells-"));
const previousPfBin = process.env.PF_BIN;
const stateFile = join(work, "state.json");
const log = join(work, "argv.log");
const pf = join(work, "pf");
writeFileSync(stateFile, JSON.stringify({ caps: true, failPublic: false, opsFails: false, failPublicShow: false }));
writeFileSync(log, "");
writeFileSync(pf, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(args) + "\\n");
const s = JSON.parse(fs.readFileSync(${JSON.stringify(stateFile)}, "utf8"));
const out = (v) => { process.stdout.write(JSON.stringify(v) + "\\n"); process.exit(0); };
if (args.includes("--version")) {
  if (s.caps) out({ version: "v9.9.9", capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] });
  process.stdout.write("v9.9.9\\n"); process.exit(0);
}
let profile = "";
const rest = [];
for (let i = 0; i < args.length; i++) {
  if (args[i] === "--json") continue;
  if (args[i] === "--profile") { profile = args[i + 1]; i++; continue; }
  rest.push(args[i]);
}
if (rest[0] === "ops" && rest[1] === "status") {
  if (s.opsFails) { process.stderr.write("pf: daemon not reachable (connection refused)\\n"); process.exit(1); }
  out({ machine_id: "m-e2e", workspaces: [], env_names: [], jobs: [], queue: { pending: 0, running: 0, max_concurrent: 4 },
    tasks: { running: 1, completed: 1 },
    cells: [
      { profile: "default", state: "serving", valid: true },
      { profile: "public", state: "serving", valid: true },
      { profile: "broken", state: "invalid", valid: false, reason: "config cli-broken.toml: line 1: boom" },
    ] });
}
const ROW = (id) => ({ id, spec: { project: "workbench", max_duration_ns: 1, max_tokens: 1 }, state: "completed",
  prompt_summary: "the task of " + (profile || "the local daemon"), created_at: "2026-11-16T00:00:00Z",
  started_at: "2026-11-16T00:00:01Z", ended_at: "2026-11-16T00:10:00Z", wall_consumed_ns: 42000000000,
  report_tail: "the tail", reporter: "alice" });
if (rest[0] === "task" && rest[1] === "list") {
  if (profile === "public" && s.failPublic) { process.stderr.write("pf: cell public is unreachable (connection refused)\\n"); process.exit(1); }
  out({ tasks: [ROW(profile === "public" ? "task_1111111111111111" : "task_00000000000000d1")] });
}
if (rest[0] === "task" && rest[1] === "show") {
  if (profile === "public" && s.failPublicShow) { process.stderr.write("pf: cell public is unreachable (connection refused)\\n"); process.exit(1); }
  out({ ...ROW("task_1111111111111111"), report: "**done**", truncated: false });
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(pf, 0o755);
process.env.PF_BIN = pf;

test.after(() => {
  if (previousPfBin === undefined) delete process.env.PF_BIN; else process.env.PF_BIN = previousPfBin;
  rmSync(work, { force: true, recursive: true });
});

const setState = (patch) => writeFileSync(stateFile, JSON.stringify({ caps: true, failPublic: false, opsFails: false, failPublicShow: false, ...patch }));
/** A fresh module graph per scenario: the presence probe's 60 s cache and
 *  the ops reader's 1 s reuse window never leak between scenarios. */
const loadRoutes = async () => {
  const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
  return {
    list: (await jiti.import("./tasks/route.ts")).GET,
    show: (await jiti.import("./tasks/[id]/route.ts")).GET,
  };
};
const get = (path) => new Request(`http://127.0.0.1:30141${path}`, { headers: { host: "127.0.0.1:30141" } });
/** every spawn's argv, as the stub logged it (one line each). */
const calls = () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
const taskLists = () => calls().filter((argv) => argv.includes("task") && argv.includes("list"));
const opsReads = () => calls().filter((argv) => argv.includes("ops") && argv.includes("status"));
const profilesOf = (argvs) => argvs.map((argv) => argv[argv.indexOf("--profile") + 1]);
const NINE_KEYS = ["id", "project", "state", "prompt_summary", "started_at", "ended_at", "wall_consumed_ns", "report_tail", "reporter"];

test("polyforge#73 u3: the Tasks list reads every serving Cell of the Host aggregate and names each row's Cell", async () => {
  setState({});
  writeFileSync(log, "");
  const { list: GET } = await loadRoutes();
  const response = await GET(get("/api/polyforge/tasks"));
  assert.equal(response.status, 200, "no 5xx");
  const body = await response.json();
  assert.ok(!("cell" in body), "the retired top-level single-Cell annotation is gone");
  assert.equal(body.cellErrors, undefined, "every serving Cell answered");
  assert.equal(body.tasks.length, 2, "one row per serving Cell (the ticket's two-Cell oracle)");
  for (const [id, cell] of [["task_00000000000000d1", "default"], ["task_1111111111111111", "public"]]) {
    const row = body.tasks.find((entry) => entry.id === id);
    assert.ok(row, `the row of ${cell} is listed`);
    assert.equal(row.cell, cell, "the route names the row's Cell on the outer layer");
    assert.deepEqual(Object.keys(row).sort(), [...NINE_KEYS, "cell"].sort(), "the nine-key row passport plus exactly one Cell field");
  }
  // argv: the one shared ops read (no new probe), then one pinned list read
  // per serving Cell — `--profile` before `--json`, the broken Cell never read.
  assert.equal(opsReads().length, 1, "the Cell list comes from the one Host aggregate read");
  assert.deepEqual(profilesOf(taskLists()), ["default", "public"], "one `pf --profile <cell> --json task list` per serving Cell, in the aggregate's order");
  for (const argv of taskLists()) {
    assert.ok(argv.indexOf("--profile") < argv.indexOf("--json"), "--profile precedes --json (global flags before the verb)");
  }
  assert.ok(!calls().some((argv) => argv.includes("broken")), "an invalid Cell is never read");
});

test("polyforge#73 u3: a Cell that fails to list degrades to its cellErrors entry while the rest list", async () => {
  setState({ failPublic: true });
  writeFileSync(log, "");
  const { list: GET } = await loadRoutes();
  const response = await GET(get("/api/polyforge/tasks"));
  assert.equal(response.status, 200, "no 5xx");
  const body = await response.json();
  assert.equal(body.tasks.length, 1, "the default Cell's task still lists");
  assert.equal(body.tasks[0].cell, "default");
  assert.equal(Array.isArray(body.cellErrors) && body.cellErrors.length, 1, "the failed Cell is named");
  assert.equal(body.cellErrors[0].cell, "public");
  assert.match(body.cellErrors[0].error, /unreachable/, "pf's own bounded stderr names the failure");
});

test("polyforge#73 u3: a pf without the multi-Cell protocol keeps the one unflagged list read", async () => {
  setState({ caps: false });
  writeFileSync(log, "");
  const { list: GET } = await loadRoutes();
  const response = await GET(get("/api/polyforge/tasks"));
  assert.equal(response.status, 200, "no 5xx");
  const body = await response.json();
  assert.deepEqual(taskLists(), [["--json", "task", "list"]], "exactly one unflagged pf --json task list (the pre-Cell fallback)");
  assert.equal(opsReads().length, 0, "no aggregate read without the multi-Cell protocol");
  assert.equal(body.tasks.length, 1);
  assert.equal(body.tasks[0].cell, undefined, "a fallback row carries no Cell");
  assert.equal(body.cellErrors, undefined);
  assert.ok(!("cell" in body), "no top-level Cell either");
});

test("polyforge#73 u3: a Host aggregate that cannot be read degrades to the one unflagged list read", async () => {
  setState({ opsFails: true });
  writeFileSync(log, "");
  const { list: GET } = await loadRoutes();
  const response = await GET(get("/api/polyforge/tasks"));
  assert.equal(response.status, 200, "no 5xx");
  const body = await response.json();
  assert.deepEqual(taskLists(), [["--json", "task", "list"]], "the daemon-down degradation keeps today's single read");
  assert.equal(body.tasks.length, 1);
  assert.equal(body.tasks[0].cell, undefined);
  assert.equal(body.cellErrors, undefined);
});
