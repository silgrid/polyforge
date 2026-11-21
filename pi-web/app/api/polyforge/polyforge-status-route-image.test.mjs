// carries: app/api/polyforge/status/*, lib/polyforge-detect.ts
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// polyforge#177: the image-managed fact rides EVERY authorized branch of the
// status read — pf absent, the daemon answering, and the daemon's answer a
// failure — while the auth branch (403) carries no machine facts. This file
// runs as its own process (node --test spawns one per file), so the presence
// probe's module-side cache starts empty and the pf-ABSENT branch is
// drivable: the fake pf's mode file answers `fail` (absent), `ok` (present,
// `--json ops status` answers) and `daemon-down` (present, ops status
// fails).

const work = mkdtempSync(join(tmpdir(), "pf-status-route-image-"));
const mode = join(work, "mode");
const pf = join(work, "pf");
writeFileSync(pf, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
if (fs.readFileSync(${JSON.stringify(mode)}, "utf8") === "fail") process.exit(1);
if (args[0] === "--version") { process.stdout.write("v9.9.9\\n"); process.exit(0); }
if (fs.readFileSync(${JSON.stringify(mode)}, "utf8") === "daemon-down") { process.stderr.write("pf: daemon not reachable\\n"); process.exit(1); }
process.stdout.write("{}");
`);
chmodSync(pf, 0o755);
process.env.PF_BIN = pf;

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { GET } = await jiti.import("./status/route.ts");

test.after(() => {
  rmSync(work, { recursive: true, force: true });
});

const get = (host = "127.0.0.1:30141") => new Request(`http://${host}/api/polyforge/status`, { headers: { host } });

test("the image mode: every authorized branch answers imageManaged:true, and the judgment needs no pf", async (t) => {
  const seed = mkdtempSync(join(work, "seed-"));
  mkdirSync(join(seed, "releases", "v0.2.4-dev.1"), { recursive: true });
  writeFileSync(join(seed, "releases", "v0.2.4-dev.1", "manifest.json"), "{}\n");
  const previous = process.env.PI_TEST_SEED_ROOT;
  process.env.PI_TEST_SEED_ROOT = seed;
  t.after(() => {
    if (previous === undefined) delete process.env.PI_TEST_SEED_ROOT; else process.env.PI_TEST_SEED_ROOT = previous;
  });

  // the pf-absent branch FIRST, while the fresh module's presence cache is
  // empty: the judgment reads the seed manifest alone — pf missing must
  // still identify the image
  writeFileSync(mode, "fail");
  const absent = await GET(get());
  assert.equal(absent.status, 200);
  assert.deepEqual(
    await absent.json(),
    { imageManaged: true, pfPresent: false, status: null },
    "the pf-absent branch carries the image fact",
  );

  // the success branch
  writeFileSync(mode, "ok");
  const ok = await GET(get());
  assert.equal(ok.status, 200);
  assert.equal((await ok.json()).imageManaged, true, "the success branch carries the image fact");

  // the degraded branch: pf present, the daemon's answer a failure — 200
  // with status:null, never a 5xx, the image fact still present
  writeFileSync(mode, "daemon-down");
  const degraded = await GET(get());
  assert.equal(degraded.status, 200);
  const degradedBody = await degraded.json();
  assert.equal(degradedBody.imageManaged, true, "the degraded branch carries the image fact");
  assert.equal(degradedBody.status, null);

  // the auth branch answers 403 with no machine facts at all
  const forbidden = await GET(new Request("http://elsewhere:9/api/polyforge/status", { headers: { host: "elsewhere:9" } }));
  assert.equal(forbidden.status, 403);
  assert.equal("imageManaged" in (await forbidden.json()), false, "the 403 body carries no machine facts");
});
