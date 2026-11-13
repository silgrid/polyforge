import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { readFileSync } from "node:fs";
import test from "node:test";

// Structural wiring tests (pi#83), source-regex style: pin the perf seams —
// the sentinel prefetch margin, the spinner state, the fade scoping, the
// server build cache, and the sidebar hover prefetch.

const chatWindow = await readFile(new URL("./ChatWindow.tsx", import.meta.url), "utf8");
const sidebar = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");
const detailRoute = await readFile(new URL("../app/api/sessions/[id]/route.ts", import.meta.url), "utf8");
const contextRoute = await readFile(new URL("../app/api/sessions/[id]/context/route.ts", import.meta.url), "utf8");
const cacheLib = await readFile(new URL("../lib/context-build-cache.ts", import.meta.url), "utf8");
const prefetchLib = await readFile(new URL("../lib/session-prefetch.ts", import.meta.url), "utf8");

test("the load-earlier sentinel prefetches and shows a spinner", () => {
  // ~1.5 viewport-heights before the sentinel: the fetch starts early.
  assert.match(chatWindow, /rootMargin: "150% 0px 0px 0px"/);
  assert.match(chatWindow, /const \[loadingOlder, setLoadingOlder\] = useState\(false\)/);
  assert.match(chatWindow, /role="status" aria-label=\{t\("chat\.loadingEarlier"\)\}/);
  // The render mirror stays in step with the reentrancy ref.
  assert.match(chatWindow, /loadingOlderRef\.current = false;\s*\n\s*setLoadingOlder\(false\);/);
});

test("only freshly prepended rows fade in, and the class is timed out", () => {
  assert.match(chatWindow, /prependedIds\.has\(entryIds\[idx\]\) \? "chat-row-prepended" : undefined/);
  assert.match(chatWindow, /markPrepended\(context\.entryIds\)/);
  assert.match(chatWindow, /400\)/);
  assert.match(chatWindow, /markPrepended = useCallback/);
});

test("the server build cache keys on the disk fingerprint and bypasses live sessions", () => {
  assert.match(cacheLib, /const fingerprint = fingerprintOf\(filePath\);/);
  assert.match(cacheLib, /if \(!filePath\) return build\(\);/);
  assert.match(cacheLib, /MAX_ENTRIES = 8/);
  // Detail route: live RPC passes "" -> bypass; disk path is cached.
  assert.match(detailRoute, /cachedSessionBuild\(\s*liveRpc \? "" : \(resolvedPath \|\| sm\.getSessionFile\(\) \|\| ""\)/);
  assert.match(detailRoute, /route: "detail"/);
  assert.match(contextRoute, /cachedSessionBuild\(\s*liveRpc \? "" : \(filePath \|\| ""\)/);
  assert.match(contextRoute, /route: "context"/);
});

test("the sidebar prefetched rows feed the view snapshot cache", () => {
  assert.match(sidebar, /prefetchSessionView\(family\.root\.id\)/);
  assert.match(prefetchLib, /setSessionViewSnapshot\(\{/);
  assert.match(prefetchLib, /inflight\.delete\(sessionId\)/);
  // The prefetch request mirrors the mount request's deferral flags.
  assert.match(prefetchLib, /deferThinking: "1", deferMedia: "1", tree: "summary"/);
});

test("the snapshot window-preservation guard requires held messages (pi#83 race)", () => {
  const hook = readFileSync(new URL("../hooks/useAgentSession.ts", import.meta.url), "utf8");
  // The regression: a hover prefetch landed a snapshot mid-flight on a fresh
  // mount; trusting the revision alone served messagesRef.current ([]) and
  // blanked the pane.
  assert.match(hook, /cached\.entryIds\.length >= \(d\.context\.entryIds \?\? \[\]\)\.length\s*&&\s*messagesRef\.current\.length > 0/);
});
