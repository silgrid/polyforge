// carries: components/PolyforgeWorkItems.tsx, lib/i18n/messages/en.ts, lib/i18n/messages/zh-CN.ts, lib/i18n/messages/zh-TW.ts
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// source pins of the read-only Work › 工单列表 surface.
const source = await readFile(new URL("./PolyforgeWorkItems.tsx", import.meta.url), "utf8");
const jiti = createJiti(import.meta.url);
const { getLocalePlugin } = await jiti.import("../lib/i18n/registry.ts");
const locales = Object.fromEntries(["en", "zh-CN", "zh-TW"].map((id) => [id, getLocalePlugin(id).messages]));

test("reads follow gestures only: the two GET routes, one mount read, one detail read per selection, no timer, storage or write", () => {
  const urls = [...source.matchAll(/fetch\((`[^`]+`|"[^"]+")/g)].map((m) => m[1]);
  assert.deepEqual(urls, ['"/api/polyforge/work-items"', "`/api/polyforge/work-items/${encodeURIComponent(id)}`"], "only the two GET work-item routes");
  assert.doesNotMatch(source, /method:/, "every fetch is a plain GET");
  assert.doesNotMatch(source, /setInterval|setTimeout|requestAnimationFrame/, "no timer, no polling");
  assert.doesNotMatch(source, /localStorage|sessionStorage|history\.|location\./, "nothing persisted");
  assert.match(source, /if \(!isPolyforgeWorkItemId\(id\)\) return;/, "a malformed id is never fetched");
  assert.match(source, /if \(listReadRef\.current\) return;\s*listReadRef\.current = true;\s*readList\(\);/, "exactly one list read per opening");
  assert.equal((source.match(/readList\(\);/g) ?? []).length, 2, "the list is read at mount and on a count-key change, nowhere else");
  // owner-intended change: the third read is the shared ↻ action's
  // click (the contract); the action itself lives in the shared
  // search bar, so this source carries onClick={readList}, not readList().
  assert.match(source, /<PolyforgeSearchBar[\s\S]*<PolyforgeRefreshButton refreshing=\{listRefreshing\} onClick=\{readList\}/,
    "the manual re-read renders in the shared action slot");
  assert.doesNotMatch(source, /setInterval|setTimeout|requestAnimationFrame/, "the ↻ adds no timer");
  for (const verb of ["cancel", "claim", "create", "edit", "close"]) {
    assert.doesNotMatch(source, new RegExp(`polyforge\\.workItems\\.${verb}`), `no ${verb} control`);
  }
});

test("one re-read per local count-key change; the mount value and a missing snapshot never read", () => {
  assert.match(source, /itemCountKey: string \| null;/);
  const start = source.indexOf("const countKeyRef = useRef(itemCountKey);");
  assert.ok(start >= 0, "the ref remembers the mount key");
  const effect = source.slice(start, source.indexOf("}, [itemCountKey, readList]);", start));
  assert.match(effect, /const previous = countKeyRef\.current;\s*countKeyRef\.current = itemCountKey;/);
  assert.match(effect, /if \(previous === null \|\| itemCountKey === null \|\| previous === itemCountKey\) return;\s*readList\(\);/);
  const read = source.slice(source.indexOf("const readList = useCallback("), source.indexOf("}, [t, onRunningCount]);"));
  assert.match(read, /if \(seq !== listSeqRef\.current\) return;/, "only the newest read writes the list");
  assert.match(read, /onRunningCount\(workItemsRunningCount\(Array\.isArray\(body\.items\) \? body\.items : \[\]\)\);/, "every list answer reports the badge count");
});

test("rows: newest first, total, exact-500 hint on the header and option, whole-row button, CSS-only goal clip, no virtualisation", () => {
  assert.match(source, /const rows = sortWorkItemsNewestFirst\(/);
  assert.match(source, /const shown = filterWorkItemRows\(rows, \{ search, project: projectFilter, status: statusFilter \}\);/, "one combined client predicate");
  assert.match(source, /t\("polyforge\.workItems\.total", \{ count:/);
  assert.match(source, /const capped = workItemsAtCap\(rows\);/);
  assert.match(source, /capped\.size > 0 && <span[^>]*>\{capHint\}/, "the header carries the hint");
  assert.match(source, /capped\.has\(project\) \? `\$\{project\} · \$\{capHint\}` : project/, "the capped project's option carries the hint");
  assert.match(source, /\{shown\.map\(\(row\) => \(\s*<button key=\{row\.id\} type="button" data-pf-work-item=\{row\.id\} onClick=\{\(\) => openDetail\(row\.id\)\}/,
    "every shown row renders as one whole-row button");
  for (const field of ["row.slug", "statusLabel(row.status)", "row.priority", "row.bundle_name || \"—\"", "row.project", "row.goal"]) {
    assert.ok(source.includes(`{${field}`), `the row shows ${field}`);
  }
  assert.match(source, /whiteSpace: "nowrap", minWidth: 0 \}\}>\{row\.goal \?\? ""\}<\/span>/, "the goal is clipped to one line by CSS");
  assert.doesNotMatch(source, /\.slice\(|\.substring\(|react-window|react-virtual|IntersectionObserver|loadMore/, "no code-point cut, virtualisation or paging");
  assert.doesNotMatch(source, /row\.content|\.content\)/, "the list never touches content");
});

test("filters: 全部状态 + the seven statuses, 全部项目 + the loaded projects; changes only set state", () => {
  assert.match(source, /<option value="all">\{t\("polyforge\.workItems\.filterAllStatuses"\)\}<\/option>\s*\{POLYFORGE_WORK_ITEM_STATUSES\.map/);
  assert.match(source, /<option value="">\{t\("polyforge\.workItems\.filterAllProjects"\)\}<\/option>\s*\{projects\.map/);
  assert.match(source, /const projects = workItemProjectOptions\(rows\);/);
  // owner-intended change: the input lives in the shared search bar,
  // which takes the setter directly; the search state and semantics are kept.
  assert.match(source, /<PolyforgeSearchBar[\s\S]*value=\{search\}[\s\S]*onChange=\{setSearch\}/,
    "the search box renders through the shared search bar with the same state");
  assert.match(source, /onChange=\{\(event\) => setStatusFilter\(event\.target\.value\)\}/);
  assert.match(source, /onChange=\{\(event\) => setProjectFilter\(event\.target\.value\)\}/);
});

test("detail: shows below the list, every field, the waiting line and the capped body; back reads nothing", () => {
  // owner-intended change (was "replaces the list"): the list
  // stays and the detail is handed to renderDetail — Work's full-width lower
  // part — with the open state reported while it shows.
  assert.doesNotMatch(source, /if \(selected\) \{/, "a selection no longer returns the detail in place of the list");
  assert.match(source, /const detailView = selected \? \(/);
  assert.match(source, /\{detailView && renderDetail\(detailView\)\}\s*<\/div>\s*\);\s*\}\s*$/, "the list view renders, with the detail handed to renderDetail");
  assert.match(source, /onDetailOpenChange\?\.\(selected !== null\);\s*return \(\) => onDetailOpenChange\?\.\(false\);/, "the open state is reported, and cleared on unmount");
  for (const field of ["slug", "status", "priority", "bundle", "project", "labels", "milestone", "reporter", "assignee", "created", "updated", "closed", "waitingFor"]) {
    assert.ok(source.includes(`t("polyforge.workItems.field.${field}")`), `the detail labels ${field}`);
  }
  assert.match(source, /detail\?\.waiting_for\?\.length\s*\? detail\.waiting_for\.map/, "waiting_for blockers when present");
  assert.match(source, /: detail\?\.waiting_on \?\? null;/, "else waiting_on");
  assert.match(source, /whiteSpace: "pre-wrap", overflowWrap: "anywhere" \}\}>\{detail\.goal\}<\/div>/, "the full goal, unclipped");
  assert.match(source, /const content = detail\?\.content \?\? "";/, "null content renders an empty body");
  assert.match(source, /isMemoryBodyTruncated\(content, detail\.content_len\)/);
  assert.match(source, /detail\.content_len > MEMORY_COLLAPSE_BYTES/);
  assert.match(source, /<MarkdownBody>\{content\}<\/MarkdownBody>/);
  const back = source.slice(source.indexOf("const back = () => {"), source.indexOf("const notice ="));
  assert.doesNotMatch(back, /fetch|readList|setSearch|setProjectFilter|setStatusFilter/, "back keeps filters/search and reads nothing");
});

test("every new string is translated in en, zh-CN and zh-TW, with the spec's pinned wording", () => {
  const keys = [...new Set([...source.matchAll(/t\(\s*"(polyforge\.workItems\.[A-Za-z.]+)"/g)].map((m) => m[1]))];
  keys.push(...["queued", "running", "paused", "blocked", "wrapped", "failed", "canceled"].map((s) => `polyforge.workItems.status.${s}`));
  keys.push("polyforge.work.itemsTab", "polyforge.work.itemsRunning");
  for (const [id, messages] of Object.entries(locales)) {
    for (const key of keys) assert.ok(typeof messages[key] === "string" && messages[key] && messages[key] !== key, `${id} translates ${key}`);
  }
  assert.equal(locales["zh-CN"]["polyforge.work.itemsTab"], "工单列表");
  assert.equal(locales.en["polyforge.work.itemsTab"], "Work items");
  assert.equal(locales["zh-TW"]["polyforge.work.itemsTab"], "工單列表");
  assert.equal(locales["zh-CN"]["polyforge.workItems.capHint"], "仅显示最近 500 条");
  assert.equal(locales.en["polyforge.workItems.capHint"], "Showing only the latest 500");
  assert.equal(locales["zh-TW"]["polyforge.workItems.capHint"], "僅顯示最近 500 條");
  assert.equal(locales["zh-CN"]["polyforge.work.itemsRunning"], "{count} 个运行中的工单");
  assert.equal(locales.en["polyforge.work.itemsRunning"], "{count} running work items");
  assert.equal(locales["zh-TW"]["polyforge.work.itemsRunning"], "{count} 個執行中的工單");
  assert.equal(locales["zh-CN"]["polyforge.workItems.filterAllStatuses"], "全部状态");
  assert.equal(locales["zh-CN"]["polyforge.workItems.filterAllProjects"], "全部项目");
  assert.equal(locales["zh-CN"]["polyforge.workItems.back"], "返回");
});

// a row pf marks acceptance_missing (a queued execution ticket
// with no acceptance section) shows 「需要交互：缺验收」 beside its status.
test("a row pf marks acceptance_missing shows the needs-interaction label beside its status", () => {
  const status = source.indexOf("{statusLabel(row.status)}");
  assert.ok(status >= 0, "the row shows its status");
  const after = source.slice(status, source.indexOf("</button>", status));
  assert.match(after, /^\{statusLabel\(row\.status\)\}<\/span>\s*\{row\.acceptance_missing && <span[^>]*>\{t\("polyforge\.workItems\.acceptanceMissing"\)\}<\/span>\}/,
    "the label renders right after the status span, only on a marked row");
  assert.equal(locales["zh-CN"]["polyforge.workItems.acceptanceMissing"], "需要交互：缺验收");
  assert.equal(locales.en["polyforge.workItems.acceptanceMissing"], "Needs interaction: no acceptance");
  assert.equal(locales["zh-TW"]["polyforge.workItems.acceptanceMissing"], "需要互動：缺驗收");
});
