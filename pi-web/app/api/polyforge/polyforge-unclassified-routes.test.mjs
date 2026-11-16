// carries: app/api/polyforge/unclassified/*, lib/polyforge-unclassified*
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// the unclassified route, driven through a fake pf (PF_BIN):
// GET /api/polyforge/unclassified (`pf --json ops unclassified`).
const work = mkdtempSync(join(tmpdir(), "pf-unclassified-routes-"));
const previousPfBin = process.env.PF_BIN;
const log = join(work, "argv.log");
const mode = join(work, "mode");
const pf = join(work, "pf");
const REPORT = {
  window_days: 7,
  items: [
    { text: "gateway tantrum A", count: 3, last_at: "2026-11-15T10:00:00Z" },
    { text: "upstream wailed mysteriously", count: 1, last_at: "2026-11-14T09:00:00Z" },
  ],
};
writeFileSync(pf, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
if (args[0] === "--version") { process.stdout.write("v2.21.0\\n"); process.exit(0); }
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify({ args }) + "\\n");
const mode = fs.readFileSync(${JSON.stringify(mode)}, "utf8");
if (mode === "fail") { process.stderr.write("ops unclassified: daemon refused (DAEMON-403)\\n"); process.exit(1); }
if (mode === "malformed") { process.stdout.write("not json\\n"); process.exit(0); }
if (mode === "shape") { process.stdout.write(JSON.stringify({ window_days: "7", items: 7 })); process.exit(0); }
process.stdout.write(JSON.stringify(${JSON.stringify(REPORT)}));
`);
chmodSync(pf, 0o755);
process.env.PF_BIN = pf;
writeFileSync(mode, "ok");
writeFileSync(log, "");

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
await jiti.import("#test-env");
const statusLib = await jiti.import("../../../lib/polyforge-status.ts");
const { GET } = await jiti.import("./unclassified/route.ts");
const getSource = await readFile(new URL("./unclassified/route.ts", import.meta.url), "utf8");

const calls = () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));

test.after(() => {
  if (previousPfBin === undefined) delete process.env.PF_BIN;
  else process.env.PF_BIN = previousPfBin;
  rmSync(work, { recursive: true, force: true });
});

const get = (host = "127.0.0.1:30141") => new Request(`http://${host}/api/polyforge/unclassified`, { headers: { host } });

test("the route reads the Host's unclassified report through pf and sanitizes it", async () => {
  const response = await GET(get());
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.pfPresent, true);
  assert.deepEqual(body.report, {
    windowDays: 7,
    items: [
      { text: "gateway tantrum A", count: 3, lastAt: "2026-11-15T10:00:00Z" },
      { text: "upstream wailed mysteriously", count: 1, lastAt: "2026-11-14T09:00:00Z" },
    ],
  });
  const argv = calls().find((call) => call.args.includes("unclassified"));
  assert.ok(argv, "the read reached pf");
  assert.deepEqual(argv.args.filter((arg) => arg !== "--json"), ["ops", "unclassified"]);
  assert.ok(!argv.args.includes("--profile"), "the read is Host-level, no --profile");
});

test("degradations are 200 fields, never 5xx: a failed read, an undecodable answer, a bad shape, an absent pf", async (t) => {
  for (const [m, want] of [
    ["fail", /daemon refused/],
    ["malformed", /undecodable/],
    ["shape", /undecodable/],
  ]) {
    writeFileSync(mode, m);
    const response = await GET(get());
    assert.equal(response.status, 200, m);
    const body = await response.json();
    assert.equal(body.pfPresent, true, m);
    assert.equal(body.report, null, m);
    assert.match(body.error, want);
  }
  // an absent pf: the probe reports absent, nothing is read
  const previous = process.env.PF_BIN;
  process.env.PF_BIN = join(work, "no-such-pf");
  await statusLib.probePolyforgeVersion(true);
  try {
    const response = await GET(get());
    assert.equal(response.status, 200);
    const body = await response.json();
    assert.equal(body.pfPresent, false);
    assert.equal(body.report, null);
    assert.equal(body.error, undefined);
  } finally {
    process.env.PF_BIN = previous;
    await statusLib.probePolyforgeVersion(true);
  }
  void t;
});

test("the route keeps the degradation-first contract and never leaks more than pf's bounded stderr", () => {
  assert.match(getSource, /isApiRequestAllowed/);
  assert.match(getSource, /probePolyforgePresent/);
  assert.match(getSource, /polyforge is unavailable/);
  assert.doesNotMatch(getSource, /process\.env\.(?!PF_BIN)/);
});
