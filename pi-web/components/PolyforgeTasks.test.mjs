import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./PolyforgeTasks.tsx", import.meta.url), "utf8");
const enSource = await readFile(new URL("../lib/i18n/messages/en.ts", import.meta.url), "utf8");

test("the tab reads on gestures only: one list read per opening, one detail read per selection, no poll or storage", () => {
  // the one timer is the block clock — it re-renders and
  // fetches nothing (the spec's own requirement; it replaces "no timer").
  assert.equal((source.match(/setInterval\(/g) ?? []).length, 1, "exactly one interval: the block clock");
  assert.doesNotMatch(source, /setTimeout/, "no other timer");
  assert.match(source, /const clock = setInterval\(\(\) => setNow\(Date\.now\(\)\), 1000\);/, "the clock only reads the time, about once per second");
  assert.doesNotMatch(source, /localStorage|sessionStorage/, "nothing persisted");
  const urls = [...source.matchAll(/fetch\((`[^`]+`|"[^"]+")/g)].map((m) => m[1]);
  assert.deepEqual(urls, ['"/api/polyforge/tasks"', "`/api/polyforge/tasks/${encodeURIComponent(id)}`"], "only the two GET task routes");
  assert.doesNotMatch(source, /method:/, "every fetch is a plain GET");
  assert.match(source, /if \(!isPolyforgeTaskId\(id\)\) return;/, "a malformed id is never fetched");
  assert.match(source, /if \(listReadRef\.current\) return;\s*listReadRef\.current = true;\s*readList\(\);/,
    "exactly one list read per mounted tab, even when mount effects re-run");
  // owner-intended change: the ↻ is the shared bar's action, so its
  // click is onClick={readList}; the three reads are unchanged.
  assert.equal((source.match(/readList\(\);/g) ?? []).length, 2, "the list is read at mount and on a count change, nowhere else");
  assert.match(source, /<PolyforgeRefreshButton refreshing=\{listRefreshing\} onClick=\{readList\} data-pf-tasks-refresh=""/,
    "the refresh button's click is the third read (shared action slot)");
});

test("one re-read per status task-count change while mounted; the mount value and a missing snapshot never read", () => {
  assert.match(source, /taskCountKey: string \| null;/);
  const start = source.indexOf("const countKeyRef = useRef(taskCountKey);");
  assert.ok(start >= 0, "the ref remembers the mount key");
  const effect = source.slice(start, source.indexOf("}, [taskCountKey, readList]);", start));
  assert.match(effect, /const previous = countKeyRef\.current;\s*countKeyRef\.current = taskCountKey;/);
  assert.match(effect, /if \(previous === null \|\| taskCountKey === null \|\| previous === taskCountKey\) return;\s*readList\(\);/);
  const readStart = source.indexOf("const readList = useCallback(");
  const read = source.slice(readStart, source.indexOf("}, [t]);", readStart));
  assert.match(read, /const seq = \+\+listSeqRef\.current;/);
  assert.match(read, /if \(seq !== listSeqRef\.current\) return;/, "only the newest read writes the list");
});

test("an empty list is 暂无任务 plus one line of explanation", () => {
  assert.match(source, /rows\.length === 0 \? \(\s*\/\/[^\n]*\n\s*notice\(t\("polyforge\.tasks\.empty"\), t\("polyforge\.tasks\.emptyHint"\)\)/);
  assert.match(enSource, /"polyforge\.tasks\.emptyHint": "\S/);
});

test("the block clock runs only while a running task is listed, is cleared on unmount and never reads", () => {
  const start = source.indexOf("const [now, setNow] = useState(() => Date.now());");
  assert.ok(start >= 0, "the clock state");
  const effect = source.slice(start, source.indexOf("}, [hasRunning]);", start));
  assert.match(effect, /if \(!hasRunning\) return;/, "no clock without a running block");
  assert.match(effect, /return \(\) => clearInterval\(clock\);/, "cleared on unmount");
  assert.doesNotMatch(effect, /fetch|\/api\//, "the clock performs no I/O");
});

test("the list is a responsive grid of discrete blocks with state dot + text, duration, project and a two-line summary", () => {
  // owner-intended change（重立自 #209）: the Tasks grid has a 150px track
  // minimum. owner-intended change: it is the Tasks grid's own minimum —
  // the job cards are fixed 180px, no longer the same number.
  assert.match(source, /gridTemplateColumns: "repeat\(auto-fill, minmax\(150px, 1fr\)\)", gap: 8/, "the Tasks grid keeps its own 150px column minimum (decoupled from the 180px job cards): two blocks side by side at the 460px dock and the 375px sheet, one at the 340px dock");
  assert.doesNotMatch(source, /minmax\(112px, 1fr\)/, "the old 112px Tasks minimum is gone");
  assert.match(source, /import \{ POLYFORGE_BLOCK_STYLE \} from "@\/lib\/panel-layout";/);
  const row = source.slice(source.indexOf("const renderRow = (row: PolyforgeTaskRow) => {"), source.indexOf("const grid = ("));
  assert.match(row, /<button key=\{row\.id\} type="button" data-pf-task=\{row\.id\}[^>]*onClick=\{\(\) => openDetail\(row\.id\)\}/, "the whole block is the (keyboard-reachable) click target");
  assert.match(row, /\}\}>\{row\.id\}<\/span>/, "the id stays visible as a secondary label");
  assert.match(row, /\.\.\.POLYFORGE_BLOCK_STYLE,/, "the shared block style");
  assert.match(row, /<span aria-hidden="true" style=\{stateDotStyle\(row\.state\)\} \/>/, "the state dot");
  assert.match(row, /\{stateLabel\(row\.state\)\}/, "the localized state text");
  assert.match(row, /const duration = taskDurationSeconds\(row, now\);/);
  assert.match(row, /duration === undefined \? "—" : formatCardDuration\(duration\)/);
  assert.match(row, /const endedAgo = row\.state === "running" \? undefined : taskEndedAgo\(row, now\);/);
  assert.match(row, /t\("polyforge\.tasks\.endedAgo", \{ age: formatTaskAge\(endedAgo\) \}\)/);
  assert.match(row, /\{row\.project \|\| "—"\}/);
  assert.match(row, /WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden"[^}]*\}\}>\{row\.prompt_summary\}/, "the summary is clipped to two lines");
  assert.doesNotMatch(row, /"#[0-9a-fA-F]{3,6}"/, "existing CSS variables only");
  for (const state of ["running", "completed", "capped", "failed", "interrupted"]) {
    assert.match(enSource, new RegExp(`"polyforge\\.tasks\\.state\\.${state}": "\\S`), `en translates the ${state} state`);
  }
});

test("the report tail renders as plain text below the summary, clipped to two lines; placeholders are keyed on state", () => {
  const row = source.slice(source.indexOf("const renderRow = (row: PolyforgeTaskRow) => {"), source.indexOf("const grid = ("));
  const summary = row.indexOf("{row.prompt_summary}</span>");
  const tail = row.indexOf('{row.state === "running" ? (');
  assert.ok(summary >= 0 && tail > summary, "the tail line sits directly below the prompt summary");
  assert.match(row, /\{row\.state === "running" \? \(\s*<span style=\{\{ fontSize: 11, color: "var\(--text-dim\)" \}\}>\{t\("polyforge\.tasks\.tailRunning"\)\}<\/span>/,
    "a running block shows the dim 生成中… whatever its tail holds");
  assert.match(row, /\) : row\.report_tail\?\.trim\(\) \? \(\s*<span style=\{\{ display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden"[^}]*\}\}>\{row\.report_tail\}<\/span>/,
    "a non-running block with a non-blank tail shows the raw string, two-line CSS clamp, hidden overflow");
  assert.match(row, /\) : \(\s*<span style=\{\{ fontSize: 11, color: "var\(--text-dim\)" \}\}>\{t\("polyforge\.tasks\.tailEmpty"\)\}<\/span>\s*\)\}/,
    "an empty, whitespace-only or absent tail shows the dim 暂无报告");
  assert.doesNotMatch(source, /report_tail\??\.(slice|substring|substr)\(/, "the client never cuts the tail");
  assert.doesNotMatch(row, /MarkdownBody|dangerouslySetInnerHTML/, "the tail is plain text, never markdown");
  assert.doesNotMatch(row, /report_tail\}…|\+ "…"/, "no appended ellipsis");
  for (const key of ["tailRunning", "tailEmpty"]) assert.match(enSource, new RegExp(`"polyforge\\.tasks\\.${key}": "\\S`), `en translates ${key}`);
});

test("two sections — running and all — filtered by the client-side search over every section", () => {
  assert.match(source, /splitTaskSections\(filterTaskRows\(rows, \{ search, project: projectFilter, state: stateFilter, reporter: reporterFilter \}\)\)/,
    "the search (and the filters) apply on the client and both sections render the filtered rows");
  assert.match(source, /t\("polyforge\.tasks\.running"\)[\s\S]*grid\(sections\.running\)[\s\S]*t\("polyforge\.tasks\.all"\)[\s\S]*grid\(sections\.all\)/,
    "正在运行 renders above 全部");
  // owner-intended change: the search box renders through the shared
  // search bar; its state and client-side filter are unchanged.
  assert.match(source, /<PolyforgeSearchBar[\s\S]*value=\{search\}[\s\S]*onChange=\{setSearch\}/,
    "the search box is the shared bar's search input");
  for (const field of ["row.project", "row.state", "row.prompt_summary", "row.id"]) assert.ok(source.includes(field), `a row shows ${field}`);
});

test("project and state selects filter the already-read list in component memory, with no read on change", () => {
  assert.match(source, /const \[projectFilter, setProjectFilter\] = useState\(""\);/);
  assert.match(source, /const \[stateFilter, setStateFilter\] = useState<PolyforgeTaskStateFilter>\("all"\);/);
  assert.match(source, /const projects = taskProjectOptions\(rows\);/, "project options come from the loaded list");
  assert.match(source, /<option value="">\{t\("polyforge\.tasks\.filterAllProjects"\)\}<\/option>\s*\{projects\.map/, "全部项目 first, then the projects");
  assert.match(source, /TASK_STATE_FILTERS\.map\(\(state\) => \(\s*<option key=\{state\} value=\{state\}>\{state === "all" \? t\("polyforge\.tasks\.all"\) : t\(`polyforge\.tasks\.state\.\$\{state\}`\)\}<\/option>/);
  for (const handler of [/onChange=\{\(event\) => setProjectFilter\(event\.target\.value\)\}/, /onChange=\{\(event\) => setStateFilter\(event\.target\.value as PolyforgeTaskStateFilter\)\}/]) {
    assert.match(source, handler, "a filter change only sets component state");
  }
  assert.equal((source.match(/fetch\(/g) ?? []).length, 2, "the two task reads stay the only requests");
  assert.doesNotMatch(source, /localStorage|sessionStorage/, "the filters are not persisted");
  assert.doesNotMatch(source, /model|time.?range/i, "no model or time-range filter");
});

test("a reporter select — 全部发起人, humans ascending, 机器发起 last — filters the loaded list in component memory, with no read", () => {
  assert.match(source, /const \[reporterFilter, setReporterFilter\] = useState<string \| null>\(null\);/, "starts at 全部发起人 on every mount");
  assert.match(source, /const reporters = taskReporterOptions\(rows\);/, "options come from the whole loaded list, not the filtered sections");
  assert.match(source, /<select value=\{reporterFilter \?\? ""\} onChange=\{\(event\) => setReporterFilter\(event\.target\.value \|\| null\)\}\s*aria-label=\{t\("polyforge\.tasks\.filterReporter"\)\} title=\{t\("polyforge\.tasks\.filterReporter"\)\}/,
    "a change only sets component state; the select is labelled like the project/state selects");
  assert.match(source, /<option value="">\{t\("polyforge\.tasks\.filterAllReporters"\)\}<\/option>\s*\{reporters\.humans\.map\(\(reporter\) => <option key=\{reporter\} value=\{reporter\}>\{reporter\}<\/option>\)\}\s*\{reporters\.machine && <option value=\{TASK_REPORTER_MACHINE\}>\{t\("polyforge\.tasks\.reporterMachine"\)\}<\/option>\}/,
    "全部发起人 first, the human names, 机器发起 last");
  const selects = source.indexOf("filterAllProjects") < source.indexOf("TASK_STATE_FILTERS.map") && source.indexOf("TASK_STATE_FILTERS.map") < source.indexOf("filterAllReporters");
  assert.ok(selects, "the reporter select sits after the project and state selects");
  assert.equal((source.match(/fetch\(/g) ?? []).length, 2, "still only the two task reads");
  assert.doesNotMatch(source, /localStorage|sessionStorage|useSearchParams|history\.(push|replace)State/, "the reporter selection is not persisted");
  for (const key of ["filterReporter", "filterAllReporters", "reporterMachine"]) assert.match(enSource, new RegExp(`"polyforge\\.tasks\\.${key}": "\\S`), `en translates ${key}`);
});

test("the detail shows status, candidates, budget, times, prompt and the markdown report with the memory collapse rule", () => {
  for (const field of ["detail.state", "detail.candidates", "detail.max_tokens", "detail.tokens_consumed", "detail.max_duration_ns",
    "detail.wall_consumed_ns", "detail.started_at", "detail.ended_at", "detail.prompt"]) {
    assert.ok(source.includes(field), `the detail renders ${field}`);
  }
  assert.match(source, /<MarkdownBody>\{detail\.report\}<\/MarkdownBody>/, "the report renders through the shared markdown renderer");
  assert.match(source, /detail\.report_len > MEMORY_COLLAPSE_BYTES/, "the memory panel's 2000-byte collapse threshold");
  assert.match(source, /maxHeight: COLLAPSE_MAX_HEIGHT_PX, overflowY: "hidden"/, "a height clip, never a source slice");
  assert.doesNotMatch(source, /report\.(slice|substring|substr)\(/);
  assert.match(source, /isMemoryBodyTruncated\(detail\.report, detail\.report_len\)/, "the one truncation rule");
  assert.match(source, /polyforge\.tasks\.truncated/, "the truncation notice");
});

test("the read surface has no write action and every string is translated", () => {
  const code = source.replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, "");
  assert.doesNotMatch(code, /\b(spawn|cancel)\b/i, "no spawn or cancel control or call");
  const usedKeys = [...source.matchAll(/\bt\("([a-zA-Z.]+)"/g)].map((m) => m[1]);
  for (const key of usedKeys) {
    assert.ok(key.startsWith("polyforge."), `unnamespaced key: ${key}`);
    if (key.startsWith("polyforge.tasks.")) {
      assert.match(enSource, new RegExp(`"${key.replace(/\./g, "\\.")}": "\\S`), `${key} is translated in en`);
    }
  }
});

test("an external task id (the analysis card's link) opens that task's existing detail read once", () => {
  assert.match(source, /openTaskId = null,\s*onOpenTaskHandled,/);
  const start = source.indexOf("if (!openTaskId) return;");
  assert.ok(start > 0, "the external open exists");
  const effect = source.slice(start, source.indexOf("}, [openTaskId]);", start));
  assert.match(effect, /openDetail\(openTaskId\);\s*onOpenTaskHandled\?\.\(\);/, "the same one detail read a row click makes, then handed back");
  assert.match(source, /<span data-pf-task=\{selected\}/, "the open detail names its task");
});
