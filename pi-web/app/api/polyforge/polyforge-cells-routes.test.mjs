import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const listSource = await read("./cells/route.ts");
const cellSource = await read("./cells/[profile]/route.ts");
const lib = await read("../../../lib/polyforge-cells.ts");

test("the Cell routes re-check the gate in-route and never answer 5xx", () => {
  for (const [name, source] of [["cells", listSource], ["cells/[profile]", cellSource]]) {
    assert.match(source, /isApiRequestAllowed\(request\)/, `${name} re-checks the host/origin gate`);
    assert.match(source, /\{ error: "Forbidden" \}, \{ status: 403 \}/);
    assert.doesNotMatch(source, /status: 5\d\d/, `${name} must never answer 5xx`);
  }
  assert.match(listSource, /export async function GET\(request: Request\)/);
  assert.match(listSource, /export async function POST\(request: Request\)/);
  assert.match(cellSource, /export async function POST\(request: Request, ctx: Ctx\)/);
  assert.match(cellSource, /export async function DELETE\(request: Request, ctx: Ctx\)/);
});

test("writes are JSON-only, pf spawns only, the key on stdin, no cleanup path", () => {
  assert.match(listSource, /hasJsonContentType\(request\)/);
  assert.match(cellSource, /hasJsonContentType\(request\)/);
  assert.match(lib, /spawn\(polyforgePfBin\(\), argv, \{ shell: false/);
  assert.match(lib, /runPf\(argv, `\$\{body\.apiKey\}\\n`\)/, "the api key reaches pf on stdin");
  for (const source of [listSource, cellSource, lib]) {
    assert.doesNotMatch(source, /"cleanup"/, "permanent cleanup stays CLI-only");
  }
});
