import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { createJiti } from "jiti";

// one subagent entry under its parent progress tool line — the
// agent badge, the one-line description (full text on the title), its state,
// duration and tool-call count; collapsed by default, its own lines rendered
// through the panel's renderer when expanded.
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const entry = await jiti.import("./PolyforgeProgressSubagent.tsx");

const run = (over = {}) => ({
  id: "0123456789abcdef", description: "Read note.txt\nfirst line", agent: "general-purpose", status: "done", startAt: 1_000, durationMs: 1_871,
  lines: [{ id: "a.1.0", kind: "tool", text: "Read: note.txt", tool: { name: "Read", argument: "note.txt", status: "ok" } }, { id: "a.2.0", kind: "narration", text: "SUBAGENT_LINE" }],
  ...over,
});
const render = (props) => renderToStaticMarkup(React.createElement(I18nProvider, null,
  React.createElement(entry.PolyforgeProgressSubagentEntry, { live: true, renderLine: (line) => React.createElement("i", { key: line.id }, line.text), onToggle: () => {}, ...props })));

test("a finished subagent: badge, one-line description with the full text on hover, done, its duration and tool count, collapsed", () => {
  const html = render({ run: run() });
  assert.match(html, /data-pf-progress-kind="subagent"/);
  assert.match(html, /data-pf-progress-subagent-status="done"/);
  assert.match(html, /aria-expanded="false"/);
  assert.match(html, />general-purpose<\/span>/);
  assert.match(html, /title="Read note.txt\nfirst line"/);
  assert.match(html, />Read note.txt first line<\/span>/);
  assert.match(html, />done<\/span>/);
  assert.match(html, />1\.9s<\/span>/);
  assert.match(html, />1 tool calls<\/span>/);
  assert.ok(!html.includes("SUBAGENT_LINE"), "collapsed: its conversation is not rendered");
});

test("a running subagent in a running block is running; in an ended block it is stopped with no invented age; a failed one is failed", () => {
  assert.match(render({ run: run({ status: "running", durationMs: undefined }) }), /data-pf-progress-subagent-status="running"/);
  const stopped = render({ run: run({ status: "running", durationMs: undefined }), live: false });
  assert.match(stopped, /data-pf-progress-subagent-status="stopped"/);
  assert.match(stopped, />stopped<\/span>/);
  assert.ok(!/>\d+\.\ds<\/span>/.test(stopped), "no duration for a subagent that never finished");
  assert.match(render({ run: run({ status: "error" }) }), />failed<\/span>/);
  assert.match(render({ run: run({ agent: undefined }) }), />subagent<\/span>/, "no agent name: the generic badge");
});

test("an omitted subagent (over the job bound) says so when expanded instead of 'nothing recorded'", () => {
  const source = readFileSync(new URL("./PolyforgeProgressSubagent.tsx", import.meta.url), "utf8");
  assert.match(source, /t\(run\.omitted \? "polyforge\.progress\.subagentOmitted" : "polyforge\.progress\.subagentEmpty"\)/);
});
