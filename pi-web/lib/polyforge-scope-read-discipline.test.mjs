// carries: lib/polyforge-config-scope.ts, lib/polyforge-cells.ts
import "#test-env";
import assert from "node:assert/strict";
import { after, test } from "node:test";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createJiti } from "jiti";

// S194-10's read discipline, pinned at unit level: while the dock's Work
// pane is open every pf verb a spawn adds to the task-report ledger must
// stay inside the authored whitelist
// ["--version","ops","config","msg","recall","memory","task","profile","watch"].
// polyforge#18's scope resolution (resolvePolyforgeCellScope — the read the
// work-items/memory/messages routes make before their data read) broke it:
// its capability read spawned `pf --json version`, a verb the whitelist
// never held. pf answers the same machine line ({"version":…,
// "capabilities":[…]}, internal/polyforge/cli/verbs_version.go versionJSON)
// through the `--version` flag under `--json` — the whitelisted face — so
// the fix keeps the capability gate and the routing, only the spelling
// changes. This stub answers like the real pf (a `--version` flag with its
// capabilities, a `profile` resolution) and logs every argv, so the test
// asserts the discipline, not an implementation detail.

const jiti = createJiti(import.meta.url);
const cells = await jiti.import("./polyforge-cells.ts");

const work = mkdtempSync(join(tmpdir(), "pf-scope-discipline-"));
const log = join(work, "argv.log");
writeFileSync(log, "");
const stub = join(work, "pf");
const CAPS = ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"];
writeFileSync(stub, `#!${process.execPath}
const fs = require("fs"); const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify(args) + "\\n");
const out = (v) => { process.stdout.write(JSON.stringify(v)); process.exit(0); };
if (args.includes("--version")) out({ version: "v2.20.16", capabilities: ${JSON.stringify(CAPS)} });
if (args.includes("profile")) out({ profile: "public", source: "cwd" });
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(stub, 0o755);
const savedBin = process.env.PF_BIN;
process.env.PF_BIN = stub;
after(() => {
  if (savedBin === undefined) delete process.env.PF_BIN;
  else process.env.PF_BIN = savedBin;
  rmSync(work, { recursive: true, force: true });
});

/** every spawn's argv, in order, as the e2e ledger records it (one line each). */
const calls = () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
/** the verb the e2e ledger would file: the first token after stripping --json. */
const verb = (argv) => argv.filter((token) => token !== "--json")[0];
const WHITELIST = ["--version", "ops", "config", "msg", "recall", "memory", "task", "profile", "watch"];

test("the dock's Cell scope resolution reads pf only through whitelisted verbs (S194-10)", async () => {
  const scope = await cells.resolvePolyforgeCellScope();
  const verbs = calls().map(verb);
  assert.ok(calls().length > 0, "the scope resolution spawned pf");
  for (const name of verbs) {
    assert.ok(WHITELIST.includes(name), `no extra pf verb (${name})`);
  }
  // the routing the whitelist discipline must not change (polyforge#18):
  // a multi-Cell pf answers the capability gate through the --version face
  // and the resolved Cell is pinned explicitly.
  assert.equal(scope.cell, "public", "the resolved Cell is still named");
  assert.deepEqual(scope.pin.prefix, ["--profile", "public"], "the data-plane pin still addresses the routed Cell");
});
