// carries: app/api/polyforge/status/*, lib/polyforge-status.ts
import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
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
process.stdout.write("{}");
`);
chmodSync(pf, 0o755);
process.env.PF_BIN = pf;

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { GET } = await jiti.import("./status/route.ts");

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
