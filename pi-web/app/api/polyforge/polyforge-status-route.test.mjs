// carries: app/api/polyforge/status/*, lib/polyforge-status.ts
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// a failed presence probe is cached for 5 s, not 60 s. The
// real GET /api/polyforge/status is driven through a fake pf (PF_BIN) whose
// answer a mode file decides: `fail` exits 1 on every argv, `ok` answers
// `--version` and `--json ops status`.
const work = mkdtempSync(join(tmpdir(), "pf-status-route-"));
const previousPfBin = process.env.PF_BIN;
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

// a seed-root fixture shaped like the image's final layer
// (polyforge#162: releases/<ver>/manifest.json is the one fact).
const seedFixture = () => {
  const seed = mkdtempSync(join(work, "seed-"));
  mkdirSync(join(seed, "releases", "v0.2.4-dev.1"), { recursive: true });
  writeFileSync(join(seed, "releases", "v0.2.4-dev.1", "manifest.json"), "{}\n");
  return seed;
};
const withSeam = async (seedRoot, run) => {
  const previous = process.env.PI_TEST_SEED_ROOT;
  process.env.PI_TEST_SEED_ROOT = seedRoot;
  try {
    return await run();
  } finally {
    if (previous === undefined) delete process.env.PI_TEST_SEED_ROOT; else process.env.PI_TEST_SEED_ROOT = previous;
    if (seedRoot) rmSync(seedRoot, { recursive: true, force: true });
  }
};

test.after(() => {
  if (previousPfBin === undefined) delete process.env.PF_BIN; else process.env.PF_BIN = previousPfBin;
  rmSync(work, { force: true, recursive: true });
});

const get = (host = "127.0.0.1:30141") => new Request(`http://${host}/api/polyforge/status`, { headers: { host } });

test("a status read 5 s after a failed probe answers pfPresent:true once pf works again", async () => {
  writeFileSync(mode, "fail");
  const failed = await GET(get());
  assert.equal(failed.status, 200);
  assert.equal((await failed.json()).pfPresent, false, "the first probe in a fresh module fails");
  const answeredAt = Date.now();

  writeFileSync(mode, "ok");
  await new Promise((resolve) => setTimeout(resolve, Math.max(0, answeredAt + 5000 - Date.now())));

  const recovered = await GET(get());
  assert.equal(recovered.status, 200);
  assert.equal((await recovered.json()).pfPresent, true, "the failed probe is cached for 5 s, not 60 s");
});

test("polyforge#177: the host stays the host — no seed manifest means imageManaged:false and the shape unchanged", async () => {
  await withSeam(join(work, "no-such-seed"), async () => {
    writeFileSync(mode, "ok");
    const ok = await GET(get());
    const body = await ok.json();
    assert.equal(body.imageManaged, false, "a host answers the image fact as false");
    assert.equal(body.pfPresent, true, "the host behavior is unchanged");
    assert.deepEqual(Object.keys(body).sort(), ["imageManaged", "pfPresent", "status"], "nothing else moved");
  });
});
