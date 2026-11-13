import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./PolyforgeMemory.tsx", import.meta.url), "utf8");
const enSource = await readFile(new URL("../lib/i18n/messages/en.ts", import.meta.url), "utf8");

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

test("no author, owner or user id is rendered anywhere, hover titles included", () => {
  assert.doesNotMatch(source, /owner_user_id|author/i, "no owner or author notion in the client");
  // owner-intended change: the search-bar controls keep aria-label +
  // title (the visible field labels are gone). Wire data still carries no
  // hover title.
  assert.doesNotMatch(source, /title=\{(detail|hit)/, "the rows and detail carry no hover titles over wire data");
  for (const used of ["hit.type", "hit.title", "hit.updated_at", "hit.tags", "detail.content", "detail.visibility", "detail.status"]) {
    assert.ok(source.includes(used), `the confirmed fields render: ${used}`);
  }
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
  assert.match(source, /\[cwd, scope, project, type, tags, activeQuery, submits\]/,
    "project, type and tags changes and every submit re-issue the current mode's read");
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
