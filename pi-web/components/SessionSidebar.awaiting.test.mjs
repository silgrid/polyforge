import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// Importing the module is a parse smoke test (same convention as
// SessionSidebar.test.mjs).
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
await jiti.import("./SessionSidebar.tsx");

const source = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");
const en = await readFile(new URL("../lib/i18n/messages/en.ts", import.meta.url), "utf8");
const zhCN = await readFile(new URL("../lib/i18n/messages/zh-CN.ts", import.meta.url), "utf8");
const zhTW = await readFile(new URL("../lib/i18n/messages/zh-TW.ts", import.meta.url), "utf8");

test("awaiting ids arrive on the same two channels as running ids", () => {
  // The 2500 ms poll is the authoritative cadence; the sessions-list fetch
  // only seeds the state before the first poll lands (same fallback guard).
  const pollBlock = source.slice(
    source.indexOf('await res.json() as {', source.indexOf('"/api/agent/running"')),
    source.indexOf("const selectedId = selectedSessionIdRef.current"),
  );
  assert.match(pollBlock, /awaitingSessionIds\?: string\[\]/);
  assert.match(pollBlock, /setAwaitingSessionIds\(new Set\(data\.awaitingSessionIds \?\? \[\]\)\)/);

  const loadBlock = source.slice(
    source.indexOf("const loadSessions = useCallback"),
    source.indexOf("const initialLoadDone = useRef(false)"),
  );
  assert.match(loadBlock, /awaitingSessionIds\?: string\[\]/);
  assert.match(loadBlock, /setAwaitingSessionIds\(new Set\(data\.awaitingSessionIds \?\? \[\]\)\)/);
  // The fallback seed only runs while the poll is not yet authoritative.
  assert.match(loadBlock, /if \(!runningPollAuthoritativeRef\.current\)[\s\S]*?setAwaitingSessionIds/);
});

test("the awaiting badge takes the row indicator slot over the running spinner", () => {
  assert.match(source, /isAwaiting\?: boolean/);
  // Row wiring: the whole family (root + subagents) is checked, matching the
  // running and unread row semantics.
  assert.match(
    source,
    /isRunning=\{familySessions\.some\(\(session\) => runningSessionIds\.has\(session\.id\)\)\}[\s\S]*?isAwaiting=\{familySessions\.some\(\(session\) => awaitingSessionIds\.has\(session\.id\)\)\}/,
  );
  // Display priority: awaiting > running > unread. A claude row's marker names claude's
  // permission prompt, its only awaiting state.
  assert.match(
    source,
    /\{isAwaiting \?\ \(\n\s*<AwaitingInputIndicator permission=\{session\.harness === "claude"\} \/>\n\s*\) : isRunning \?\ \(\n\s*<RunningSessionIndicator \/>\n\s*\) : isUnread \?\ \(\n\s*<UnreadSessionIndicator \/>\n\s*\) : \(\n\s*<span title=\{session\.modified\}>\{formatRelativeTime\(session\.modified, locale\)\}<\/span>\n\s*\)\}/,
  );
});

test("the awaiting marker is a distinct inline indicator with an accessible label", () => {
  const indicator = source.slice(
    source.indexOf("function AwaitingInputIndicator("),
    source.indexOf("function UnreadSessionIndicator()"),
  );
  assert.ok(indicator, "AwaitingInputIndicator is defined in the sidebar's indicator family");
  // A claude row (`permission`) reads 「等待你的授权」, every other row "Awaiting input".
  assert.match(indicator, /const label = permission \? t\("sidebar\.awaitingPermission"\) : t\("sidebar\.awaitingInput"\);/);
  assert.match(indicator, /title=\{label\}/);
  assert.match(indicator, /aria-label=\{label\}/);
});

test("the awaiting state is registry-driven, so closed sessions stay badge-free", () => {
  // The sidebar never derives awaiting locally — the ids come from the API
  // payload only, and the server-side set is restricted to live registry
  // sessions that are running (covered in lib/rpc-manager-awaiting.test.mjs).
  assert.doesNotMatch(source, /setAwaitingSessionIds\(new Set\(\[\.\.\.[^\]]*\]\)\)/);
  for (const messages of [en, zhCN, zhTW]) {
    assert.match(messages, /"sidebar\.awaitingInput": /);
    assert.match(messages, /"sidebar\.awaitingPermission": /);
  }
});
