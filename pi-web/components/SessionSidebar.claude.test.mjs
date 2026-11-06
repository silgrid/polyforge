import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { HarnessMarker, withClaudeArchiveState } = await jiti.import("./SessionSidebar.tsx");
const source = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");

// Owner intentional change a claude row's marker is the harness only (the
// model can change in-session, so a listed value would go stale), pushed to the right end
// of the title line; the filtered chip stays beside the title.
test("a claude row carries only the `claude` harness marker; a pi row none", () => {
  const html = renderToStaticMarkup(React.createElement(HarnessMarker, { session: { harness: "claude", claude: { model: "claude-sonnet-5-5" } } }));
  assert.match(html, /data-harness="claude"/);
  assert.match(html, /^<span[^>]*>claude<\/span>$/, "the marker's visible text is exactly `claude`");
  assert.doesNotMatch(html, /claude-(?:opus|sonnet|haiku|fable)-/i, "the marker must not show a claude model name");
  assert.doesNotMatch(html, /\b(?:title|aria-label|alt)=/, "the marker must not carry a model tooltip");
  assert.equal(renderToStaticMarkup(React.createElement(HarnessMarker, { session: {} })), "");
  assert.equal(renderToStaticMarkup(React.createElement(HarnessMarker, { session: { harness: "pi", claude: { model: "claude-sonnet-5-5" } } })), "");

  const markerSource = source.slice(source.indexOf("export function HarnessMarker"), source.indexOf("export function withClaudeArchiveState"));
  assert.match(markerSource, /marginLeft: "auto"/, "the marker is pushed to the right end of the title line");
  assert.doesNotMatch(markerSource, /session\.claude|claude-sonnet|claude-opus|claude-haiku|claude-fable/, "the marker's code reads no model");

  // Both row kinds (the archive view and the list) render it as the last element of
  // the title line, immediately before that line's closing div.
  const calls = [...source.matchAll(/<HarnessMarker session=\{session\} \/>/g)];
  assert.equal(calls.length, 2);
  for (const call of calls) {
    const tail = source.slice(call.index + call[0].length);
    assert.match(tail, /^\s*<\/div>/, "the harness marker must be the last element of its title line");
  }
  // The active row keeps the `filtered` chip next to the title, to the left of the marker.
  const activeTitle = source.slice(source.indexOf("{session.transient ?"), calls[1].index);
  assert.match(activeTitle, /\{isFiltered && \([\s\S]*?<\/span>\s*\)\}\s*(?:\{\/\*[\s\S]*?\*\/\}\s*)?$/);
});

test("the archive set mirrors claude rows' index state and leaves pi rows to the browser", () => {
  const current = new Set(["pi-archived", "claude-unarchived"]);
  const sessions = [
    { id: "pi-archived" },
    { id: "pi-live" },
    { id: "claude-archived", harness: "claude", archived: true },
    { id: "claude-unarchived", harness: "claude" },
  ];
  assert.deepEqual([...withClaudeArchiveState(current, sessions)].sort(), ["claude-archived", "pi-archived"]);
  const settled = new Set(["pi-archived", "claude-archived"]);
  assert.equal(withClaudeArchiveState(settled, sessions), settled, "unchanged state keeps the same set");
});

test("archiving a claude row writes the index (PATCH { archived }) instead of the browser set", () => {
  const archive = source.slice(source.indexOf("const archiveSession = useCallback"), source.indexOf("const unarchiveSession = useCallback"));
  assert.match(archive, /session\.harness === "claude"[\s\S]*setClaudeArchived\(id, true\)/);
  const unarchive = source.slice(source.indexOf("const unarchiveSession = useCallback"), source.indexOf("// Persist the archive set"));
  assert.match(unarchive, /setClaudeArchived\(id, false\)/);
  assert.match(source, /method: "PATCH"[\s\S]{0,120}JSON\.stringify\(\{ archived \}\)/);
});
