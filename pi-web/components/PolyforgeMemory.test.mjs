import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const source = await readFile(new URL("./PolyforgeMemory.tsx", import.meta.url), "utf8");
const enSource = await readFile(new URL("../lib/i18n/messages/en.ts", import.meta.url), "utf8");

// ---- polyforge#120 u3 (r2#2): the attribution renders, through the real
// English table — renderToStaticMarkup over the extracted display pieces
// (the TabBar pattern), never a regex over the source. ----
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { MemoryRowAttribution, MemoryDetailAttribution, MemoryWriterFilterSelect } = await jiti.import("./PolyforgeMemory.tsx");
const en = await jiti.import("@/lib/i18n/messages/en");
const t = (key) => en.enLocale.messages[key] ?? key;
const render = (component, props) => renderToStaticMarkup(React.createElement(component, props));
const attributedHit = { id: "mem_e2eRow00001", type: "doc.note", title: "t", visibility: "project", status: "active",
  tags: [], related: [], work_item_id: null, created_at: "c", updated_at: "u", content_len: 1,
  author_display_name: 'E2E Ada <img src=x onerror="window.__pwned=1">', writer_kind: "step",
  writer_details: { work_item: "wi_e2e", step: "build", attempt: 1, model: "pi:e2e" } };
const writerlessHit = { ...attributedHit, id: "mem_e2eRow00002", author_display_name: "", writer_kind: null, writer_details: {} };

test("the tab is pull-only: no timer-driven fetch, no polling, no storage", () => {
  assert.doesNotMatch(source, /setInterval|setTimeout/, "no timer anywhere");
  assert.doesNotMatch(source, /localStorage|sessionStorage/, "no query, result or selection is persisted");
  const fetches = [...source.matchAll(/fetch\((`[^`]+`|"[^"]+")/g)].map((m) => m[1]);
  assert.ok(fetches.length >= 3, "the scope read, the search and the show read all fetch");
});

test("the body renders through the shared sanitized markdown renderer", () => {
  assert.match(source, /<MarkdownBody>\{detail\.content\}<\/MarkdownBody>/, "the detail's body goes through MarkdownBody");
  assert.match(source, /import \{ MarkdownBody \} from "\.\/MarkdownBody\"/, "the existing renderer, unchanged");
});

test("the collapse is a height clip on the threshold, never a source slice", () => {
  assert.match(source, /detail\.content_len > MEMORY_COLLAPSE_BYTES/, "the collapse decision uses the pure threshold");
  assert.match(source, /maxHeight: COLLAPSE_MAX_HEIGHT_PX, overflowY: "hidden"/, "a CSS maxHeight plus overflow clip");
  assert.doesNotMatch(source, /content\.(slice|substring|substr)\(/, "the markdown source is never sliced");
  assert.match(source, /collapsible && !expanded/, "expanding removes the clip");
  assert.match(source, /setExpanded\(\(value\) => !value\)/, "one toggle flips expand and collapse");
  // Expanding must reveal the whole body, not merely unclip it below the fold.
  assert.match(source, /element\.scrollTop = element\.scrollHeight;/, "expanding scrolls the body's end into view");
});

test("the copy control uses the shared clipboard helper and confirms visibly", () => {
  assert.match(source, /copyText\(detail\.id\)/, "the exact id goes through lib/clipboard");
  assert.match(source, /setCopied\(true\)/);
  assert.match(source, /polyforge\.memory\.copied/, "the confirmation is a translated string");
});

test("work-item references leave through the one callback; a null work item renders no link", () => {
  assert.match(source, /onOpenWorkItem: \(ref: string\) => void/, "the prop is the existing openPfAnchor path");
  assert.match(source, /\{detail\.work_item_id && \(/, "the link renders only for a non-null id");
  assert.match(source, /onClick=\{\(\) => onOpenWorkItem\(detail\.work_item_id as string\)\}/);
  assert.match(source, /openDetail\(relatedId\)/, "a related id opens in place, through the same show read");
});

test("the row attribution renders the author display name and the writer, inertly, with the explicit unknown placeholders (r2#2)", () => {
  // The red the round-2 pair is built on: the extraction itself is absent
  // until the implementation lands (the typeof guard fails first, never a
  // missing-symbol crash).
  assert.equal(typeof MemoryRowAttribution, "function", "MemoryRowAttribution is exported for rendering");
  assert.equal(typeof MemoryDetailAttribution, "function", "MemoryDetailAttribution is exported for rendering");
  assert.equal(typeof MemoryWriterFilterSelect, "function", "MemoryWriterFilterSelect is exported for rendering");
  if (typeof MemoryRowAttribution !== "function") return;
  const html = render(MemoryRowAttribution, { hit: attributedHit, t });
  assert.ok(html.includes("E2E Ada"), `the row shows the author's display name: ${html}`);
  assert.ok(html.includes("step (work_item=wi_e2e, step=build, attempt=1, model=pi:e2e)"), `the row shows the writer string: ${html}`);
  assert.ok(!/<img/.test(html), "a hostile display name never reaches the DOM as markup (Q3: React escapes it)");
  assert.ok(!html.includes("owner_user_id"), "no owner id notion renders");
  const writerless = render(MemoryRowAttribution, { hit: writerlessHit, t });
  assert.ok(writerless.includes("Unknown author"), `a missing display name renders the explicit placeholder: ${writerless}`);
  assert.ok(writerless.includes("Unknown writer"), `a pre-writer row renders the explicit placeholder: ${writerless}`);
});

test("the detail attribution renders the same strings under the Author and Writer labels (r2#2)", () => {
  const html = render(MemoryDetailAttribution, { detail: attributedHit, t });
  assert.ok(html.includes("Author") && html.includes("E2E Ada"), `the detail shows the author: ${html}`);
  assert.ok(html.includes("Writer") && html.includes("step (work_item=wi_e2e, step=build, attempt=1, model=pi:e2e)"), `the detail shows the writer: ${html}`);
  const writerless = render(MemoryDetailAttribution, { detail: writerlessHit, t });
  assert.ok(writerless.includes("Unknown author") && writerless.includes("Unknown writer"), `the detail placeholders: ${writerless}`);
});

test("the writer-kind select renders only while the scope says the pf can filter (r2#2)", () => {
  const html = render(MemoryWriterFilterSelect, { visible: true, value: "session", onChange() {}, t });
  assert.match(html, /<select[^>]*aria-label="Writer kind"/, `the control keeps its accessible name: ${html}`);
  for (const label of ["All writers", "Session (daemon)", "Step (dispatch)"]) {
    assert.ok(html.includes(label), `the ${label} option renders`);
  }
  assert.ok(render(MemoryWriterFilterSelect, { visible: false, value: "", onChange() {}, t }) === "",
    "the control stays absent while the pf cannot filter (Q2 — the display of author and writer never is)");
  assert.ok(render(MemoryWriterFilterSelect, { visible: undefined, value: "", onChange() {}, t }) === "",
    "an absent gate fails closed");
});

test("the only fetch URLs are the two memory routes", () => {
  const urls = [...source.matchAll(/fetch\(`([^`]+)`/g)].map((m) => m[1]);
  for (const url of urls) {
    assert.ok(url.startsWith("/api/polyforge/memory?cwd=")
      || url.startsWith("/api/polyforge/memory?${")
      || url.startsWith("/api/polyforge/memory/${"), `unexpected fetch URL: ${url}`);
  }
  assert.match(source, /encodeURIComponent\(cwd\)/, "the cwd is encoded");
  assert.match(source, /encodeURIComponent\(id\)/, "the memory id is encoded");
});

test("the tab browses by default: the first page is read once the scope is ready, never for an unmapped cwd", () => {
  assert.doesNotMatch(source, /polyforge\.memory\.guidance/, "the keyword guidance state is gone (browse is the default)");
  assert.match(source, /if \(!cwd \|\| !scope\?\.mapped \|\| !scope\.capable \|\| !project\) return;/,
    "no list read without a mapped, capable scope and a selected project");
  assert.match(source, /readList\(activeQuery, null\);/, "the current mode's first page is read by the effect");
  assert.match(source, /\[cwd, scope, project, type, tags, writerKind, activeQuery, submits\]/,
    "project, type, tags and writer-kind changes and every submit re-issue the current mode's read");
  assert.match(source, /if \(tagFilter && !isMemoryTagList\(tagFilter\)\) return;/, "a half-typed tag list reads nothing");
});

test("load more appends the next keyset page through the pure merge and keeps the list mounted", () => {
  assert.match(source, /params\.set\("after", after\)/, "the cursor travels to the browse face");
  assert.match(source, /mergeMemoryPages\(previous\.rows, items\)/, "the next page is appended with id de-duplication");
  assert.match(source, /onClick=\{\(\) => readList\("", browse\.nextCursor\)\}/, "load more continues the walk");
  assert.match(source, /browse\?\.nextCursor &&/, "load more is offered only while a next cursor exists");
  assert.match(source, /polyforge\.memory\.total", \{ total: browse\.total \}/, "the project total is visible");
  assert.doesNotMatch(source, /scrollTop = 0;\s*\n\s*fetch\(`\/api\/polyforge\/memory\?\$\{params\}`/, "a list read never resets the scroll");
});

test("a submitted keyword switches to the 200-row relevance read; an empty one returns to browse", () => {
  assert.match(source, /setActiveQuery\(keyword\.trim\(\)\);/, "submit decides the mode from the keyword");
  assert.match(source, /if \(query\) \{\s*params\.set\("q", query\);/, "a keyword read carries q (and never a cursor)");
  assert.match(source, /sortMemoryHitsNewestFirst/, "the display order is the pure newest-first sort");
  assert.match(source, /memoryPage\(hits, page\)/, "keyword paging is the pure 20-row slicer");
  assert.match(source, /hits\.length >= MEMORY_RESULT_LIMIT/, "the narrowing notice triggers at the 200 bound");
  assert.match(source, /seq !== listSeqRef\.current/, "only the newest list read lands");
});

test("browse failures and an empty project render translated states", () => {
  for (const key of ["polyforge.memory.loadError", "polyforge.memory.browseEmpty", "polyforge.memory.loadMore", "polyforge.memory.total"]) {
    assert.ok(source.includes(key), `${key} is rendered`);
  }
});

test("the detail's user-facing strings all come from polyforge.memory.* keys", () => {
  const usedKeys = [...source.matchAll(/\bt\("([a-zA-Z.*]+)"/g)].map((m) => m[1]);
  assert.ok(usedKeys.length >= 10, `expected i18n keys, got ${usedKeys.length}`);
  for (const key of usedKeys) {
    assert.ok(key.startsWith("polyforge."), `unnamespaced key: ${key}`);
    if (key.startsWith("polyforge.memory.")) {
      assert.match(enSource, new RegExp(`"${key.replace(/\./g, "\\.")}": "\\S`), `${key} is translated in en`);
    }
  }
});
