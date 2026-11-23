// carries: lib/polyforge-messages.ts, lib/polyforge-messages-pure.ts, app/api/polyforge/messages/file-ticket/*, components/PolyforgeFileTicketDialog.tsx
// polyforge#167 (91-onboarding) AC2 / Q5: the first ticket is created in the
// develop lane. The wizard's write path is one `pf file create --stdin`
// whose JSON body carries `bundle_name` — the key and content never ride
// the command line — while the Messages path keeps its bundle-free flag
// create. Expected argv and body come from `pf file create`'s documented
// `--stdin` contract (internal/polyforge/cli/verbs_aihub.go) and the
// ticket's "Messages 路径保持不带 bundle".
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const work = mkdtempSync(join(tmpdir(), "pf-messages-lane-"));
const pf = join(work, "pf");
const log = join(work, "calls.jsonl");
writeFileSync(log, "");
writeFileSync(pf, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
let stdin = "";
let done = false;
const finish = () => {
  if (done) return;
  done = true;
  fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify({ args, stdin }) + "\\n");
  const rest = args.filter((a) => a !== "--json");
  const out = (v) => { process.stdout.write(JSON.stringify(v)); process.exit(0); };
  if (args.includes("--version")) out({ version: "v2.20.12", capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] });
  if (rest[0] === "profile") out({ profile: "default" });
  if (rest.includes("config") && rest.includes("list")) out({ items: [{ key: '[workspace."/ws"]', value: "polyforge", section: true }] });
  if (rest.includes("file") && rest.includes("create")) out({ id: "wi_new", slug: "polyforge#7" });
  process.exit(2);
};
process.stdin.on("data", (chunk) => { stdin += chunk; });
process.stdin.on("end", finish);
setTimeout(finish, 150);
`);
chmodSync(pf, 0o755);

const savedBin = process.env.PF_BIN;
process.env.PF_BIN = pf;
const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const lib = await jiti.import("./polyforge-messages.ts");
const pure = await jiti.import("./polyforge-messages-pure.ts");
const route = await jiti.import("../../app/api/polyforge/messages/file-ticket/route.ts");

test.after(() => {
  if (savedBin === undefined) delete process.env.PF_BIN;
  else process.env.PF_BIN = savedBin;
  rmSync(work, { recursive: true, force: true });
});

const calls = () => readFileSync(log, "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
const createCalls = () => calls().filter((call) => call.args.includes("file") && call.args.includes("create"));
const reset = () => writeFileSync(log, "");
const post = (body, headers = {}) => route.POST(new Request("http://127.0.0.1:30141/api/polyforge/messages/file-ticket", {
  method: "POST",
  headers: { host: "127.0.0.1:30141", "content-type": "application/json", ...headers },
  body: JSON.stringify(body),
}));

test("a lane is a plain token: develop passes, blank/option-shaped/oversized values are refused", () => {
  assert.deepEqual(pure.validatePolyforgeLane("develop"), { ok: true, lane: "develop" });
  assert.deepEqual(pure.validatePolyforgeLane("  develop  "), { ok: true, lane: "develop" });
  for (const bad of [undefined, null, "", "   ", "-x", "--bundle", "two words", "a\nb", "x".repeat(129), 7]) {
    assert.equal(pure.validatePolyforgeLane(bad).ok, false, JSON.stringify(bad));
  }
});

test("the message-less dialog defaults: blank goal/content, the first mapped project", () => {
  assert.deepEqual(pure.fileTicketDialogDefaults(null, null, ["alpha", "beta"]), { goal: "", project: "alpha", content: "" });
  assert.deepEqual(pure.fileTicketDialogDefaults(undefined, undefined, []), { goal: "", project: "", content: "" });
});

test("the wizard's lane create is one `file create --stdin`, the body on stdin only", async () => {
  reset();
  assert.deepEqual(await lib.createPolyforgeFileTicket({ goal: "Do it", project: "polyforge", content: "body" }, [], "develop"), { slug: "polyforge#7" });
  const [call] = createCalls();
  assert.deepEqual(call.args, ["--json", "file", "create", "--stdin"], "the wizard create takes its body on stdin");
  assert.deepEqual(JSON.parse(call.stdin), { project: "polyforge", goal: "Do it", content: "body", bundle_name: "develop" });
  assert.equal(call.args.includes("develop"), false, "the lane never rides the command line");
  assert.equal(call.args.includes("body"), false);
});

test("the Messages path keeps its bundle-free flag create, unchanged", async () => {
  reset();
  assert.deepEqual(await lib.createPolyforgeFileTicket({ goal: "Do it", project: "alpha", content: "body" }), { slug: "polyforge#7" });
  const [call] = createCalls();
  assert.deepEqual(call.args, ["--json", "file", "create", "--project", "alpha", "--goal", "Do it", "--content", "body"]);
  assert.equal(call.args.includes("--stdin"), false);
  assert.equal(call.args.includes("--bundle"), false, "a registration ticket: no lane");
  assert.equal(call.stdin, "");
});

test("the route reads the lane header, validates it before the spawn, and keeps the Messages body", async () => {
  reset();
  const created = await post({ goal: "First ticket", project: "polyforge", content: "content" }, { "x-polyforge-lane": "develop" });
  assert.equal(created.status, 200);
  assert.deepEqual(await created.json(), { ok: true, slug: "polyforge#7" });
  const [call] = createCalls();
  assert.equal(call.args.includes("--stdin"), true);
  assert.equal(JSON.parse(call.stdin).bundle_name, "develop");

  reset();
  const refused = await post({ goal: "First ticket", project: "polyforge", content: "content" }, { "x-polyforge-lane": "--bundle" });
  assert.equal(refused.status, 400);
  assert.match((await refused.json()).error, /lane/);
  assert.deepEqual(createCalls(), [], "a refused lane spawns no create");

  reset();
  const messages = await post({ goal: "First ticket", project: "polyforge", content: "content" });
  assert.equal(messages.status, 200);
  const [plain] = createCalls();
  assert.equal(plain.args.includes("--stdin"), false, "the Messages path's flag create is unchanged");
  assert.equal(plain.args.includes("--bundle"), false);
});
