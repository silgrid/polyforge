import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { stripTypeScriptTypes } from "node:module";
import vm from "node:vm";

const source = await readFile(new URL("./usePolyforge.ts", import.meta.url), "utf8");

function extractPureFunction(name) {
  const start = source.indexOf(`export function ${name}`);
  assert.ok(start >= 0, `${name} must be exported`);
  const end = source.indexOf("\n}", start);
  assert.ok(end > start, `${name} body`);
  return source.slice(start, end + 2);
}

test("a degraded watch response keeps the previous snapshot (selection persistence)", () => {
  const context = vm.createContext({});
  vm.runInContext(
    stripTypeScriptTypes(extractPureFunction("nextWatchSnapshot").replace(/export /g, "")),
    context,
  );
  const snapshot = { wi: "wi_x", slug: "pi#108", workflowStatus: "running", steps: [], events: [] };
  assert.equal(context.nextWatchSnapshot(snapshot, { status: null }), snapshot, "null status keeps the snapshot");
  assert.equal(context.nextWatchSnapshot(snapshot, null), snapshot, "no body keeps the snapshot");
  assert.equal(context.nextWatchSnapshot(snapshot, {}), snapshot, "missing status keeps the snapshot");
  const next = { ...snapshot, workflowStatus: "done" };
  assert.equal(context.nextWatchSnapshot(snapshot, { status: next }), next, "a real snapshot replaces it");
});

test("one fetch at mount decides presence; the other surfaces poll only while pf is present", () => {
  assert.match(source, /useEffect\(\(\) => \{\s*refreshStatus\(\);/);
  assert.match(source, /return \(\) => \{\s*\+\+statusSeq\.current;/);
  assert.match(source, /if \(!probed \|\| !pfPresent\) return;/, "the messages and cards effects gate on presence");
});

test("effect cleanups advance the guard counters; they never rewind to an observation + 1", () => {
  assert.match(source, /return \(\) => \{\s*\+\+watchSeq\.current;\s*clearInterval\(interval\);/);
  assert.doesNotMatch(source, /seqAt\w+ \+ 1/, "a counter reset to seqAtX + 1 rewinds after interval polls");
});

test("cadence: 3s while the panel is open, 30s while closed", () => {
  assert.match(source, /const OPEN_STATUS_POLL_MS = 3000;/);
  assert.match(source, /const CLOSED_STATUS_POLL_MS = 30000;/);
  assert.match(source, /panelOpen \? OPEN_STATUS_POLL_MS : CLOSED_STATUS_POLL_MS/);
});

test("the watch poll runs every 3s while open with a selection", () => {
  assert.match(source, /if \(!panelOpen \|\| !selectedWi\) return;/);
  assert.match(source, /const OPEN_WATCH_POLL_MS = 3000;/);
});

test("the watch URL encodes the ref and stale fetches are dropped by sequence", () => {
  assert.match(source, /\/api\/polyforge\/watch\?wi=\$\{encodeURIComponent\(wi\)\}/);
  assert.match(source, /if \(seq !== statusSeq\.current\) return;/);
  assert.match(source, /if \(seq !== watchSeq\.current\) return;/);
});

test("selecting replaces the snapshot; the running count is null while unknown", () => {
  assert.match(source, /setWatchStatus\(null\);/);
  assert.match(source, /const runningCount = opsStatus \? opsStatus\.queue\.running : null;/, "no badge while the daemon is down");
});

test("pi#113: refreshStatus returns its settle promise for the manual-refresh feedback", () => {
  assert.match(source, /return fetch\("\/api\/polyforge\/status"\)/, "the fetch chain is returned");
  // The promise resolves on both success and failure paths (the .catch arm).
  const start = source.indexOf("const refreshStatus = useCallback");
  const end = source.indexOf("}, []);", start);
  const body = source.slice(start, end);
  assert.match(body, /\.catch\(\(\) => \{/);
  assert.match(body, /return fetch\("\/api\/polyforge\/status"\)/);
});

test("messages reuse the dock cadence and add no timer of their own", () => {
  assert.match(source, /setInterval\(refreshMessages, panelOpen \? OPEN_STATUS_POLL_MS : CLOSED_STATUS_POLL_MS\)/);
  assert.equal((source.match(/const (OPEN|CLOSED)_[A-Z_]+_MS =/g) ?? []).length, 3, "only the existing three cadence constants");
  assert.match(source, /const OPEN_WATCH_POLL_MS = 3000;/, "the open conversation refresh reuses the 3 s tick");
  assert.match(source, /setInterval\(\(\) => refreshOpen\(selectedConversation\), OPEN_WATCH_POLL_MS\)/);
});

test("one acknowledge per advance, naming the newest displayed seq", () => {
  assert.match(source, /import \{[\s\S]*ackDecision,[\s\S]*\} from "@\/lib\/polyforge-messages-pure"/);
  assert.match(source, /newestDisplayedSeq/);
  assert.match(source, /if \(seq <= \(lastAcked\.current\[conversationId\] \?\? 0\)\) return Promise\.resolve\(\);/, "already-acked seq is a no-op");
  assert.match(source, /lastAcked\.current\[conversationId\] = seq;/, "the optimistic stamp prevents a duplicate ack");
  assert.match(source, /if \(!statusReady\) return;/, "the blocked state is judged on this opening's own status read");
  assert.match(source, /explicit: true/, "the explicit gesture bypasses the blocked/visibility gate");
});

test("no message content or analysis is persisted in the browser", () => {
  assert.doesNotMatch(source, /(?:window\.)?(?:localStorage|sessionStorage)\./, "the hook writes neither storage");
  assert.match(source, /if \(!panelOpen\) setAnalyses\(\{\}\);/, "closing the dock drops the transient conclusion");
});

test("paging reads backwards, refresh reads forwards", () => {
  assert.match(source, /\?before=\$\{oldest\}/);
  assert.match(source, /\?after=\$\{newest\}/);
  assert.match(source, /mergeOlderPage\(previous, older\)/);
  assert.match(source, /mergeNewestPage\(previous, newer\)/);
  assert.match(source, /setHasOlder\(body\.has_older === true\)/, "paging ends on page fullness, not seq 1");
  assert.doesNotMatch(source, /page\[0\]\.seq > 1/, "the global AUTOINCREMENT seq is not a paging bound");
  assert.match(source, /blocked,/, "the messages surface receives the blocked state");
});

test("the first settled page is exposed so the panel can position while blocked", () => {
  assert.match(source, /setInitialPageLoaded\(conversationId\)/);
  assert.match(source, /initialPageLoaded: selectedConversation !== null && initialPageLoaded === selectedConversation/);
});

test("w93: a send's answer opens the conversation it names, not a raced list read", () => {
  const start = source.indexOf("const sendMessage = useCallback");
  const end = source.indexOf("}, [refreshMessages, refreshOpen, selectedConversation, selectConversation]);", start);
  const body = source.slice(start, end);
  assert.match(body, /await refreshMessages\(\);/);
  assert.match(body, /const openedId = sent\?\.conversation_id \?\? null;/, "the conversation id comes from the send's own answer");
  assert.match(body, /if \(openedId && openedId !== selectedConversation\) \{\s*selectConversation\(openedId\);/, "a different conversation opens; the open one refreshes");
  assert.doesNotMatch(body, /rows\.find/, "no list-read match to race the polls");
  assert.match(source, /if \(selectedConversation\) \{\s*\/\/ Await the open conversation's refresh/);
});

test("the top-level tabs are memory, messages, work; Messages refresh stays gated on messages", () => {
  assert.match(source, /export type PolyforgeTab = "memory" \| "messages" \| "work";/);
  // (intentional 2026-10-31 third-segment change): 工单列表 joins the sub-tabs.
  // intentional fourth sub-tab: 待集成, `integrate`.
  assert.match(source, /export type PolyforgeWorkTab = "jobs" \| "tasks" \| "items" \| "integrate";/);
  const start = source.indexOf("const setActiveTab = useCallback");
  const end = source.indexOf("}, [refreshMessages]);", start);
  const body = source.slice(start, end);
  assert.match(body, /if \(tab === "messages"\) void refreshMessages\(\);/, "only the messages tab refreshes the hook's list");
  assert.doesNotMatch(body, /memory/, "activating the Memory tab triggers no hook read");
  assert.doesNotMatch(body, /work|tasks/, "activating the Work tab triggers no hook read (the Tasks surface reads on its own gestures)");
});

test("the dock opens on Work › Jobs and the sub-tab lives in page memory only", () => {
  assert.match(source, /useState<PolyforgeTab>\("work"\)/, "Work is the default top-level tab");
  assert.match(source, /const \[workTab, setWorkTab\] = useState<PolyforgeWorkTab>\("jobs"\);/, "Jobs is the default sub-tab");
  assert.doesNotMatch(source, /(?:localStorage|sessionStorage)\.|setItem\(/, "no storage key for workTab (a reload resets it)");
  assert.doesNotMatch(source, /pi-web:polyforge-work/, "no storage key name for the sub-tab");
  assert.match(source, /\n    workTab,\n    setWorkTab,\n/, "the sub-tab state is exposed to the shell");
});

test("the 工单列表 badge count is page memory, null until the first list read, and the hook reads nothing for it", () => {
  assert.match(source, /const \[workItemsRunning, setWorkItemsRunning\] = useState<number \| null>\(null\);/);
  assert.match(source, /\n    workItemsRunning,\n    setWorkItemsRunning,\n/, "the badge state and its setter are exposed to the shell");
  assert.doesNotMatch(source, /\/api\/polyforge\/work-items/, "the hook never reads the work-item list");
});

test("a work-item anchor lands on Work › Jobs with the item selected", () => {
  const start = source.indexOf("const openAnchor = useCallback");
  const body = source.slice(start, source.indexOf("}, [select]);", start));
  assert.match(body, /setActiveTabState\("work"\);\s*setWorkTab\("jobs"\);\s*select\(ref\);/);
});

test("the cards poll is gated on Work › Jobs", () => {
  assert.match(source, /if \(!probed \|\| !pfPresent \|\| !panelOpen \|\| activeTab !== "work" \|\| workTab !== "jobs"\) return;/);
});

test("w93: the list read accepts responses by landing order, not start order", () => {
  // The mount/dock-open/tab burst starts three reads inside one latency
  // window; a start-order guard discarded the first response and blanked
  // the list until the last one landed. Acceptance is now by landing order:
  // a response older than one that already landed is still dropped.
  assert.match(source, /const messagesLandedSeq = useRef\(0\);/);
  assert.match(source, /if \(seq <= messagesLandedSeq\.current\) return null;/);
  assert.match(source, /messagesLandedSeq\.current = seq;/);
  assert.doesNotMatch(source, /seq !== messagesSeq\.current/, "no start-order discard remains for the list read");
});

test("w93: re-selecting a conversation always fetches its newest page (verification F3)", () => {
  // A cached page predates whatever arrived while the user was away; the
  // newest page on open is the base behaviour. The identity guard (below)
  // is the real two-column fix.
  assert.doesNotMatch(source, /pageCache/, "no cached page renders without a fetch");
  const start = source.indexOf("const selectConversation = useCallback");
  const end = source.indexOf("}, [loadNewestPage]);", start);
  const body = source.slice(start, end);
  assert.doesNotMatch(body, /\.get\(conversationId\)/, "no kept-page branch");
  assert.match(body, /setInitialPageLoaded\(null\);\s*void loadNewestPage\(conversationId\);/, "selection clears and fetches the newest page");
});

test("w93: the first list read starts at mount, ungated on the presence probe", () => {
  const start = source.indexOf("Messages use the same interval");
  const end = source.indexOf("}, [probed, pfPresent, panelOpen, refreshMessages]);", start);
  const body = source.slice(start, end);
  assert.match(body, /if \(probed && !pfPresent\) return;\s*refreshMessages\(\);\s*if \(!probed \|\| !pfPresent\) return;/, "the immediate read runs before the probe answers and never after an absent answer; only the interval needs presence");
});

test("w93: page writes guard by conversation identity, not a page-generation counter", () => {
  // Opening Y while X is open must not invalidate Y's own in-flight first
  // page (the old counter's effect-cleanup bump did exactly that); a response
  // from a previous selection still never applies.
  assert.doesNotMatch(source, /pageSeq/, "no page-generation counter remains");
  assert.match(source, /const selectedConversationRef = useRef<string \| null>\(null\);/);
  assert.match(source, /selectedConversationRef\.current = conversationId;/);
  for (const site of ["loadNewestPage", "loadOlder", "refreshOpen"]) {
    const start = source.indexOf(`const ${site} = useCallback`);
    assert.ok(start >= 0, site);
    assert.ok(source.slice(start, start + 2000).includes("selectedConversationRef.current !== conversationId"), `${site} guards by identity`);
  }
});

test("w93: every refreshMessages call starts its own read — no in-flight slot (verification F2)", () => {
  // Sharing one in-flight read made sendMessage's awaited refresh resolve
  // with a list read that could predate the send, so a new conversation's
  // row and header name were missing until the next 3 s poll. Read-your-
  // write needs the post-send refresh to start its own read.
  assert.doesNotMatch(source, /inflightMessagesRef/, "no in-flight slot remains");
  const start = source.indexOf("const refreshMessages = useCallback");
  const end = source.indexOf("}, []);", start);
  const body = source.slice(start, end);
  assert.match(body, /return fetch\(withGroups \? "\/api\/polyforge\/messages" : "\/api\/polyforge\/messages\?groups=0"\)/, "each call fetches on its own");
  assert.doesNotMatch(body, /\.finally\(/, "no slot to clear on settle");
});

test("the project groups ride the one list read — no timer, no storage", () => {
  const start = source.indexOf("const refreshMessages = useCallback");
  const body = source.slice(start, source.indexOf("}, []);", start));
  assert.match(body, /const withGroups = messagesVisibleRef\.current;/, "only a read made while Messages is shown asks for the groups");
  assert.match(body, /if \(withGroups\) setGroups\(Array\.isArray\(body\.groups\) \? body\.groups : \[\]\);/, "such an answer replaces the groups; others keep them");
  assert.match(source, /messagesVisibleRef\.current = messagesVisible;\s*if \(probed && !pfPresent\) return;\s*refreshMessages\(\);/, "showing Messages reads the list at once");
  assert.match(source, /\n    groups,\n/, "the groups are exposed to the shell");
  assert.equal((source.match(/setInterval\(/g) ?? []).length, 5, "status, cards, messages, watch and the open conversation: no new interval");
});

test("the users read runs only on showing Messages, landing-ordered, with no timer", () => {
  assert.match(source, /return fetch\("\/api\/polyforge\/users"\)/);
  assert.match(source, /if \(seq <= usersLandedSeq\.current\) return;/, "a late older read never overwrites a landed one");
  assert.doesNotMatch(source, /useEffect\(\(\) => \{\s*void refreshUsers\(\);\s*\}, \[refreshUsers\]\);/, "no read at mount");
  assert.equal((source.match(/void refreshUsers\(\)/g) ?? []).length, 1, "one read site: the Messages-visible effect");
  assert.match(source, /const messagesVisible = panelOpen && activeTab === "messages";/);
  assert.match(source, /if \(!messagesVisible \|\| \(probed && !pfPresent\)\) return;\s*void refreshUsers\(\);/, "only when the open dock shows Messages");
  assert.doesNotMatch(source, /setInterval\(refreshUsers/, "no timer of its own");
  assert.match(source, /setMembershipAdmin\(body\.admin === true\);/);
  assert.match(source, /useState<boolean \| null>\(null\)/, "unknown until the probe answers");
});

test("a membership change posts once and awaits the list re-read before resolving", () => {
  const start = source.indexOf("const changeMembership = useCallback");
  const body = source.slice(start, source.indexOf("}, [refreshMessages]);", start));
  assert.ok(start > 0);
  assert.match(body, /fetch\("\/api\/polyforge\/projects\/members", \{\s*method: "POST",/);
  assert.match(body, /JSON\.stringify\(\{ action: input\.action, project: input\.project, user_id: input\.userId \}\)/, "no role in the body: add is writer server-side");
  assert.match(body, /throw new Error\(body\.error \?\? "the membership change was refused"\);/, "a refusal throws pf's text");
  assert.ok(body.indexOf("throw new Error") < body.indexOf("await refreshMessages();"), "only a success re-reads");
  assert.match(body, /await refreshMessages\(\);\s*$/, "resolves after the immediate re-read");
});

test("the analyses are structured, page-memory only, and cleared when the dock closes", () => {
  assert.match(source, /const \[analyses, setAnalyses\] = useState<Record<string, PolyforgeAnalysis>>\(\{\}\);/);
  assert.match(source, /parsePolyforgeAnalysis\(body\.analysis\)/, "the hook reads the one structured wire shape");
  assert.match(source, /throw new PolyforgeAnalyzeError\(body\.error \?\? "the analyzer is unavailable", body\.upgrade === true\)/,
    "a refusal carries pf's text and the below-floor marker");
  assert.match(source, /if \(!panelOpen\) setAnalyses\(\{\}\);/, "closing the dock drops every card");
  assert.doesNotMatch(source, /conclusion/, "no retired conclusion is read");
});

test("the decision feed rides every status read that finds pf; the reminder walks waiting jobs, then open entries", () => {
  const start = source.indexOf("const refreshStatus = useCallback");
  const body = source.slice(start, source.indexOf("}, [refreshDecisions]);", start));
  assert.match(body, /if \(body\.pfPresent\) void refreshDecisions\(\);/, "no timer of its own: the status cadence carries it, dock closed too");
  assert.match(source, /await fetch\("\/api\/polyforge\/decisions", \{ signal: controller\.signal \}\)/);
  assert.match(source, /const feed = sanitizePolyforgeDecisionFeed\(body\.feed\);\s*if \(feed\) setDecisionsFeed\(feed\);/, "a failed read keeps the last feed");
  assert.match(source, /const attention: PolyforgeAttentionItem\[\] = attentionItems\(waitingJobs, decisionsFeed\?\.decisions \?\? \[\]\);/);
  assert.match(source, /attentionCount: attention\.length,/);
  assert.match(source, /nextAttentionItem\(attention, \{ lastKey: lastAttentionKey\.current, selectedWi, selectedProfile: selectedCell \}\)/);
  assert.match(source, /fetch\("\/api\/polyforge\/decisions\/dismiss", \{\s*method: "POST",\s*headers: \{ "content-type": "application\/json" \},\s*body: JSON\.stringify\(\{ ids \}\),/);
  // owner ruling 2026-11-09, the walk selects the work item's job detail (nothing else opens), or the task in Tasks.
  const walk = source.slice(source.indexOf("const openNextAttention = useCallback"));
  assert.match(walk, /const target = attentionTarget\(item, opsStatus\?\.jobs \?\? \[\]\);/);
  assert.match(walk, /setWorkTab\("tasks"\);\s*setTaskRequest\(\{ id: target\.id, seq: \+\+taskRequestSeq\.current \}\);/);
  assert.match(walk, /setWorkTab\("jobs"\);\s*if \(target\.ref !== selectedWi \|\| target\.profile !== selectedCell\) select\(target\.ref, target\.profile\);\s*\}, \[/, "the walk selects the job, nothing more");
  assert.match(source, /setWatchStatus\(null\);\s*\}, \[\]\);/, "a new selection starts from a clean snapshot");
});

test("PR #860 review: the decision read is single-flight and bounded; the walk's task request can be cleared", () => {
  assert.match(source, /const refreshDecisions = useMemo\(\(\) => singleFlight\(readDecisions\), \[readDecisions\]\);/, "minor 3: one read at a time (lib singleFlight, behaviour-tested in lib/polyforge-decisions-pure.test.mjs)");
  const read = source.slice(source.indexOf("const readDecisions = useCallback"), source.indexOf("const refreshDecisions = useMemo"));
  assert.match(read, /const timer = setTimeout\(\(\) => controller\.abort\(\), CARD_REQUEST_TIMEOUT_MS\);/, "a hung read frees the slot");
  assert.match(read, /if \(feed\) setDecisionsFeed\(feed\);/, "a failed read keeps the last feed");
  assert.match(source, /const clearTaskRequest = useCallback\(\(\) => setTaskRequest\(null\), \[\]\);/, "minor 1");
  assert.match(source, /\n    clearTaskRequest,\n/);
});
