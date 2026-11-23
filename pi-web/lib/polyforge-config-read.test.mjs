// carries: lib/polyforge-config.ts, lib/polyforge-version.ts
// polyforge#256 (T1, B1): the step-tier catalog's READ gate uses the same
// 0.2.4-0 floor as the write gate — a v0.2.4-dev.N pf reads the Host's
// [step_tiers] catalog, a v0.2.3 pf answers `capable:false` with the visible
// reason. Each version runs through a fresh module graph (the catalog's 10 s
// cache and the presence cache must not hide the swap) against a stub pf and
// an isolated model fixture, so no real configuration is read.
import "#test-env";
import assert from "node:assert/strict";
import { after, test } from "node:test";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { createJiti } from "jiti";

const SAVED = {
  PATH: process.env.PATH,
  PF_BIN: process.env.PF_BIN,
  PI_CODING_AGENT_DIR: process.env.PI_CODING_AGENT_DIR,
  PI_OFFLINE: process.env.PI_OFFLINE,
  ANTHROPIC_API_KEY: process.env.ANTHROPIC_API_KEY,
  ANTHROPIC_AUTH_TOKEN: process.env.ANTHROPIC_AUTH_TOKEN,
};
const trees = [];
after(() => {
  for (const [key, value] of Object.entries(SAVED)) {
    if (value === undefined) delete process.env[key];
    else process.env[key] = value;
  }
  for (const work of trees) rmSync(work, { recursive: true, force: true });
});

const MODELS = JSON.stringify({ providers: { acme: { baseUrl: "https://example.invalid/v1", apiKey: "sk-test", api: "openai-completions", models: [{ id: "one", name: "Acme One" }] } } });

/** A stub pf answering `version`, the Host config list and ops status; the
 *  claude probe fails (its defined names are just `default`). */
function stubTree(version) {
  const work = mkdtempSync(join(tmpdir(), "pf-read-256-"));
  const bin = join(work, "bin");
  const agent = join(work, "agent");
  mkdirSync(bin, { recursive: true });
  mkdirSync(agent, { recursive: true });
  writeFileSync(join(agent, "models.json"), MODELS);
  writeFileSync(join(bin, "claude"), "#!/bin/sh\necho 'claude: not logged in' >&2\nexit 1\n", { mode: 0o755 });
  writeFileSync(join(bin, "pf"), `#!${process.execPath}
const args = process.argv.slice(2);
const rest = args.filter((a) => a !== "--json");
if (args.includes("--version")) {
  process.stdout.write(JSON.stringify({ version: ${JSON.stringify(version)}, capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] }) + "\\n");
  process.exit(0);
}
if (rest[0] === "config" && rest[1] === "list") {
  process.stdout.write(JSON.stringify({ file: { sha256: "read-sha", exists: true }, items: [
    { key: "step_tiers.default", value: ["pi:acme/one"], source: "file", class: "live", schema: { kind: "model-list" } },
  ] }) + "\\n");
  process.exit(0);
}
if (rest[0] === "ops" && rest[1] === "status") { process.stdout.write(JSON.stringify({ queue: { max_concurrent: 4 } }) + "\\n"); process.exit(0); }
process.stderr.write("pf: unknown command " + rest.join(" ") + "\\n");
process.exit(2);
`);
  chmodSync(join(bin, "pf"), 0o755);
  trees.push(work);
  return { bin, agent };
}

async function readCatalogAt(version) {
  const tree = stubTree(version);
  process.env.PF_BIN = join(tree.bin, "pf");
  process.env.PATH = `${tree.bin}${delimiter}${SAVED.PATH}`;
  process.env.PI_CODING_AGENT_DIR = tree.agent;
  process.env.PI_OFFLINE = "1";
  delete process.env.ANTHROPIC_API_KEY;
  delete process.env.ANTHROPIC_AUTH_TOKEN;
  // a fresh module graph per version: the reads' caches must not mask the swap
  const jiti = createJiti(import.meta.url, { moduleCache: false });
  const read = await jiti.import("./polyforge-config.ts");
  return read.readStepTierCatalog();
}

test("a v0.2.4-dev.N pf reads the step-tier catalog (polyforge#256 B1)", async () => {
  const catalog = await readCatalogAt("v0.2.4-dev.49");
  assert.equal(catalog.capable, true, catalog.reason);
  assert.equal(catalog.pfPresent, true);
  assert.deepEqual(catalog.tiers, [{ tier: "default", candidates: ["pi:acme/one"] }]);
  assert.deepEqual(catalog.defined.pi, ["acme/one"], "the defined-model set is the fixture's own");
});

test("a v0.2.3 pf is refused by the same 0.2.4-0 floor with a visible reason", async () => {
  const catalog = await readCatalogAt("v0.2.3");
  assert.equal(catalog.capable, false);
  assert.match(catalog.reason, /0\.2\.4-0/);
  assert.deepEqual(catalog.tiers, []);
});
