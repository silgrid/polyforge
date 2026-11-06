import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// the job detail's progress tool card — header from the poll's
// light fields on the shared ToolCallCard shell, the on-demand detail states.
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const card = await jiti.import("./PolyforgeProgressToolCard.tsx");

const line = (tool, text = tool ? `${tool.name}: ${tool.argument ?? ""}` : "legacy: text") => ({ id: "0123456789abcdef.10.0", kind: "tool", text, ...(tool ? { tool } : {}) });
const render = (props) => renderToStaticMarkup(React.createElement(I18nProvider, null,
  React.createElement(card.PolyforgeProgressToolCard, { detail: undefined, onToggle: () => {}, ...props })));

test("the header is name + a preview within 120 code points; the full masked header is the title", () => {
  const argument = `curl --token=*** https://e2e.invalid/${"x".repeat(200)} TAIL`;
  const header = card.progressToolHeader(line({ name: "bash", argument, status: "ok", durationMs: 1500 }));
  assert.equal(card.PROGRESS_TOOL_HEADER_MAX, 120);
  assert.equal(Array.from(`${header.name}: ${header.preview}`).length, 120, "name + ': ' + preview fill the 120 code points");
  assert.ok(header.preview.endsWith("…") && !header.preview.includes("TAIL"));
  assert.equal(header.full, `bash: ${argument}`);
  const short = card.progressToolHeader(line({ name: "read", argument: "a\n  b", status: "running" }));
  assert.deepEqual([short.preview, short.full, short.durationMs], ["a b", "read: a\n  b", undefined], "whitespace collapses in the preview only");
  assert.deepEqual(card.progressToolHeader(line(undefined)), { name: "legacy: text", preview: "", full: "legacy: text" }, "a line without light fields shows its text");
  const html = render({ line: line({ name: "bash", argument, status: "ok", durationMs: 1500 }) });
  assert.match(html, /data-pf-progress-kind="tool"/);
  assert.match(html, new RegExp(`title="bash: curl --token=\\*\\*\\* https://e2e.invalid/x+ TAIL"`));
  assert.ok(!html.replace(/title="[^"]*"/g, "").includes("TAIL"), "the tail is only on the title");
});

test("status and duration: ok / error / running with the transcript's format and 100 ms floor; no duration is invented", () => {
  const ok = render({ line: line({ name: "bash", argument: "a", status: "ok", durationMs: 1500 }) });
  assert.match(ok, />ok<\/span>/);
  assert.match(ok, />1\.5s<\/span>/);
  assert.match(ok, /border:1px solid rgba\(34,197,94,0.25\)/);
  const failed = render({ line: line({ name: "bash", argument: "a", status: "error", durationMs: 200 }) });
  assert.match(failed, />error<\/span>/);
  assert.match(failed, />0\.2s<\/span>/);
  assert.match(failed, /border:1px solid rgba\(248,113,113,0.45\)/, "a failed call wears the transcript's error tone");
  const runningHtml = render({ line: line({ name: "bash", argument: "a", status: "running" }) });
  assert.match(runningHtml, />running<\/span>/);
  assert.doesNotMatch(runningHtml, /\d+\.\ds</);
  assert.doesNotMatch(render({ line: line({ name: "bash", argument: "a", status: "ok", durationMs: 40 }) }), /\d+\.\ds</, "under 100 ms: no time");
});

test("collapsed by default: one aria-expanded header and no body, no detail asked", () => {
  let asked = 0;
  const html = render({ line: line({ name: "bash", argument: "a", status: "ok" }), onToggle: () => { asked += 1; } });
  assert.equal([...html.matchAll(/<button/g)].length, 1);
  assert.match(html, /aria-expanded="false"/);
  assert.doesNotMatch(html, /Input|Output|No result|unavailable/);
  assert.equal(asked, 0, "rendering asks nothing; only an expansion does");
});

test("the detail answer parses strictly: only a well-formed available answer is a detail", () => {
  assert.deepEqual(card.parseProgressToolDetail({ available: true, hasResult: true, tool: { input: "IN", output: "OUT", isError: true } }), { status: "available", input: "IN", output: "OUT", isError: true });
  assert.deepEqual(card.parseProgressToolDetail({ available: true, hasResult: false, tool: { input: "IN", output: null } }), { status: "available", input: "IN", output: null, isError: false });
  for (const bad of [null, "x", {}, { available: false, error: "gone" }, { available: true }, { available: true, tool: { input: 1, output: "o" } }, { available: true, tool: { input: "i" } }, { error: "Forbidden" }]) {
    assert.deepEqual(card.parseProgressToolDetail(bad), { status: "unavailable" }, JSON.stringify(bad));
  }
});

test("the detail read is one GET of the route with the wi and the id; any failure is unavailable", async () => {
  const previous = globalThis.fetch;
  const asked = [];
  try {
    globalThis.fetch = async (url, init) => { asked.push([url, init?.cache]); return { ok: true, json: async () => ({ available: true, tool: { input: "I", output: null } }) }; };
    assert.deepEqual(await card.fetchProgressToolDetail("wi_A1", "0123456789abcdef.1.2"), { status: "available", input: "I", output: null, isError: false });
    assert.deepEqual(asked, [["/api/polyforge/progress/tool?wi=wi_A1&id=0123456789abcdef.1.2", "no-store"]]);
    globalThis.fetch = async () => ({ ok: false, json: async () => ({ available: true, tool: { input: "I", output: "O" } }) });
    assert.deepEqual(await card.fetchProgressToolDetail("wi_A1", "x"), { status: "unavailable" }, "a refusal is unavailable");
    globalThis.fetch = async () => { throw new Error("offline"); };
    assert.deepEqual(await card.fetchProgressToolDetail("wi_A1", "x"), { status: "unavailable" }, "a network error is unavailable");
  } finally {
    globalThis.fetch = previous;
  }
});
