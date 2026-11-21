import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const configSource = await read("./config/route.ts");
const historySource = await read("./config/history/route.ts");
const reloadSource = await read("./config/reload/route.ts");
const writeLib = await read("../../../lib/polyforge-config-write.ts");
const readLib = await read("../../../lib/polyforge-config.ts");

test("every new config route re-checks isApiRequestAllowed and never answers 5xx", () => {
  for (const [name, source] of [["config", configSource], ["history", historySource], ["reload", reloadSource]]) {
    assert.match(source, /isApiRequestAllowed\(request\)/, `${name} re-checks the host/origin gate in-route`);
    assert.match(source, /\{ error: "Forbidden" \}, \{ status: 403 \}/, `${name} answers 403 JSON on refusal`);
    assert.doesNotMatch(source, /status: 5\d\d/, `${name} must never answer 5xx`);
  }
  assert.match(configSource, /export async function GET\(request: Request\)/);
  assert.match(configSource, /export async function POST\(request: Request\)/);
  assert.match(historySource, /export async function GET\(request: Request\)/);
  assert.match(historySource, /export async function POST\(request: Request\)/);
  assert.match(reloadSource, /export async function POST\(request: Request\)/);
});

test("writes are JSON-only and every mutation is a pf spawn", () => {
  for (const source of [configSource, historySource, reloadSource]) {
    assert.match(source, /hasJsonContentType\(request\)/, "POST is JSON-only");
  }
  assert.match(writeLib, /spawn\(polyforgePfBin\(\), argv, \{ shell: false/, "one no-shell pf spawn helper resolving PF_BIN");
  assert.match(writeLib, /api_key goes on stdin only|stdin = `\$\{String\(body\.value\)\}\\n`/, "api_key reaches pf on stdin");
  assert.doesNotMatch(writeLib, /writeFileSync|writePrivateFileAtomicSync\([^)]*cli\.toml/, "the write module never writes a config file");
});

test("the read assembles only the three pf reads and gates capability on the list shape", () => {
  // the Cell reads are pinned to the request's scope, the
  // daemon read stays the Host aggregate unless a named Cell is read
  assert.match(readLib, /const pin = await pinPolyforgeScope\(scope\);/);
  assert.match(readLib, /\[\.\.\.pin\.prefix, "--json", "config", "list", \.\.\.pin\.suffix\]/);
  assert.match(readLib, /\[\.\.\.pin\.prefix, "--json", "config", "workspace", "--cwd", cwd\]/);
  assert.match(readLib, /polyforgeNamedCell\(scope\) \? \[\.\.\.pin\.prefix, "--json", "ops", "status"\] : \["--json", "ops", "status"\]/);
  assert.doesNotMatch(readLib, /\["--json", "config", "(list|workspace)"/, "no unscoped config read is left");
  assert.match(readLib, /hasConfigListShape/);
  assert.match(readLib, /POLYFORGE_CONFIG_PANEL_MINIMUM_PF_VERSION = "2.20.6"/);
});

// polyforge#151 (85b2): the [step_tiers] family's write gate — the Host-only
// catalog joins the panel allowlist as a prefix family, its own 0.2.4 floor
// treats an unparseable version as below, the defined-model set is
// recomputed per write (never trusted from the client), and every refusal
// precedes the write spawn (the reads — the probe, the pre-read list — stay
// reads).
test("the step_tiers family is writable only on the Host scope, behind its own floor and the defined-models gate", async () => {
  assert.match(writeLib, /parseStepTierKey\(key\) !== null/, "the family joins the panel's allowlist as a prefix family");
  assert.match(writeLib, /isPolyforgeVersionBelowMinimum\(presence\.version, POLYFORGE_CONFIG_STEP_TIERS_MINIMUM_PF_VERSION, true\)/, "the family's floor counts an unparseable version below");
  assert.match(writeLib, /await stepTierDefinedModels\(\)/, "the defined set is recomputed per write");
  assert.match(writeLib, /is not a defined model/, "an undefined new candidate is refused");
  assert.match(writeLib, /the Host's catalog/, "a non-Host write is refused with the Host reason");
  assert.match(configSource, /\?host=1 reads the Host keys/, "the GET read stays the one read face (no new read route for the gate)");
});
