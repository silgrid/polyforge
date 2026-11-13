// Pins docs/agents/tools.md's plain-language answer to "what does `configured` mean"
// to lib/tool-presets.ts, so the doc and the code cannot drift apart:
// `configured` sends no tool override (getToolNamesForPreset returns undefined) and the
// other presets pin the lists the doc names. claude-sessions.md says claude has none.
import "#test-env";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const presets = await jiti.import("../lib/tool-presets.ts");
const tools = readFileSync(new URL("./agents/tools.md", import.meta.url), "utf8");
const claude = readFileSync(new URL("./agents/claude-sessions.md", import.meta.url), "utf8");
const opening = tools.slice(0, tools.indexOf("\n## "));

test("tools.md opens with the plain-language `configured` answer, and the code agrees", () => {
  assert.equal(presets.getToolNamesForPreset("configured"), undefined, "configured sends no toolNames");
  assert.match(opening, /`configured`/);
  assert.match(opening, /sends no tool override/);
  assert.match(opening, /`defaultTools`/);
  assert.match(opening, /`settings\.json`/);
  assert.match(opening, /exactly as the `pi` CLI does/);
  assert.match(opening, /If no `defaultTools` is written anywhere, that is pi's built-in default list\./);
  assert.match(opening, /never pinned shows `configured`/);
});

test("the opening names the lists the other presets pin, as lib/tool-presets.ts defines them", () => {
  assert.deepEqual(presets.getToolNamesForPreset("none"), []);
  assert.match(opening, /Chat only \(no tools\)/);
  const list = (names) => names.map((name) => `\`${name}\``).join(", ");
  const readOnly = presets.getToolNamesForPreset("read-only");
  const fallback = presets.getToolNamesForPreset("default");
  assert.ok(opening.includes(`read-only (${list(readOnly)})`), `read-only names ${readOnly.join(", ")}`);
  assert.ok(opening.includes(`default (${list(fallback)})`), `default names ${fallback.join(", ")}`);
  assert.equal(presets.getToolNamesForPreset("full").length, 7);
  assert.match(opening, /full \(all seven built-in tools\)/);
});

test("both notes say claude sessions have no tool preset and so no `configured` state", () => {
  assert.match(opening, /claude sessions have no pi-web tool preset and so no `configured` state/);
  assert.match(claude, /claude sessions have no pi-web tool preset and therefore no `configured` state/);
});
