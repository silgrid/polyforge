import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { stripTypeScriptTypes } from "node:module";
import vm from "node:vm";

const source = await readFile(new URL("./PolyforgePanel.tsx", import.meta.url), "utf8");
const panelLayoutSource = await readFile(new URL("../lib/panel-layout.ts", import.meta.url), "utf8");
const enSource = await readFile(new URL("../lib/i18n/messages/en.ts", import.meta.url), "utf8");
const zhCNSource = await readFile(new URL("../lib/i18n/messages/zh-CN.ts", import.meta.url), "utf8");
const zhTWSource = await readFile(new URL("../lib/i18n/messages/zh-TW.ts", import.meta.url), "utf8");

function extractPureFunction(name) {
  const start = source.indexOf(`export function ${name}`);
  assert.ok(start >= 0, `${name} must be exported`);
  const end = source.indexOf("\n}", start);
  assert.ok(end > start, `${name} body`);
  return source.slice(start, end + 2);
}

test("candidate shortening: >24 chars become the first 24 + ellipsis, nothing else changes", () => {
  const context = vm.createContext({});
  vm.runInContext(
    "const CANDIDATE_MAX = 24;\n" + stripTypeScriptTypes(
      extractPureFunction("shortenCandidate").replace(/export /g, "")
      + "\n" + stripTypeScriptTypes(extractPureFunction("formatAge").replace(/export /g, "")),
    ),
    context,
  );
  const long = "claude:claude-opus-5-5-extended-context-e2e";
  assert.equal(context.shortenCandidate(long), `${long.slice(0, 24)}…`);
  assert.equal(context.shortenCandidate("pi:glm-e2e"), "pi:glm-e2e", "24 chars and under pass through");
  assert.equal(context.shortenCandidate("x".repeat(24)), "x".repeat(24));
  assert.equal(context.shortenCandidate("x".repeat(25)), `x`.repeat(24) + "…");
  // Ages render compactly for the job list.
  assert.equal(context.formatAge(61), "1m");
  assert.equal(context.formatAge(7325), "2h 2m");
  assert.equal(context.formatAge(-1), "—");
});

test("job rows show slug and status label; candidate and ages live in the detail; the queue badge stays", () => {
  assert.match(source, /\{job\.slug\}/);
  assert.match(source, /jobStatusLabel\(job\.status\)/);
  const cardSource0 = source.slice(source.indexOf("const PolyforgeJobCard = memo("), source.indexOf("export function PolyforgePanel("));
  assert.doesNotMatch(cardSource0, /job\.spawnN|job\.candidate|job\.stepAgeSec|job\.idleAgeSec/, "the card no longer carries #spawnN, model or ages");
  assert.match(source, /\{selectedJob\.candidate \? shortenCandidate\(selectedJob\.candidate\) : "—"\}/, "the detail shows the model, shortened as the card did");
  assert.match(source, /#\{selectedJob\.spawnN\}/, "the detail keeps the spawn number");
  assert.match(source, /formatAge\(selectedJob\.stepAgeSec\)/);
  assert.match(source, /formatAge\(selectedJob\.idleAgeSec\)/);
  assert.match(source, /\{opsStatus\.queue\.running\} \/ \{opsStatus\.queue\.max\}/, "queue running/max as 2/4");
  // All jobs, no owner filter: the job list renders the sanitized jobs
  // wholesale. (pi#117: the DETAIL now shows the sanitized chat origin —
  // `owner` exists in the panel — so the no-filter guarantee is pinned on
  // the job-list block, not the whole file.)
  const jobListStart = source.indexOf("{/* Queue line + job card stream");
  const jobListEnd = source.indexOf("{/* Detail for the selected work item */}");
  assert.ok(jobListStart >= 0 && jobListEnd > jobListStart, "job list block boundaries");
  const jobList = source.slice(jobListStart, jobListEnd);
  assert.doesNotMatch(jobList, /owner/, "the job list must not filter by owner");
  // every job is a card, rendered in the server's order.
  assert.match(jobList, /shownCards\.map\(\(card: PolyforgeCard\) =>/, "the stream renders every card");
  const cardStart = source.indexOf("const PolyforgeJobCard = memo(");
  assert.ok(cardStart >= 0, "the card component exists");
  const cardSource = source.slice(cardStart, source.indexOf("export function PolyforgePanel("));
  assert.doesNotMatch(cardSource, /owner/, "a card never filters by owner");
  assert.match(cardSource, /const baseStatusLabel = jobStatusLabel\(job\.status\);/);
  assert.match(cardSource, /runningMinutes === null \? baseStatusLabel : `\$\{baseStatusLabel\} \$\{runningMinutes\}m`/, "the running age rides the same label");
});

test("the detail shows the flow graph and status-only blocked rendering, with no step-chip block and no event tail", () => {
  // Graph nodes: the node row maps buildStepGraph(...).nodes; each node is a
  // display-only listitem carrying title+aria-label with full name + status.
  assert.match(source, /const graph = watchStatus\s*\?\s*buildStepGraph\(watchStatus\.steps, watchStatus\.declared_steps, watchStatus\.events\)/, "graph derived from the sanitized snapshot");
  assert.match(source, /graph\.nodes\.map\(\(node, index\) =>/, "node row maps the graph model");
  assert.match(source, /role="listitem"/, "nodes are display-only list items");
  assert.match(source, /aria-label=\{\`\$\{node\.id\} · \$\{stepStatusLabel\(node\.status\)\}\`\}/, "accessible label carries full name + status");
  assert.match(source, /\{shortenCandidate\(node\.id\)\}/, "visible node text is the 24-char cut");
  // owner-intended change（重立自 #209）: the node shows the name and the
  // dot only — the localized state is in title and aria-label (both pinned),
  // never a visible span; skipped is a hollow gray ring, every other status
  // keeps the filled stepNodeColor dot (pending stays filled gray).
  // owner-intended change: the title also carries the step's start time when it has one.
  assert.match(source, /title=\{\`\$\{node\.id\} · \$\{stepStatusLabel\(node\.status\)\}\$\{timing \? \` · \$\{formatEventTime\(timing\.startedAt\)\}\` : ""\}\`\}/, "title carries full name + status (+ start time)");
  assert.match(source, /\{shortenCandidate\(node\.id\)\}\s*<\/span>/, "the node's visible content ends with the shortened name");
  assert.doesNotMatch(source, /<span[^>]*>\{stepStatusLabel\(node\.status\)\}<\/span>/, "no visible localized state label on the node");
  assert.match(source, /style=\{node\.status === "skipped"\s*(?:\/\/[^\n]*\n\s*)?\? \{ width: 7, height: 7, borderRadius: 4, flexShrink: 0, boxSizing: "border-box", background: "transparent", border: "1\.5px solid var\(--text-dim\)" \}\s*: \{ width: 7, height: 7, borderRadius: 4, flexShrink: 0, background: stepNodeColor\(node\.status\) \}\}/, "skipped is a hollow gray ring; every other status keeps the filled stepNodeColor dot");
  assert.match(source, /pending: "var\(--text-dim\)",/, "pending stays the filled gray dot");
  assert.match(source, /className=\{node\.status === "in_progress" \? "pf-step-node-pulse" : undefined\}/, "only in_progress pulses");
  // Sequence connectors between consecutive nodes; dashed rewind chips below.
  assert.match(source, /graph\.rewindEdges\.map\(\(\[gate, target\]\) =>/, "rewind chips render from the model");
  // pi#116: the pre-graph per-step name+status chip block is gone — the
  // graph nodes are the only step STATUS display in every event mode.
  // (pi#117 per-step TIMING — owner-intended change: now a line under
  // each node — shows duration and ×N, never a status label.)
  const timingStart = source.indexOf("data-pf-step-timing={node.id}");
  assert.ok(timingStart >= 0, "the per-step timing lines exist (pi#117), now under the nodes");
  const timingLine = source.slice(timingStart, source.indexOf("</span>", timingStart));
  assert.doesNotMatch(timingLine, /stepStatusLabel|STEP_STATUS_KEYS|role="listitem"|title=/, "timing lines must not render step status labels, nodes or a title");
  // owner-intended change: the standalone event tail is gone — lifecycle
  // events are lines of the execution progress; the watch events only feed
  // the DAG's rewind chips, labelled with the one surviving kind key.
  assert.doesNotMatch(source, /watchStatus\.events\.map|renderEventRow/);
  assert.match(source, /title=\{`\$\{t\("polyforge\.eventKind\.rewind"\)\}: \$\{gate\} → \$\{target\}`\}/, "the rewind chip keeps its label");
  assert.match(source, /workflowStatusLabel\(watchStatus\.workflowStatus\)/, "a blocked WI shows its status");
  // owner-intended change (workflow contract v2, change 14): the question the
  // step waits on is the watch snapshot's pending question (a contract-1
  // question or the current dispatch's open `pf step ask`) and shows highlighted
  // above the card's one input box (PolyforgeJobComposer) — the title row
  // and the rest of the detail never reference it; the
  // discussion assistant and its question panel are gone.
  assert.match(source, /const pendingQuestion = currentPendingQuestion\(watchStatus\);/);
  assert.doesNotMatch(source, /PolyforgeQuestionPanel|PolyforgeQuestionOptions|polyforge-discussion/, "the discussion assistant is deleted");
  assert.match(source, /question=\{pendingQuestion\}/, "the composer receives the pending question");
  assert.equal((source.match(/pendingQuestion/g) ?? []).length, 2, "declared once, read only by the composer");
  assert.match(source, /const conversation = conversationState\(\{ jobStatus: selectedJob\?\.status, workflowStatus: watchStatus\?\.workflowStatus, currentStep: watchStatus\?\.currentStep, blocked: watchStatus\?\.blocked,\s*steps: watchStatus\?\.steps, events: watchStatus\?\.events, entries: selectedItemEntries, daemonPresent: opsStatus !== null \}\);/,
    "the input box's state is the pure rule's: the watch park, its steps and tail (the C1 proof, review r2), the item's feed entries");
  assert.match(source, /<PolyforgeJobComposer\s/, "the composer rides every selected job's detail");
  assert.ok(source.indexOf("<PolyforgeJobComposer") > source.indexOf("data-pf-detail-scroll"), "the composer sits after the detail's scroll region");
});

test("empty, loading, and unavailable states are explicit", () => {
  assert.match(source, /\{t\("polyforge\.unavailable"\)\}/);
  assert.match(source, /\{t\("polyforge\.empty"\)\}/);
  assert.match(source, /\{t\("polyforge\.loading"\)\}/);
  assert.match(source, /\{t\("polyforge\.selectJob"\)\}/);
});

test("the selected detail carries an explicit deselect control (AC6 once the job leaves the list)", () => {
  // The detail header's back button calls onSelect(null) — the only gesture
  // that survives the selected job leaving the list (its row is gone) or the
  // watch snapshot still loading (header hoisted over the watchStatus split).
  assert.match(source, /onClick=\{\(\) => onSelect\(null\)\}/, "detail must have a deselect gesture outside the job rows");
  assert.match(source, /aria-label=\{t\("polyforge\.deselect"\)\}/, "accessible name via polyforge.deselect");
  // owner-intended change: the old detail header became the title row;
  // it still shows the selected ref while the snapshot loads (the slug falls
  // back to the selected job's, then the selection ref) and sits above the
  // watchStatus split, so the stale detail is always dismissable.
  assert.match(source, /const detailSlug = watchStatus\?\.slug \?\? selectedJob\?\.slug \?\? selectedWi;/);
  const titleRow = source.indexOf("data-pf-detail-title");
  assert.ok(titleRow >= 0 && titleRow < source.indexOf("{graph ? (", titleRow), "the title row precedes the watch-gated detail");
  const row = source.slice(titleRow, source.indexOf("\n                            </div>\n", titleRow));
  assert.match(row, /onClick=\{\(\) => onSelect\(null\)\}/, "the deselect control sits in the title row");
  assert.match(row, /\{detailSlug\}/);
  // The key is translated in all three locales (fallback would be a raw key).
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    assert.match(localeSource, /"polyforge\.deselect": "\S/, `${name} must translate polyforge.deselect`);
  }
  // Accessible name must not collide with the topbar button locator, whose
  // name carries "Polyforge" (see e2e/polyforge.mjs pfButton).
  const enMatch = enSource.match(/"polyforge\.deselect": "([^"]+)"/);
  assert.ok(enMatch && !/polyforge/i.test(enMatch[1]), "deselect label must not contain Polyforge");
});

test("the title row shows deselect, slug, the short title (one ellipsized line) and the job status; the rest is on hover", () => {
  const start = source.indexOf("data-pf-detail-title");
  const row = source.slice(start, source.indexOf("\n                            </div>\n", start));
  const order = ["onSelect(null)", "{detailSlug}", "{detailShortTitle}", "{detailStatus}"].map((needle) => row.indexOf(needle));
  assert.ok(order.every((index, i) => index >= 0 && (i === 0 || index > order[i - 1])), `one line in order: ${order}`);
  assert.match(row, /title=\{detailTitleHover \|\| undefined\}/, "the hover carries the facts");
  assert.match(row, /\{detailShortTitle && \(\s*<span style=\{\{ flex: "1 1 0", minWidth: 0, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis"/, "the short title is one ellipsized line, absent without a goal");
  assert.match(source, /const detailShortTitle = shortJobTitle\(detailGoal\);/, "the pure first-clause heuristic, no model");
  assert.match(source, /const detailStatus = selectedJob\s*\? jobStatusLabel\(selectedJob\.status\)\s*: watchStatus \? workflowStatusLabel\(watchStatus\.workflowStatus\) : null;/);
  const hover = source.slice(source.indexOf("const detailTitleHover = ["), source.indexOf('].filter(Boolean).join("\\n");'));
  for (const fact of ["detailGoal", "workflowStatusLabel(watchStatus.workflowStatus)", 't("polyforge.updatedAt")', 't("polyforge.currentStep")']) {
    assert.ok(hover.includes(fact), `the hover carries ${fact}`);
  }
});

test("every user-facing string goes through i18n, with raw fallback for unknown values", () => {
  const usedKeys = [...source.matchAll(/t\("([a-zA-Z.]+)"\)/g)].map((m) => m[1]);
  assert.ok(usedKeys.length >= 4, `expected i18n keys, got ${usedKeys.length}`);
  for (const key of usedKeys) {
    assert.ok(key.startsWith("polyforge."), `unnamespaced key: ${key}`);
  }
  // Unknown statuses pass through raw instead of a missing-key warning.
  assert.match(source, /JOB_STATUS_KEYS\.has\(status\) \? t\(`polyforge\.jobStatus\.\$\{status\}`\) : status/);
  assert.match(source, /STEP_STATUS_KEYS\.has\(status\) \? t\(`polyforge\.stepStatus\.\$\{status\}`\) : status/);
  assert.doesNotMatch(source, /polyforge\.eventKind\.\$\{/, "no per-kind event label remains");
});

test("all three locales carry the polyforge keys in parity", () => {
  const keyOf = (localeSource) => [
    ...localeSource.matchAll(/^\s*"((?:polyforge|bgTasks)\.[a-zA-Z._]+)":/gm),
  ].map((m) => m[1]).filter((key) => key.startsWith("polyforge."));
  const enKeys = keyOf(enSource);
  assert.ok(enKeys.length >= 40, `expected the full polyforge key set in en, got ${enKeys.length}`);
  for (const [name, localeSource] of [["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    const keys = keyOf(localeSource);
    assert.deepEqual(keys, enKeys, `${name} must carry the exact en key set in order`);
    assert.ok(!localeSource.includes("polyforge.queue\": \"Queue\""), `${name} must actually translate, not copy en`);
  }
});







test("the event-mode toggle, its stored key, the command row, the event tail and its helpers are gone; their locale keys retired", () => {
  // owner-intended change (reverses pi#112's toggle/tail and pi#117's command row by ruling).
  for (const gone of ["pi-web:polyforge-event-mode", "EVENT_MODE_STORAGE_KEY", "EventMode", "toggleEventMode", "eventMode",
    "collapseNoiseRuns", "EVENT_NOISE_KINDS", "renderEventRow", "eventValueParts", "formatEventValues", "eventSeverity",
    "EVENT_SEVERITY_COLORS", "EVENT_KIND_KEYS", "EVENT_ARROW_KINDS", "PolyforgeTailRow", "progress?.command", "localStorage"]) {
    assert.ok(!source.includes(gone), `${gone} is gone from the panel`);
  }
  const keyOf = (localeSource) => [...localeSource.matchAll(/^\s*"(polyforge\.[a-zA-Z._]+)":/gm)].map((m) => m[1]);
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    const keys = keyOf(localeSource);
    for (const retired of ["polyforge.eventMode.simple", "polyforge.eventMode.verbose", "polyforge.eventSync", "polyforge.command", "polyforge.emptyEvents"]) {
      assert.ok(!keys.includes(retired), `${name} still ships ${retired}`);
    }
    // workflow contract v2 (change 14): the rewind chip's label plus the
    // owner conversation's four kinds (the ask row, the owner bubble, the
    // delivered and intent status rows).
    assert.deepEqual(keys.filter((key) => key.startsWith("polyforge.eventKind.")).sort(),
      ["polyforge.eventKind.awaiting_owner", "polyforge.eventKind.delivered", "polyforge.eventKind.intent", "polyforge.eventKind.owner_message", "polyforge.eventKind.rewind"],
      `${name} keeps the rewind chip's kind label and the conversation's four`);
    for (const kind of ["step_blocked", "job_done", "job_blocked"]) {
      assert.ok(!localeSource.includes(`polyforge.eventKind.${kind}`), `${name} still ships the dead label ${kind}`);
    }
  }
});

test("pi#113: a manual refresh click spins the icon and disables the button for at least ~400ms", () => {
  // The click path only: guard first, then busy.
  assert.match(source, /const \[refreshBusy, setRefreshBusy\] = useState\(false\);/);
  assert.match(source, /if \(refreshBusy\) return;/, "clicks during the busy window are ignored");
  assert.match(source, /onClick=\{handleRefresh\}/, "the button is wired to the guarded handler");
  // In-flight indication: disabled (assistive tech sees it) + spinning icon.
  assert.match(source, /disabled=\{refreshBusy\}/, "busy is exposed as disabled");
  assert.match(source, /animation: "spin 0\.8s linear infinite"/, "the icon rotates while busy");
  // The window lasts until the fetch settles AND at least 400ms.
  assert.match(source, /setTimeout\(resolve, 400\)/, "~400ms minimum");
  assert.match(source, /Promise\.resolve\(onRefresh\(\)\)\.catch\(\(\) => undefined\)/, "the settle promise is awaited");
  assert.match(source, /Promise\.all\(\[settled, floor\]\)\.then\(\(\) => setRefreshBusy\(false\)\)/, "idle returns after settle+floor, failure included");
  // The busy flag is written only inside the manual click handler — the
  // hook's background polls (interval/mount) never reach it.
  const writes = [...source.matchAll(/setRefreshBusy\(/g)].map((m) => m.index);
  assert.deepEqual(writes.map((index) => source.slice(Math.max(0, source.lastIndexOf("handleRefresh", index)), index).length > 0), [true, true], "both busy writes live in the click handler");
  // The settle promise flows through the prop contract.
  assert.ok(source.includes("onRefresh: () => void | Promise<void>;"), "onRefresh may return a promise");
});

test("pi#114 buildStepGraph: declared_steps drives node order with pending placeholders and declared-only edges", () => {
  const context = vm.createContext({});
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("buildStepGraph").replace(/export /g, "")), context);
  const graph = JSON.parse(JSON.stringify(context.buildStepGraph(
    [
      { name: "spec", status: "done" },
      { name: "grill", status: "done" },
      { name: "code", status: "in_progress" },
    ],
    [{ id: "grill" }, { id: "grill-review" }, { id: "spec" }, { id: "code" }],
    [],
  )));
  // Declaration order wins over started-at order; declared-but-missing id is a pending placeholder.
  assert.deepEqual(graph.nodes.map((n) => [n.id, n.status]), [
    ["grill", "done"],
    ["grill-review", "pending"],
    ["spec", "done"],
    ["code", "in_progress"],
  ]);
  // Sequence edges join consecutive declared steps only.
  assert.deepEqual(graph.sequenceEdges, [["grill", "grill-review"], ["grill-review", "spec"], ["spec", "code"]]);
  assert.deepEqual(graph.rewindEdges, []);
});

test("pi#114 buildStepGraph: on_fail can never become a node or an edge", () => {
  const context = vm.createContext({});
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("buildStepGraph").replace(/export /g, "")), context);
  // Even if a raw declaration sneaks extra fields past the caller, the model
  // keeps id+status only, and non-rewind events produce no edges.
  const graph = JSON.parse(JSON.stringify(context.buildStepGraph(
    [{ name: "grill", status: "done" }],
    [{ id: "grill", on_fail: "spec" }],
    [{ kind: "step_failed", step: "grill", cause: "x" }, { kind: "gate_verdict" }],
  )));
  assert.deepEqual(graph.nodes, [{ id: "grill", status: "done" }]);
  assert.equal(JSON.stringify(graph).includes("on_fail"), false, "on_fail leaked into the graph model");
  assert.deepEqual(graph.sequenceEdges, []);
  assert.deepEqual(graph.rewindEdges, []);
});

test("pi#114 buildStepGraph: null declared_steps falls back to started-at order with started-only edges", () => {
  const context = vm.createContext({});
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("buildStepGraph").replace(/export /g, "")), context);
  for (const declared of [null, []]) {
    const graph = JSON.parse(JSON.stringify(context.buildStepGraph(
      [
        { name: "zulu-grill", status: "done" },
        { name: "mike-spec", status: "done" },
        { name: "alpha-plan", status: "in_progress" },
        { name: "kilo-author", status: "pending" },
        { name: "bravo-code", status: "pending" },
      ],
      declared,
      [],
    )));
    assert.deepEqual(graph.nodes.map((n) => n.id), ["zulu-grill", "mike-spec", "alpha-plan", "kilo-author", "bravo-code"], "nodes follow steps order when declared_steps is null or empty");
    assert.deepEqual(graph.sequenceEdges, [["zulu-grill", "mike-spec"], ["mike-spec", "alpha-plan"]], "only consecutive started steps are edged");
  }
});

test("pi#114 buildStepGraph: mismatch appends extras unedged after the declaration", () => {
  const context = vm.createContext({});
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("buildStepGraph").replace(/export /g, "")), context);
  const graph = JSON.parse(JSON.stringify(context.buildStepGraph(
    [
      { name: "grill", status: "done" },
      { name: "spec", status: "in_progress" },
      { name: "plan", status: "done" },
      { name: "code", status: "done" },
    ],
    [{ id: "grill" }, { id: "spec" }, { id: "ghost-step" }],
    [],
  )));
  assert.deepEqual(graph.nodes.map((n) => [n.id, n.status]), [
    ["grill", "done"],
    ["spec", "in_progress"],
    ["ghost-step", "pending"],
    ["plan", "done"],
    ["code", "done"],
  ]);
  assert.deepEqual(graph.sequenceEdges, [["grill", "spec"], ["spec", "ghost-step"]], "consecutive declared pairs are edged; declared→extra and extra→extra are not");
});

test("pi#114 buildStepGraph: one node per step after a rewind re-run, no attempt counter", () => {
  const context = vm.createContext({});
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("buildStepGraph").replace(/export /g, "")), context);
  const graph = JSON.parse(JSON.stringify(context.buildStepGraph(
    [
      { name: "grill", status: "done" },
      { name: "spec", status: "in_progress" },
      { name: "grill-review", status: "failed" },
      { name: "plan", status: "pending" },
      { name: "code", status: "pending" },
    ],
    ["grill", "spec", "grill-review", "plan", "code"].map((id) => ({ id })),
    [{ kind: "rewind", gate: "grill-review", target: "spec" }],
  )));
  assert.deepEqual(graph.nodes.map((n) => n.id), ["grill", "spec", "grill-review", "plan", "code"], "exactly one node per step");
  assert.ok(graph.nodes.every((n) => Object.keys(n).sort().join(",") === "id,status"), "nodes carry no attempt counter fields");
});

test("pi#114 buildStepGraph: rewind edges need the tail, both endpoints, and dedupe", () => {
  const context = vm.createContext({});
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("buildStepGraph").replace(/export /g, "")), context);
  const steps = [
    { name: "grill", status: "done" },
    { name: "spec", status: "in_progress" },
    { name: "grill-review", status: "failed" },
    { name: "plan", status: "pending" },
    { name: "code", status: "pending" },
  ];
  const declared = steps.map((s) => ({ id: s.name }));
  // Tail-window limitation: a rewind outside the passed tail (the sanitizer
  // only forwards the newest 40) produces no edge.
  const outsideTail = JSON.parse(JSON.stringify(context.buildStepGraph(steps, declared, [])));
  assert.deepEqual(outsideTail.rewindEdges, [], "no tail rewind events → no rewind edges");
  const tail = JSON.parse(JSON.stringify(context.buildStepGraph(steps, declared, [
    { kind: "rewind", gate: "code", target: "plan" },
    { kind: "published" },
    { kind: "rewind", gate: "grill-review", target: "spec" },
    { kind: "rewind", gate: "ghost-gate", target: "spec" },
    { kind: "rewind", gate: "spec", target: "ghost-target" },
    { kind: "rewind", gate: "grill-review", target: "spec" },
  ])));
  assert.deepEqual(tail.rewindEdges, [["code", "plan"], ["grill-review", "spec"]], "only tail rewinds with both endpoints as nodes, deduplicated");
});

test("pi#114 buildStepGraph: fallback edges the whole prefix up to the last non-pending step (post-rewind shape)", () => {
  const context = vm.createContext({});
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("buildStepGraph").replace(/export /g, "")), context);
  // After a pf rewind, [target, gate) is reset to 'pending' while started_at is
  // kept, so pending steps sit inside the started segment. The started segment
  // is the prefix up to and including the LAST non-pending node; edges span
  // that prefix, trailing never-started steps stay unedged.
  const graph = JSON.parse(JSON.stringify(context.buildStepGraph(
    [
      { name: "grill", status: "done" },
      { name: "grill-review", status: "done" },
      { name: "spec", status: "pending" },
      { name: "code", status: "in_progress" },
      { name: "review", status: "in_progress" },
      { name: "verification", status: "pending" },
    ],
    null,
    [],
  )));
  assert.deepEqual(graph.sequenceEdges, [
    ["grill", "grill-review"],
    ["grill-review", "spec"],
    ["spec", "code"],
    ["code", "review"],
  ], "edges span the first five nodes only; the trailing pending step is unedged");
});

test("pi#114 stepNodeColor: explicit gray for pending/cancelled, unknown statuses fall back gray", () => {
  const context = vm.createContext({});
  const colorMap = source.match(/const STEP_STATUS_COLORS: Record<string, string> = \{[\s\S]*?\n\};/);
  assert.ok(colorMap, "STEP_STATUS_COLORS map literal");
  vm.runInContext(stripTypeScriptTypes(colorMap[0]), context);
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("stepNodeColor").replace(/export /g, "")), context);
  assert.equal(context.stepNodeColor("done"), "#16a34a");
  assert.equal(context.stepNodeColor("failed"), "#dc2626");
  assert.equal(context.stepNodeColor("blocked"), "#d97706");
  assert.equal(context.stepNodeColor("in_progress"), "var(--accent)");
  const gray = context.stepNodeColor("pending");
  assert.equal(gray, "var(--text-dim)", "pending is explicit gray");
  assert.equal(context.stepNodeColor("cancelled"), gray, "cancelled is the same gray as pending");
  assert.equal(context.stepNodeColor("paused"), gray, "unknown status falls back to the same gray");
});

test("job status → card frame colour, quiet states share one gray, unknown keeps the block default", () => {
  const context = vm.createContext({});
  const borderMap = source.match(/const JOB_STATUS_BORDER_COLORS: Record<string, string> = \{[\s\S]*?\n\};/);
  assert.ok(borderMap, "JOB_STATUS_BORDER_COLORS map literal");
  vm.runInContext(stripTypeScriptTypes(borderMap[0]), context);
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("jobCardBorderColor").replace(/export /g, "")), context);
  assert.equal(context.jobCardBorderColor("running"), "var(--accent)");
  assert.equal(context.jobCardBorderColor("needs_input"), "#d97706");
  assert.equal(context.jobCardBorderColor("completed"), "#16a34a");
  assert.equal(context.jobCardBorderColor("failed"), "#dc2626");
  const quiet = context.jobCardBorderColor("queued");
  assert.equal(quiet, "var(--text-dim)", "queued is the shared quiet gray");
  assert.equal(context.jobCardBorderColor("paused"), quiet, "paused shares the quiet gray");
  assert.equal(context.jobCardBorderColor("cancelled"), quiet, "cancelled shares the quiet gray");
  // Unknown statuses keep today's block default, never a missing entry.
  assert.equal(context.jobCardBorderColor("blocked"), "var(--border)");
  assert.equal(context.jobCardBorderColor("mystery"), "var(--border)");
  // The four loud states are pairwise distinct and distinct from the quiet gray.
  const loud = [context.jobCardBorderColor("running"), context.jobCardBorderColor("needs_input"), context.jobCardBorderColor("completed"), context.jobCardBorderColor("failed")];
  assert.equal(new Set(loud).size, 4, "the four coloured states are pairwise distinct");
  assert.ok(!loud.includes(quiet) && quiet !== "var(--border)", "quiet gray is distinct from the four and from the default");
});

test("pi#117 buildStepTimings: done = start → next start, in_progress = live age, others = started_at only", () => {
  const context = vm.createContext({});
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("buildStepTimings").replace(/export /g, "")), context);
  const now = Date.parse("2026-10-19T08:13:30Z");
  const steps = [
    { name: "grill", status: "done", startedAt: "2026-10-19T08:00:00Z" },
    { name: "spec", status: "done", startedAt: "2026-10-19T08:05:00Z" },
    { name: "plan", status: "in_progress", startedAt: "2026-10-19T08:10:00Z" },
    { name: "code", status: "pending" },
  ];
  const timings = context.buildStepTimings(steps, now);
  assert.equal(timings.get("grill").durationSec, 300, "done: own start → the next started step's start");
  assert.equal(timings.get("spec").durationSec, 300);
  assert.equal(timings.get("plan").durationSec, 210, "in_progress: live age since start");
  assert.equal(timings.get("grill").startedAt, "2026-10-19T08:00:00Z", "started_at carried for display");
  assert.equal(timings.has("code"), false, "never-started steps carry no timing");
  // The last done step with nothing started after it shows started_at only.
  const last = context.buildStepTimings([
    { name: "grill", status: "done", startedAt: "2026-10-19T08:00:00Z" },
    { name: "code", status: "pending" },
  ], now);
  assert.equal(last.get("grill").durationSec, null, "no next started step → no duration");
  // Failed/blocked started steps: started_at only (no duration rule for them).
  const failed = context.buildStepTimings([
    { name: "review", status: "failed", startedAt: "2026-10-19T08:00:00Z" },
    { name: "code", status: "done", startedAt: "2026-10-19T08:02:00Z" },
  ], now);
  assert.equal(failed.get("review").durationSec, null, "failed started steps show started_at only");
  assert.equal(failed.get("code").durationSec, null, "the trailing done step has no next start");
  // An unparseable started_at degrades that step to no timing, never throws.
  assert.equal(context.buildStepTimings([{ name: "grill", status: "done", startedAt: "not-a-date" }], now).size, 0);
  assert.equal(context.buildStepTimings([], now).size, 0);
});

test("pi#117/#238: the summary carries tool calls, recent files and chat origin (no command row); absent → nothing", () => {
  // The whole block is gated: absent data renders no section, no placeholder.
  assert.match(source, /const detailInfoVisible = progress\?\.tool_calls != null\s*\|\| Boolean\(progress\?\.recent_files\)\s*\|\| originHasText;/, "visibility derives from the sanitized snapshot");
  assert.match(source, /\{detailInfoVisible && \(/, "the facts block renders only when something is present");
  assert.doesNotMatch(source, /progress\??\.command|polyforge\.command/, "owner-intended change: the command row is gone");
  // Tool calls count.
  assert.match(source, /\{progress\?\.tool_calls != null && \(/, "tool calls row renders only when present");
  assert.match(source, /\{progress\.tool_calls\}/, "the count renders");
  // Recent files: one ellipsized mono row per file, full path on hover.
  assert.match(source, /progress\.recent_files\.map\(\(file\) =>/, "one row per recent file");
  assert.match(source, /title=\{file\}/, "full file path on hover");
  // Live age at render time (the watch poll re-renders every 3s): the clock
  // read lives in the module-level helper (the repo's clock-at-render
  // pattern, BackgroundTasksPanel.formatDuration).
  assert.match(source, /function liveStepTimings\(steps: \{ name: string; status: string; startedAt\?: string \}\[\]\): Map<string, StepTiming> \{\s*return buildStepTimings\(steps, Date\.now\(\)\);/, "the live age reads the clock");
  assert.match(source, /const stepTimings = watchStatus \? liveStepTimings\(watchStatus\.steps\) : null;/, "timing derives from the snapshot at render");
  // Origin: the owner as plain text; the row exists only when it is present
  // (the origin carries no chat id any more).
  assert.match(source, /const originHasText = Boolean\(origin\?\.owner\)/);
  assert.doesNotMatch(source, /chat_id/, "no chat id is rendered");
  assert.match(source, /\{origin\.owner && <span style=\{\{ color: "var\(--text\)" \}\}>\{origin\.owner\}<\/span>/, "owner renders as plain text");
  // Nothing in the block is clickable or issues a request: no onClick/fetch.
  const blockStart = source.indexOf("{/* pi#117 facts, moved into the summary");
  const blockEnd = source.indexOf("{/* ② Step flow graph", blockStart);
  assert.ok(blockStart >= 0 && blockEnd > blockStart, "facts block boundaries");
  const block = source.slice(blockStart, blockEnd);
  assert.doesNotMatch(block, /onClick|fetch|href/, "the facts rows must stay display-only");
});

test("pi#117: new detail labels are translated in all three locales", () => {
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    for (const key of ["polyforge.toolCalls", "polyforge.recentFiles", "polyforge.origin", "polyforge.currentStep", "polyforge.jobStart", "polyforge.elapsed", "polyforge.goal.expand", "polyforge.goal.collapse"]) {
      assert.match(localeSource, new RegExp(`"${key.replace(/\./g, "\\.")}": "\\S`), `${name} must translate ${key}`);
    }
    // owner-intended change: the step-timing block left the detail rows for the DAG, so its label is dead.
    assert.ok(!localeSource.includes("polyforge.stepTiming"), `${name} drops the dead polyforge.stepTiming key`);
  }
});

test("pi#119: the detail renders a summary block — line, age, cap marker, error line; absent → nothing", () => {
  const blockStart = source.indexOf("{/* pi#119 summarize narration");
  const blockEnd = source.indexOf("{/* pi#117 facts, moved into the summary", blockStart);
  assert.ok(blockStart >= 0 && blockEnd > blockStart, "summary block boundaries");
  const block = source.slice(blockStart, blockEnd);
  // The block is gated on the sanitized snapshot: when all four fields are
  // absent the block renders nothing at all — no heading, placeholder or
  // loading text.
  assert.match(source, /const summaryVisible = Boolean\(summary \|\| summaryError \|\| summaryCapped\)/, "visibility derives from the four sanitized fields");
  assert.match(source, /\{summaryVisible && \(/, "the summary block renders only when at least one field is present");
  // The line + its age (`Summary · 2h 2m`).
  assert.match(block, /\{t\("polyforge\.summary"\)\}\{summaryAgeSec != null \? ` · \$\{formatAge\(summaryAgeSec\)\}` : ""\}/, "the summary age renders formatAge'd");
  assert.match(block, />\{summary\}</, "the summary line renders verbatim");
  assert.ok(block.includes('wordBreak: "break-word"'), "the summary line wraps (no ellipsis)");
  // A cap-reached marker when summary_capped is true (amber, its own line).
  assert.match(block, /\{summaryCapped && \(/, "the cap marker renders only when capped");
  assert.match(block, /color: "#d97706"/, "the cap marker is amber");
  assert.match(block, /\{t\("polyforge\.summaryCapped"\)\}/, "the cap marker label is translated");
  // summary_error on its own error-styled line; it may show together with
  // an older summary (the rows are independent: no cross-gating).
  assert.match(block, /\{summaryError && \(/, "the error line renders only when present");
  assert.match(block, /color: "#dc2626"/, "the error line is error-styled (red)");
  assert.match(block, /fontFamily: "var\(--font-mono, monospace\)"/, "the error line is mono");
  assert.match(block, />\{summaryError\}</, "the raw error text renders");
  // Nothing in the block is clickable or issues a request.
  assert.doesNotMatch(block, /onClick|fetch|href/, "the summary block must stay display-only");
});

test("pi#120/#238: with a job selected, the whole detail is ONE vertical scroll region", () => {
  // The detail container (the block after the job list) is the single
  // scroller: summary, graph and execution progress scroll together, so
  // every line is reachable at any sheet height and in the desktop dock.
  const detailStart = source.indexOf("{/* Detail for the selected work item");
  assert.ok(detailStart >= 0, "detail block exists");
  const container = source.slice(detailStart, detailStart + 1400);
  assert.match(container, /<div ref=\{detailScrollRef\} data-pf-detail-scroll=""[^>]*overflowY: "auto"/, "the detail container is the one scroller, [data-pf-detail-scroll]");
  // No block inside it scrolls on its own.
  const inside = source.slice(source.indexOf(">", source.indexOf("data-pf-detail-scroll=\"\"")), source.indexOf("{t(\"polyforge.selectJob\")}"));
  assert.doesNotMatch(inside, /overflowY/, "no nested vertical scroller in the detail");
  // The panel's top header (title / refresh / close) stays outside the
  // scroll region and visible: the detail container starts inside the body.
  const bodyStart = source.indexOf("{/* Body */}");
  assert.ok(bodyStart >= 0 && bodyStart < detailStart, "the body split precedes the detail");
  // The job list keeps its own scroller above the detail. owner-intended
  // change: the 45% cap moved from the card stream to Work's
  // top part, which takes its content height (at most 45%) while a detail
  // shows below and the whole height otherwise.
  assert.match(source, /<div style=\{\{ flex: "0 1 auto", minHeight: 0, overflowY: "auto", overflowX: "hidden" \}\}>/, "the job list keeps its own scroller");
  assert.match(source, /data-pf-work-top="" style=\{\{ \.\.\.\(workDetailOpen \? \{ flex: "0 1 auto", maxHeight: "45%" \} : \{ flex: 1 \}\)/, "the top part is at most 45% with a detail below, the whole height without");
  // Horizontal behavior is unchanged: the graph still wraps (no horizontal
  // scroll) — the graph container keeps its overflow-hidden wrap container.
  assert.match(source, /flexWrap: "wrap", alignItems: "center", rowGap: 6/, "the graph node row still wraps");
});

test("the detail opens at its bottom, offers ↓ Latest and folds spawn blocks", () => {
  // Owner-ruled change: the open-at-top reset is gone and
  // the follow starts pinned, so the first stream ends at the whole region's
  // bottom.
  assert.doesNotMatch(source, /detailScrollRef\.current\.scrollTop = 0/);
  assert.match(source, /const pinned = useRef\(true\);/);
  assert.match(source, /streamed && pinned\.current\) region\.scrollTop = region\.scrollHeight/);
  // The floating control is outside the flow, shown only off the bottom, and
  // jumps without an animation.
  assert.match(source, /data-pf-detail-latest=""/);
  assert.match(source, /!atBottom && \(/);
  assert.match(source, /const jumpToLatest = \(\) => \{/);
  assert.match(source, /region\.scrollTop = region\.scrollHeight;\n\s*pinned\.current = true;/);
  assert.match(source, /t\("polyforge\.progress\.latest"\)/);
  // Blocks come from the pure cut and only the newest starts expanded.
  // owner-intended change: a lone block is a block too — it has its group
  // head and folds like any other (the single-block-flat rule is gone).
  assert.match(source, /const layout = progressBlocks\(lines\);/);
  assert.match(source, /const lastBlockId = layout\.blocks\.length > 0 \? lines\[layout\.blocks\[layout\.blocks\.length - 1\]\.start\]\?\.id \?\? null : null;/);
  assert.doesNotMatch(source, /const foldable = |if \(!foldable\) return true;/, "no flat special case for one block");
  assert.match(source, /id === lastBlockId/);
  assert.match(source, /aria-expanded=\{open\}/);
  assert.match(source, /data-pf-progress-block-status=\{block\.status\}/);
  assert.match(source, /data-pf-progress-block-count=\{block\.count\}/);
  assert.match(source, /t\("polyforge\.progress\.lines", \{ count: block\.count \}\)/);
  // Fold state resets per selected job, never persisted.
  assert.match(source, /setFolded\(new Map\(\)\);/);
  assert.doesNotMatch(source, /localStorage.*[Ff]old/);
  // A toggle is measured after the fold, never mistaken for a scroll.
  assert.match(source, /setFoldVersion\(\(current\) => current \+ 1\)/);
  assert.match(source, /if \(foldVersion === 0\) return;/);
});

test("the latest label and line-count wording exist in all three locales", () => {
  const latest = { en: "↓ Latest", "zh-CN": "↓ 最新", "zh-TW": "↓ 最新" };
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    assert.match(localeSource, new RegExp(`"polyforge\\.progress\\.latest": "${latest[name]}"`), `${name} latest label`);
    assert.match(localeSource, /"polyforge\.progress\.lines": "\{count\} \S/, `${name} line-count wording`);
  }
});

test("the dock adds Jobs | Messages tabs with an unread dot of its own", () => {
  assert.match(source, /role="tablist"/);
  // Jobs is Work's sub-tab, its label the same key.
  assert.match(source, /role="tab" aria-selected=\{activeTab === "work"\}/);
  assert.match(source, /role="tab" aria-selected=\{activeTab === "messages"\}/);
  assert.match(source, /t\("polyforge\.messages\.jobsTab"\)/);
  assert.match(source, /t\("polyforge\.messages\.tab"\)/);
  assert.match(source, /messagesSurface\.unreadTotal > 0/);
  assert.match(source, /aria-label=\{t\("polyforge\.messages\.unread"\)\}/, "the dot is named unread");
  assert.match(source, /activeTab === "messages" \? \(\s*<PolyforgeMessages/, "the Messages tab renders the messages surface");
});

test("the Jobs content is unchanged behind the tab switch", () => {
  const jobListStart = source.indexOf("{/* Queue line + job card stream");
  const jobListEnd = source.indexOf("{/* Detail for the selected work item */}");
  assert.ok(jobListStart >= 0 && jobListEnd > jobListStart, "the Jobs body keeps its block markers");
  const jobs = source.slice(jobListStart, jobListEnd);
  assert.match(jobs, /shownCards\.map\(\(card: PolyforgeCard\) =>/);
  assert.match(jobs, /opsStatus\.queue\.running/, "the queue running/max line stays");
});

test("each card has a stable key and test identity, toggles the existing selection, and is a compact key-information card", () => {
  const cardStart = source.indexOf("const PolyforgeJobCard = memo(");
  const card = source.slice(cardStart, source.indexOf("export function PolyforgePanel("));
  assert.match(source, /key=\{polyforgeCardKey\(card\)\}/, "keyed by wi (else slug), never by index");
  assert.match(card, /data-pf-card=\{pending \? undefined : ""\}/);
  assert.match(card, /data-pf-card-wi=\{pending \? undefined : polyforgeCardKey\(card\)\}/);
  assert.match(card, /role="button"/);
  assert.match(card, /tabIndex=\{0\}/);
  // the selection names the card's Cell (equal literals of two Cells are two jobs)
  assert.match(card, /const toggle = \(\) => onSelect\(selected \? null : job\.slug, job\.profile\);/, "a second activation deselects");
  assert.match(card, /event\.key === "Enter" \|\| event\.key === " "/);
  // a pending stand-in has no select handler and takes no
  // pointer (the first card read may reorder it); a real card keeps both.
  assert.match(card, /onClick=\{pending \? undefined : toggle\}/, "a real card selects; a stand-in has no click handler");
  assert.match(card, /onKeyDown=\{pending \? undefined : \(event\) => \{/, "a real card selects by key; a stand-in has no key handler");
  assert.match(card, /aria-disabled=\{pending \|\| undefined\}/);
  assert.match(card, /cursor: pending \? "default" : "pointer", pointerEvents: pending \? "none" : undefined,/);
  assert.doesNotMatch(card, /onClick=\{toggle\}/, "no unconditional click handler remains");
  // isSelected matches the slug ref or the wi id within the selected Cell
  assert.match(source, /selected=\{isSelected\(card\.job\)\}/, "selection is the slug ref, or the wi id an anchor names");
  // one block style, cards side by side, never a full-width
  // row. owner-intended change, re-filed: the Jobs cards are one
  // horizontally scrolling row of fixed-width cards. owner-intended change:
  // that fixed width is 180px and no longer equals the Tasks
  // blocks' 150px column minimum — the two are decoupled (#213 reads on the
  // Tasks side only).
  assert.match(source, /import \{ POLYFORGE_BLOCK_STYLE \} from "@\/lib\/panel-layout";/);
  // owner-intended change: the card's outer column gap is 4 (was 6).
  assert.match(card, /display: "flex", flexDirection: "column", gap: 4, minWidth: 0,/, "the card column gap is 4");
  assert.doesNotMatch(card, /width: "100%"/, "a card is a fixed-width item, not a full-width block");
  assert.match(source, /const JOB_CARD_WIDTH = 180;/, "the fixed card width is 180px, decoupled from the Tasks grid's 150px minimum (owner-intended change)");
  assert.match(card, /flex: `0 0 \$\{JOB_CARD_WIDTH\}px`/, "the card width is a non-shrinking 180px flex basis");
  assert.match(panelLayoutSource, /border: "1px solid var\(--border\)"/, "the shared block keeps its 1px frame width ( changes only the card colour)");
  assert.match(card, /borderColor: jobCardBorderColor\(job\.status\)/, "the card frame colour follows the job's status");
  assert.doesNotMatch(card, /borderWidth|border: `[^`]*\$\{/, "the card changes no border width");
  // owner-intended change: the row opens with the feed-only
  // attention cards, then the live cards.
  assert.match(source, /<div style=\{\{ display: "flex", flexWrap: "nowrap", gap: 8, padding: 8, overflowX: "auto", scrollbarWidth: "thin" \}\}>\s*(?:\{\/\*[\s\S]*?\*\/\}\s*)?\{droppedItems\.map\(\(decision\) => \(\s*<PolyforgeAttentionCard[\s\S]*?\/>\s*\)\)\}\s*\{shownCards\.map/,
    "the Jobs stream is one row that scrolls horizontally, gap and padding unchanged, feed-only attention cards first");
  assert.match(source, /scrollbarWidth: "thin"/, "the scrollbar stays visible but thin");
  assert.doesNotMatch(source, /gridTemplateColumns: "repeat\(auto-fill, minmax\(\d+px, 1fr\)\)"/, "the Jobs stream is no longer a wrapping grid");
  // Title (s14#2): the slug (wrapping) and the status at the
  // right end of the same row, the truncated goal under them clamped to 2
  // lines (full goal on hover). The status may ellipsize (flex 0 1 auto,
  // at most 60%) but never squeezes the slug (flex 1 1 0, wraps anywhere).
  const titleRow = card.slice(card.indexOf('<div style={{ display: "flex", alignItems: "flex-start", gap: 6, minWidth: 0 }}>'), card.indexOf("{goal && ("));
  assert.ok(titleRow.length > 0, "one title row holds slug and status");
  assert.match(titleRow, /<div style=\{\{ flex: "1 1 0", minWidth: 0, [^}]*overflowWrap: "anywhere"[^}]*\}\}>\{job\.slug\}<\/div>/);
  assert.match(titleRow, /\{needsInput \? \(\s*<div data-pf-needs-input=""/, "the needs-input marker sits on the slug's row");
  assert.match(titleRow, /\) : \(\s*<div title=\{fullStatusLabel\} style=\{\{ flex: "0 1 auto", minWidth: 0, maxWidth: "60%"[^}]*\}\}>\{visibleStatusLabel\}<\/div>/, "the dim status label sits on the slug's row and carries the full status in its title");
  assert.match(card, /<span title=\{goal\} style=\{\{[^}]*lineHeight: "16px"[^}]*WebkitLineClamp: 2[^}]*\}\}>\{truncateCardTitle\(goal, titleLength\)\}<\/span>/);
  // the explicit 16px card lines — the slug row and the goal
  // span, so the inherited 18px (line-height 1.5 at 12px) no longer inflates
  // the card; the needs-input badge fits a 16px row (14px line + 1px border)
  // and the dim status label stays 16px.
  assert.match(titleRow, /<div style=\{\{ flex: "1 1 0", minWidth: 0, [^}]*lineHeight: "16px"[^}]*\}\}>\{job\.slug\}<\/div>/, "the slug row is a 16px line");
  assert.match(titleRow, /data-pf-needs-input="" title=\{fullStatusLabel\} style=\{\{[^}]*padding: "0 5px"[^}]*lineHeight: "14px"[^}]*\}\}/, "the needs-input badge is padding 0 5px / line-height 14px, so its row is 16px with the border, and carries the full status title");
  assert.match(titleRow, /\) : \(\s*<div title=\{fullStatusLabel\} style=\{\{ flex: "0 1 auto", minWidth: 0, maxWidth: "60%", fontSize: 11, lineHeight: "16px", color: "var\(--text-dim\)"/, "the dim status label is on a 16px line and carries the full status title");
  assert.ok(card.indexOf("{goal && (") > card.indexOf("{visibleStatusLabel}</div>"), "the goal comes below the slug/status row");
  assert.doesNotMatch(card, /\{!needsInput && \(/, "no separate status row any more");
  assert.match(source, /const \{ titleLength \} = usePolyforgePanelPreferences\(\);/, "the length is the browser-stored preference");
  // Needs input: the status itself as an emphasized marker (amber, bold,
  // framed), replacing the dim label on the title row — from the card's
  // status, no new read.
  assert.match(card, /const needsInput = job\.status === "needs_input";/);
  assert.match(titleRow, /\) : \(\s*<div[^>]*color: "var\(--text-dim\)"[^>]*>\{visibleStatusLabel\}<\/div>/, "a running card keeps the dim label and no marker, showing the duration alone");
  // owner-intended change: the marker is solid amber with white
  // text (was amber text in an amber frame), readable over the card's tint.
  assert.match(titleRow, /\{needsInput \? \(\s*<div data-pf-needs-input="" title=\{fullStatusLabel\} style=\{\{[^}]*border: "1px solid #d97706", background: ATTENTION_AMBER, color: "#fff", fontWeight: 600[^}]*\}\}>\s*\{visibleStatusLabel\}\s*<\/div>/, "the needs-input marker is solid amber and carries the full status title");
  assert.match(source, /const ATTENTION_AMBER = "#d97706";/, "the marker's amber is the existing #d97706");
  assert.match(card, /aria-label=\{\`\$\{job\.slug\} · \$\{fullStatusLabel\}\`\}/, "the card aria-label keeps the slug and the full status text");
  assert.doesNotMatch(card, /aria-label=\{\`\$\{job\.slug\} · \$\{visibleStatusLabel\}\`\}/, "the aria-label never loses the status word to the bare duration");
  const cardRoot = card.slice(card.indexOf("return ("), card.indexOf('<div style={{ display: "flex", flexDirection: "column"'));
  assert.doesNotMatch(cardRoot, /title=/, "no card-level title is added; the goal span keeps its own");
  // No step information on the card: the DAG, durations and reruns live in the detail.
  assert.doesNotMatch(card, /steps\.map|data-pf-step-phase|STEP_PHASE_STYLES|cardStepPhase|step\.runs|currentStep|formatCardDuration/, "no step segment, rerun or duration on the card");
  assert.doesNotMatch(card, /polyforge\.card\.(?:unknownDuration|notStarted|skipped)/, "no unstarted/unknown/skipped step text on the card");
  assert.doesNotMatch(source, /STEP_PHASE_STYLES|cardStepPhase/, "the segment-bar styling is gone");
  assert.doesNotMatch(card, /candidate|stepAge|idleAge/, "model and step/idle age stay off the card");
  // owner-intended change (reverses the 32px narration):
  // the card renders no narration at all — no fixed-height self-scrolling area,
  // no entry list, no placeholders — so it holds only the slug/status row and
  // the goal. The job detail's progress and summary blocks are untouched.
  assert.doesNotMatch(source, /CARD_PROGRESS_HEIGHT/, "the card progress-area height constant is gone");
  assert.doesNotMatch(card, /progressRef|stickToBottom|overflowY: "auto"/, "no card-owned scroll region remains");
  assert.doesNotMatch(card, /const \{ job, goal, progress \} = card;/, "the card no longer reads progress off the card");
  assert.doesNotMatch(card, /progress\.(?:entries|available)|entry\.id/, "no narration entry render on the card");
  assert.doesNotMatch(card, /polyforge\.card\.(?:noProgress|progressUnavailable)/, "the card's narration placeholders are gone");
  // the card-only compaction — vertical padding 4px (the shared
  // POLYFORGE_BLOCK_STYLE keeps 6px, so the Tasks blocks stay as they are) and
  // no gap between the slug/status row and the goal.
  assert.match(card, /padding: "4px 8px"/, "the card's own vertical padding is 4px, horizontal 8px");
  assert.match(card, /display: "flex", flexDirection: "column", gap: 0, minWidth: 0/, "the slug/goal gap is 0");
  assert.doesNotMatch(source, /POLYFORGE_BLOCK_STYLE = \{/, "the shared block style is defined elsewhere, not redefined by the card");
  assert.match(panelLayoutSource, /padding: "6px 8px"/, "the shared POLYFORGE_BLOCK_STYLE keeps its 6px 8px padding, so the Tasks blocks are unchanged");
  // a running job's age in whole minutes comes from the card's
  // existing earliest job_started and a local render clock (no request).
  // owner-intended change: the visible label is that age alone (`23m`,
  // no status word); a missing startedAt falls back to the status word, and
  // the full `<label> <n>m` survives in the title/aria-label.
  assert.match(source, /import \{[\s\S]*?\bcardRunningMinutes,/, "the age comes from the pure cards helper");
  assert.match(card, /const runningMinutes = running \? cardRunningMinutes\(card\.startedAt, now\) : null;/, "only a running card reads an age, from startedAt");
  assert.match(card, /const fullStatusLabel = runningMinutes === null \? baseStatusLabel : `\$\{baseStatusLabel\} \$\{runningMinutes\}m`;/, "the full status text is `<label> <n>m`, bare without a readable age");
  assert.match(card, /const visibleStatusLabel = runningMinutes === null \? baseStatusLabel : `\$\{runningMinutes\}m`;/, "the visible running text is the duration alone, the status word without a readable age");
  assert.match(card, /title=\{fullStatusLabel\}/, "the status element carries the full status text as its title");
  assert.match(card, /const \[now, setNow\] = useState\(\(\) => Date\.now\(\)\);/);
  assert.match(card, /if \(!running\) return;\s*const clock = setInterval\(\(\) => setNow\(Date\.now\(\)\), 30_000\);\s*return \(\) => clearInterval\(clock\);/, "a 30s local render clock, never a poll");
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    assert.doesNotMatch(localeSource, /"polyforge\.card\.(?:noProgress|progressUnavailable)":/, `${name} drops the card-only narration placeholders`);
  }
  // Before the first good card read the status jobs render as status-only
  // stand-ins (same key, no card identity). With a known snapshot of no job
  // the empty state shows at once — never a Loading… flash.
  assert.match(source, /const shownCards = cards \?\? \(opsStatus\?\.jobs \?\? \[\]\)\.map\(\(job\) => statusOnlyCard\(job, !cardsError\)\);/);
  assert.match(source, /pending=\{cards === null\}/);
  assert.match(source, /return \{ job, steps: \[\], progress: \{ available, entries: \[\] \} \};/);
  // owner-intended change: the empty state also needs no feed-only attention card.
  assert.match(source, /\{shownCards\.length === 0 && droppedItems\.length === 0 \? \(\s*<div[^>]*>\{t\("polyforge\.empty"\)\}<\/div>/);
  assert.doesNotMatch(source, /cardsError \? t\("polyforge\.unavailable"\) : t\("polyforge\.loading"\)/, "no Loading… while the status snapshot is known");
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    assert.match(localeSource, /"polyforge\.candidate": "\S/, `${name} labels the detail's model`);
  }
  assert.match(zhCNSource, /"polyforge\.empty": "暂无在跑作业"/);
  assert.doesNotMatch(zhCNSource, /"polyforge\.(?:empty|deselect|selectJob|eventKind\.job_\w+)": "[^"]*任务/, "zh-CN no longer calls jobs 任务");
  assert.doesNotMatch(zhTWSource, /"polyforge\.(?:empty|deselect|selectJob|eventKind\.job_\w+)": "[^"]*工作[^流]/, "zh-TW no longer calls jobs 工作");
});

test("attention cards — a feed-only card per dropped work item, and every attention card amber", () => {
  assert.match(source, /const droppedItems = droppedItemAttention\(decisionsSurface\.feed\?\.decisions \?\? \[\], \[\.\.\.shownCards\.map\(\(card\) => card\.job\), \.\.\.\(opsStatus\?\.jobs \?\? \[\]\)\]\);/,
    "the feed-only items are the open entries of work items with neither a card nor a status job");
  assert.match(source, /const ATTENTION_TINT = "rgba\(217, 119, 6, 0\.16\)";/);
  assert.match(source, /const ATTENTION_TINT_SELECTED = "rgba\(217, 119, 6, 0\.30\)";/);
  const attention = source.slice(source.indexOf("const PolyforgeAttentionCard = memo("), source.indexOf("const PolyforgeJobCard = memo("));
  assert.ok(attention.length > 0, "the feed-only card component exists");
  assert.match(attention, /data-pf-card-wi=\{wi\}/);
  assert.match(attention, /data-pf-card-attention=""/);
  assert.match(attention, /aria-label=\{`\$\{slug\} · \$\{label\}`\}/, "named slug · label");
  assert.match(attention, /borderColor: ATTENTION_AMBER,/, "the amber frame");
  assert.match(attention, /background: selected \? ATTENTION_TINT_SELECTED : ATTENTION_TINT,/, "the amber tint");
  assert.match(attention, /data-pf-attention-marker=\{decision\.class\} title=\{decision\.phrase \?\? label\} style=\{\{[^}]*background: ATTENTION_AMBER, color: "#fff"/, "the solid amber marker, titled with the daemon's phrase");
  assert.match(attention, /if \(selected\) \{\s*onSelect\(null\);\s*return;\s*\}\s*onSelect\(wi, profile\);\s*\};/, "a click selects the item, nothing more; a second click deselects");
  // the entry's reason and 忽略, which dismisses every open
  // entry of the item in one post
  assert.match(attention, /const reason = isKnownDecisionClass\(decision\.class\) \? decision\.phrase : undefined;/, "the reason is a known class's phrase");
  assert.match(attention, /<div data-pf-attention-reason="" title=\{reason\}/, "the reason line, titled with the full phrase");
  assert.match(attention, /data-pf-attention-dismiss=""/, "the card's 忽略");
  assert.match(attention, /event\.stopPropagation\(\);/, "忽略 does not reach the card's toggle");
  assert.match(attention, /onDismiss\(openIds\)/, "忽略 dismisses the item's open entries");
  assert.match(attention, /data-pf-attention-dismiss-error=""/, "a refusal shows on the card");
  assert.match(source, /openIds=\{itemDecisions\(decisionsSurface\.feed\?\.decisions \?\? \[\], \{ wi: decision\.wi, slug: decision\.slug, profile: decision\.profile \}\)\.filter\(isOpenDecision\)\.map\(\(e\) => e\.id\)\}/,
    "the item's open entries, newest first");
  assert.match(source, /function attentionCardLabel\(decision: PolyforgeDecision, t: \(key: string\) => string\): string \{\s*const label = isKnownDecisionClass\(decision\.class\) \? t\(`polyforge\.decisions\.class\.\$\{decision\.class\}`\) : \(decision\.phrase \?\? decision\.class\);\s*const source = decisionSourceShown\(decision\);\s*return source \? `\$\{label\} · \$\{source\}` : label;/,
    "the marker text is the class label, then the shown source");
  const card = source.slice(source.indexOf("const PolyforgeJobCard = memo("), source.indexOf("export function PolyforgePanel("));
  assert.match(card, /data-pf-card-attention=\{needsInput \? "" : undefined\}/, "a needs_input card is an attention card");
  assert.match(card, /background: needsInput\s*\? \(selected \? ATTENTION_TINT_SELECTED : ATTENTION_TINT\)\s*: selected \? "var\(--bg-selected\)" : POLYFORGE_BLOCK_STYLE\.background,/, "tinted amber, the other cards unchanged");
});

test("the summary's facts carry the current step, model + spawn, step/idle age, job start and elapsed, even before the watch read", () => {
  // a selection matches by slug or wi id within the selected Cell
  assert.match(source, /const isSelected = \(job: \{ slug: string; wi: string; profile\?: string \}\) =>\s*\(job\.slug === selectedWi \|\| job\.wi === selectedWi\) && \(selectedProfile === undefined \|\| \(job\.profile \?\? "default"\) === selectedProfile\);/);
  assert.match(source, /const selectedCard = selectedWi\s*\? shownCards\.find\(\(card\) => isSelected\(card\.job\)\)/);
  assert.match(source, /const selectedJob = selectedWi\s*\? \(selectedCard\?\.job \?\? opsStatus\?\.jobs\.find/);
  assert.match(source, /const detailCurrentStep = watchStatus\?\.currentStep \?\? selectedCard\?\.currentStep;/);
  const factsStart = source.indexOf("{/* Job facts (+ #238)");
  const factsEnd = source.indexOf("{/* pi#119 summarize narration", factsStart);
  assert.ok(factsStart >= 0 && factsEnd > factsStart, "the facts sit in the summary, before the narration");
  const facts = source.slice(factsStart, factsEnd);
  assert.doesNotMatch(facts, /watchStatus &&/, "the facts are not gated on the watch read");
  for (const key of ["polyforge.currentStep", "polyforge.candidate", "polyforge.stepAge", "polyforge.idleAge", "polyforge.jobStart", "polyforge.elapsed"]) {
    assert.ok(facts.includes(`t("${key}")`), `the facts carry ${key}`);
  }
  assert.match(facts, /#\{selectedJob\.spawnN\}/, "the spawn number beside the model");
  assert.match(facts, /formatProgressClock\(jobStartMs\)/, "the job start as HH:MM:SS");
  assert.match(facts, /formatAge\(liveElapsedSec\(jobStartMs\)\)/, "the total elapsed time at render");
  assert.match(source, /function liveElapsedSec\(startMs: number\): number \{\s*return \(Date\.now\(\) - startMs\) \/ 1000;/, "the clock-at-render helper, no timer");
});

test("the goal is folded to three lines with an expand/collapse control that resets per job", () => {
  const start = source.indexOf("data-pf-summary-goal");
  const block = source.slice(start, source.indexOf("</button>", start));
  assert.match(block, /title=\{detailGoal\}/, "the full goal on hover too");
  assert.match(block, /: \{ [^}]*display: "-webkit-box", WebkitLineClamp: 3, WebkitBoxOrient: "vertical", overflow: "hidden" \}\}/, "folded: a three-line clamp");
  assert.match(block, /\{detailGoal\}\s*<\/span>/, "the whole goal is in the DOM");
  assert.match(block, /data-pf-goal-toggle=""\s*aria-expanded=\{goalExpanded\}/);
  assert.match(block, /onClick=\{\(\) => setGoalExpandedFor\(goalExpanded \? null : selectedWi\)\}/);
  assert.match(source, /const goalExpanded = goalExpandedFor !== null && goalExpandedFor === selectedWi;/, "another job opens folded");
  assert.match(source, /\{detailGoal && \(/, "no goal, no goal line and no invented title");
});

test("the DAG timing line carries the live +duration and the selected card's ×N reruns, one line under each node", () => {
  // The rerun count comes from the selected card's own read — no new request.
  assert.match(source, /const stepRuns = new Map\(\(selectedCard\?\.steps \?\? \[\]\)\.map\(\(step\) => \[step\.id, step\.runs\]\)\);/);
  // owner-intended change: the timing row moved from the detail rows to a line under its DAG node.
  const nodes = source.slice(source.indexOf("graph.nodes.map((node, index) => {"), source.indexOf("graph.rewindEdges.length > 0 &&"));
  assert.match(nodes, /const timing = stepTimings\?\.get\(node\.id\);/);
  assert.match(nodes, /const runs = stepRuns\.get\(node\.id\) \?\? 0;/);
  assert.match(nodes, /timing && timing\.durationSec != null \? `\+\$\{formatCardDuration\(timing\.durationSec\)\}` : ""/, "the live +duration in the `+3m 05s` form; no duration → nothing");
  assert.match(nodes, /runs > 1 \? `×\$\{runs\}` : ""/, "×N only for a rerun step");
  assert.match(nodes, /<span data-pf-step-timing=\{node\.id\} style=\{\{ height: 14, lineHeight: "14px"[^}]*whiteSpace: "nowrap" \}\}>\{timingText\}<\/span>/, "one fixed-height line, empty when there is no timing, so the nodes stay aligned");
  const item = nodes.indexOf('role="listitem"');
  const line = nodes.indexOf("data-pf-step-timing");
  assert.ok(item >= 0 && line > nodes.indexOf("{shortenCandidate(node.id)}", item), "the timing line is the listitem's sibling, after its name — never inside it");
  assert.doesNotMatch(source, /t\("polyforge\.stepTiming"\)/, "the old detail timing block is gone");
});

test("the tab strip gains a third Memory tab and the body renders the memory surface", () => {
  assert.match(source, /role="tab" aria-selected=\{activeTab === "memory"\}/);
  assert.match(source, /t\("polyforge\.memory\.tab"\)/);
  // The Messages branch stays pinned (the #56 assertion above); the Memory
  // branch slots between it and the Jobs fallback.
  assert.match(source, /activeTab === "messages" \? \(\s*<PolyforgeMessages[\s\S]*\) : activeTab === "memory" \? \(\s*<PolyforgeMemory cwd=\{memoryCwd\} onOpenWorkItem=\{messagesSurface\.onOpenAnchor\} \/>/);
  assert.match(source, /memoryCwd: string \| null;/, "the panel takes the session cwd as a prop");
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    assert.match(localeSource, /"polyforge\.memory\.tab": "\S/, `${name} translates the tab label`);
  }
});

test("the top strip orders Work, Memory, Messages (owner 2026-10-30); Work hosts Jobs and Tasks as sub-tabs", () => {
  const strip = source.slice(source.indexOf('role="tablist" data-pf-tabstrip="top"'), source.indexOf("{/* Body */}"));
  const order = [...strip.matchAll(/aria-selected=\{activeTab === "(\w+)"\}/g)].map((m) => m[1]);
  assert.deepEqual(order, ["work", "memory", "messages"], "three top-level tabs in DOM order");
  assert.match(strip, /t\("polyforge\.work\.tab"\)/);
  assert.doesNotMatch(strip, /polyforge\.tasks\.tab|polyforge\.messages\.jobsTab/, "no Jobs/Tasks top-level tab remains");
  // The unread dot stays its own element beside the Messages label.
  const messagesTab = strip.slice(strip.indexOf('aria-selected={activeTab === "messages"}'));
  assert.match(messagesTab, /t\("polyforge\.messages\.tab"\)\}\s*\{messagesSurface\.unreadTotal > 0 && \(/, "the dot follows the Messages label");
  // Work: the main area renders only the selected sub-tab's surface.
  assert.match(source, /\) : workTab === "tasks" \? \(\s*(?:\/\/[^\n]*\n\s*)*<PolyforgeTasks/, "the Tasks sub-tab renders the tasks surface");
  // (intentional 2026-10-31 third-segment change): 工单列表 renders only the work-item surface.
  // its detail goes to Work's full-width lower part.
  assert.match(source, /\{workTab === "items" \? \(\s*(?:\/\/[^\n]*\n\s*)*<PolyforgeWorkItems itemCountKey=\{itemCountKey\} onRunningCount=\{onWorkItemsRunningChange\} renderDetail=\{renderWorkDetail\} onDetailOpenChange=\{setSubDetailOpen\} \/>/,
    "the 工单列表 sub-tab renders the work-item surface with the count key, the badge reporter and the lower-part detail");
  // the external task open rides beside the count key.
  // the task summaries (decision-feed entries) ride beside it.
  assert.match(source, /<PolyforgeTasks\s+taskCountKey=\{opsStatus \? `\$\{opsStatus\.tasks\?\.running \?\? 0\}:\$\{opsStatus\.tasks\?\.completed \?\? 0\}` : null\}\s+openTaskId=\{taskToOpen\}\s+onOpenTaskHandled=\{clearTaskToOpen\}\s+decisions=\{decisionsSurface\.feed\?\.decisions \?\? \[\]\}\s+onDismissDecisions=\{decisionsSurface\.onDismiss\}\s+renderDetail=\{renderWorkDetail\}\s+onDetailOpenChange=\{setSubDetailOpen\}\s+\/>/,
    "the Tasks surface re-reads on the status snapshot's running:completed key (null without a snapshot)");
  // The sub-tab controls: their own named tablist, after the main area.
  const work = source.slice(source.indexOf('role="tablist"\n              data-pf-tabstrip="work"'), source.indexOf('<div data-pf-work-detail=""'));
  assert.ok(source.indexOf('data-pf-tabstrip="work"') > source.indexOf(") : workTab === \"tasks\" ? ("), "the sub-tab controls follow the main area in the DOM");
  // (intentional 2026-10-31 third-segment change): jobs, tasks, items in that order
  // (the 决策 sub-tab is gone again: owner ruling 2026-11-09, every interaction is the work item's).
  // intentional fourth sub-tab: 待集成 (`integrate`) last.
  assert.match(work, /\(\["jobs", "tasks", "items", "integrate"\] as const\)\.map\(\(tab\) =>/);
  assert.match(work, /role="tab" aria-selected=\{active\}\s*onClick=\{\(\) => onWorkTab\(tab\)\}/);
  assert.match(work, /const label = tab === "jobs" \? t\("polyforge\.messages\.jobsTab"\) : tab === "tasks" \? t\("polyforge\.tasks\.tab"\)\s*: tab === "items" \? t\("polyforge\.work\.itemsTab"\) : t\("polyforge\.work\.integrateTab"\);/,
    "作业 / 任务 / 工单列表 / 待集成 labels ( adds the third; intentional fourth sub-tab)");
  // owner-intended change（重立自 #209）: desktop keeps the ~72px text rail;
  // ≤640px is a 40px vertical icon rail on the right (was a three-segment bar
  // above the main area). owner-intended change: Work is two
  // parts stacked at every width — the top part is the row (main area first
  // in the DOM, the rail beside it on the right), the detail below it spans
  // the whole width.
  assert.match(source, /const isMobile = useIsMobile\(\);/);
  assert.match(source, /<div data-pf-work-layout="" style=\{\{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" \}\}>\s*<div data-pf-work-top="" style=\{\{[^\n]*display: "flex", flexDirection: "row", overflow: "hidden" \}\}>\s*<div data-pf-work-main="" style=\{\{ flex: 1, minWidth: 0, minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden" \}\}>\s*\{workTab === "items"/, "two parts stacked: the top row holds the main area first, the rail beside it on the right");
  const detailPart = source.indexOf('<div data-pf-work-detail=""');
  assert.ok(detailPart > source.indexOf('data-pf-tabstrip="work"'), "the full-width detail part follows the top row (and its rail) in the DOM");
  assert.ok(source.indexOf("data-pf-detail-scroll") > detailPart && source.indexOf("<PolyforgeJobComposer") > source.indexOf("data-pf-detail-scroll"),
    "the job detail and, under it, the input box sit in the full-width part");
  // Tasks and 工单列表 render their detail into the part's slot (a portal), so
  // the part stays mounted and only hides while no detail is open.
  assert.match(source, /const workDetailOpen = workTab === "jobs" \? Boolean\(opsStatus\) : subDetailOpen;/);
  assert.match(source, /workDetailSlot \? createPortal\(detail, workDetailSlot\) : null/);
  assert.match(source, /<div data-pf-work-detail="" style=\{\{ flex: 1, minHeight: 0, display: workDetailOpen \? "flex" : "none",/, "the lower part hides, never unmounts");
  assert.ok(source.indexOf("<div ref={setWorkDetailSlot} data-pf-work-detail-slot") > source.indexOf("<PolyforgeJobComposer"), "the slot sits in the lower part");
  assert.doesNotMatch(source, /column-reverse/, "no bar lifted above the main area any more");
  assert.match(work, /aria-orientation="vertical"/, "the rail is vertical at every width");
  assert.doesNotMatch(work, /aria-orientation=\{isMobile/, "no horizontal orientation on mobile");
  assert.match(work, /\? \{ flex: "0 0 40px", width: 40, display: "flex", flexDirection: "column", borderLeft: "1px solid var\(--border\)", overflowY: "auto" \}\s*: \{ flex: "0 0 72px", width: 72, display: "flex", flexDirection: "column", borderLeft: "1px solid var\(--border\)", overflowY: "auto" \}/, "mobile 40px rail, desktop 72px rail, both on the right");
  assert.doesNotMatch(work, /borderBottom: "1px solid var\(--border\)"/, "no horizontal bar border");
  // Icon-only on mobile, named by the existing tab copy; desktop keeps the visible label.
  assert.match(work, /aria-label=\{isMobile \? label : undefined\}\s*title=\{isMobile \? label : undefined\}/, "the icon-only button is named by its tab copy");
  assert.match(work, /\{isMobile \? WORK_TAB_ICONS\[tab\] : <span>\{label\}<\/span>\}/, "icon on mobile, visible label on desktop");
  assert.match(work, /\.\.\.\(isMobile \? \{ width: "100%", height: 40, padding: 0 \} : \{ padding: "10px 4px" \}\)/, "about 40×40 mobile buttons");
  assert.match(work, /display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center"/, "the badge sits under the icon/label");
  assert.match(source, /const workTabIcon = \(paths: ReactNode\) => \(\s*<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">/, "16px icons in the panel's 24-viewBox stroke style");
  // intentional fourth sub-tab: 待集成 has its rail icon too.
  for (const tab of ["jobs", "tasks", "items", "integrate"]) assert.match(source, new RegExp(`\\n  ${tab}: workTabIcon\\(`), `${tab} has a rail icon`);
  assert.match(work, /background: active \? "var\(--bg-selected\)" : "none"/);
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    assert.match(localeSource, /"polyforge\.tasks\.tab": "\S/, `${name} translates the Tasks label`);
    assert.match(localeSource, /"polyforge\.work\.tab": "\S/, `${name} translates the Work label`);
  }
  assert.match(enSource, /"polyforge\.work\.tab": "Work"/);
  assert.match(zhCNSource, /"polyforge\.work\.tab": "工单"/);
  assert.match(zhTWSource, /"polyforge\.work\.tab": "工單"/);
  // Jobs (workflow jobs) and Tasks (one-shot tasks) read apart from pi's own
  // background-tasks surface in Chinese.
  assert.match(zhCNSource, /"polyforge\.messages\.jobsTab": "作业"/);
  assert.match(zhTWSource, /"polyforge\.messages\.jobsTab": "作業"/);
  assert.match(enSource, /"polyforge\.tasks\.tab": "Tasks"/);
});

test("the sub-tab badges show running counts from the one status snapshot; zero greyed, none on Work", () => {
  const work = source.slice(source.indexOf('data-pf-tabstrip="work"'));
  // (intentional 2026-10-31 third-segment change): the items badge is the last list read's count.
  assert.match(work, /const count = tab === "items" \? workItemsRunning\s*: opsStatus \? \(tab === "jobs" \? opsStatus\.queue\.running : \(opsStatus\.tasks\?\.running \?\? 0\)\) : null;/,
    "queue.running on Jobs, tasks.running on Tasks, no badge without a snapshot; the items badge from the last list read (absent before it)");
  // intentional fourth sub-tab: 待集成 carries no badge.
  assert.match(work, /\{count !== null && tab !== "integrate" && \(/);
  assert.match(work, /color: count > 0 \? "var\(--accent\)" : "var\(--text-dim\)"/, "zero renders greyed, never hidden");
  assert.match(work, /aria-label=\{t\(WORK_TAB_BADGES\[tab\], \{ count \}\)\}/, "the badge has an accessible name");
  assert.match(source, /const WORK_TAB_BADGES = \{ jobs: "polyforge\.work\.jobsRunning", tasks: "polyforge\.work\.tasksRunning", items: "polyforge\.work\.itemsRunning" \} as const;/);
  const strip = source.slice(source.indexOf('role="tablist" data-pf-tabstrip="top"'), source.indexOf("{/* Body */}"));
  const workTab = strip.slice(strip.indexOf('aria-selected={activeTab === "work"}'));
  assert.doesNotMatch(workTab, /count|running/, "the top-level Work tab carries no count");
  assert.doesNotMatch(source, /fetch\(/, "the panel adds no read for the badges");
  // the panel's one timer is the card's local age clock (it
  // reads Date.now() and fetches nothing); the badge path adds none.
  assert.deepEqual(source.match(/setInterval\(/g) ?? ["x"], ["setInterval("], "the only panel timer is the card's running-age clock");
  assert.match(source, /const clock = setInterval\(\(\) => setNow\(Date\.now\(\)\), 30_000\);/, "the one timer is the card clock");
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    for (const key of ["polyforge.work.jobsRunning", "polyforge.work.tasksRunning", "polyforge.work.itemsRunning"]) {
      assert.match(localeSource, new RegExp(`"${key.replaceAll(".", "\\.")}": "[^"]*\\{count\\}`), `${name} names the ${key} badge with its count`);
    }
  }
});

test("the header carries a pressed-state pin control only when showPin", () => {
  const header = source.slice(source.indexOf("{/* Header */}"), source.indexOf("{/* Work | Memory | Messages tab strip"));
  assert.match(header, /\{showPin && \(\s*<button[\s\S]*?onClick=\{onTogglePin\}\s*aria-pressed=\{pinned\}/, "the pin renders only with showPin and reports aria-pressed");
  assert.match(header, /title=\{pinned \? t\("polyforge\.dock\.unpin"\) : t\("polyforge\.dock\.pin"\)\}/);
  assert.ok(header.indexOf("onTogglePin") < header.indexOf("handleRefresh"), "the pin sits before refresh and close");
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    for (const key of ["polyforge.dock.pin", "polyforge.dock.unpin"]) {
      assert.match(localeSource, new RegExp(`"${key.replace(/\./g, "\\.")}": "\\S`), `${name} translates ${key}`);
    }
  }
});

test("the 工单列表 count key is queue.running plus the terminal-job count, null without a snapshot; the panel adds no read", () => {
  const start = source.indexOf("export function workItemCountKey(");
  assert.ok(start >= 0, "one pure key helper");
  const body = source.slice(start, source.indexOf("export function PolyforgePanel(", start));
  assert.match(body, /if \(!opsStatus\) return null;/, "no snapshot, no key (no re-read)");
  assert.match(body, /job\.status === "completed" \|\| job\.status === "failed" \|\| job\.status === "cancelled"/, "terminal jobs only");
  assert.match(body, /return `\$\{opsStatus\.queue\.running\}:\$\{terminal\}`;/);
  assert.match(source, /const itemCountKey = workItemCountKey\(opsStatus\);/);
  assert.match(source, /workItemsRunning: number \| null;\s*onWorkItemsRunningChange: \(count: number\) => void;/);
  assert.doesNotMatch(source, /\/api\/polyforge\/work-items/, "the panel never reads the work-item list itself");
});

test("the detail order is summary (title row, goal, facts, narration, pi#117 facts) → DAG → execution progress last; no fourth block", () => {
  // owner-intended change (reverses the earlier progress-before-the-rest order by ruling).
  const detail = source.slice(source.indexOf("{/* Detail for the selected work item */}"), source.indexOf("{t(\"polyforge.selectJob\")}"));
  const marks = [
    "data-pf-detail-summary",
    "data-pf-detail-title",
    "data-pf-summary-goal",
    "{/* Job facts (+ #238)",
    "{/* pi#119 summarize narration",
    "{/* pi#117 facts, moved into the summary",
    "data-pf-detail-graph",
    "<PolyforgeDetailProgress wi={progressWi} profile={selectedJob?.profile} running={progressLive} snapshot={progressSnapshot} echoes={detailEchoes} scrollRef={detailScrollRef} />",
  ].map((needle) => detail.indexOf(needle));
  assert.ok(marks.every((index, i) => index >= 0 && (i === 0 || index > marks[i - 1])), `detail order: ${marks}`);
  const after = detail.slice(detail.indexOf("<PolyforgeDetailProgress"));
  assert.match(after, /^<PolyforgeDetailProgress[^\n]*\/>\n\s*<\/>\n/, "nothing follows the progress block");
  // The progress read is keyed on the selected job's wi (a card click stores
  // the slug as the selection ref), never on selectedWi.
  assert.doesNotMatch(source, /usePolyforgeProgress\(selectedWi/);
  // code review: a finished job leaves the daemon's lists, so the
  // wi falls back to the selection's watch snapshot — never null while that
  // snapshot is held (the last stream stays instead of "unavailable").
  assert.ok(source.includes("const progressWi = selectedWi ? (selectedJob?.wi ?? watchStatus?.wi ?? null) : null;"), "the progress wi survives the job leaving the lists");
  // workflow contract v2 (change 14): the read is the panel's (the composer
  // under the scroll region shares the stream); it polls while the job runs
  // (the rule): a waiting job is read once, a reply makes it run.
  assert.match(source, /const progressLive = selectedJob\?\.status === "running";/);
  assert.match(source, /const progressSnapshot = usePolyforgeProgress\(progressWi, progressLive, selectedJob\?\.profile\);/);
});

test("the progress section renders every stream line in full (wrapping, no ellipsis) with hover text, thinking narration dim italic, tones and its states localized, no nested scroller", () => {
  const start = source.indexOf("function PolyforgeDetailProgress(");
  const block = source.slice(start, source.indexOf("\n}\n", start));
  assert.doesNotMatch(block, /usePolyforgeProgress\(/, "the snapshot is the panel's prop (workflow contract v2, change 14)");
  assert.match(block, /data-pf-progress=""/);
  assert.match(block, /t\("polyforge\.progress\.title"\)/);
  assert.match(block, /!wi \|\| snapshot\?\.available === false \? t\("polyforge\.progress\.unavailable"\)/, "nothing readable: the unavailable state, no fabricated line");
  assert.match(block, /: !snapshot \? t\("polyforge\.loading"\)/, "Loading only until the first read settles");
  assert.match(block, /snapshot\.lines\.length === 0 \? t\("polyforge\.progress\.empty"\)/);
  assert.match(block, /snapshot\.omitted > 0 && \(\s*<span data-pf-progress-omitted="" [^>]*>\{t\("polyforge\.progress\.omitted", \{ count: snapshot\.omitted \}\)\}<\/span>/, "a legacy omitted count still renders before the lines");
  assert.match(source, /export const PROGRESS_RENDER_CHUNK = 200;/);
  assert.match(block, /const total = snapshot\?\.lines\.length \?\? 0;\s*if \(visibleCount >= total\) return;\s*const frame = requestAnimationFrame\(\(\) => \{\s*setVisibleCount\(\(current\) => Math\.min\(total, current \+ PROGRESS_RENDER_CHUNK\)\);/, "a large stream appends one bounded chunk per animation frame");
  // owner-intended change: each entry renders by kind — narration as Markdown
  // through the transcript's SafeMarkdownBody (a thinking one in the same Markdown,
  // dim italic, never folded), a tool call as the shared tool card, every other
  // event a marker row that still wraps in full with its raw kind and text on hover.
  // owner-intended change: a tool line renders through toolRow — the same shared
  // card, followed (only when the call ran subagents) by one subagent entry per subagent.
  assert.match(block, /const lineRow = \(line: PolyforgeProgressLine, index: number\): ReactNode => \{\s*if \(index >= chunkLimit\) return null;\s*if \(line\.kind === "tool"\) \{\s*return toolRow\(line, liveAt\(index\)\);/, "every tool line goes through toolRow");
  assert.match(block, /const toolRow = \(line: PolyforgeProgressLine, live: boolean\): ReactNode => \{\s*const card = <PolyforgeProgressToolCard key=\{line\.id\} line=\{line\} detail=\{details\.get\(line\.id\)\} onToggle=\{toggleTool\} \/>;\s*if \(!line\.subagents\?\.length\) return card;/, "every tool line is one shared tool card; without subagents nothing else");
  assert.match(block, /if \(line\.kind === "narration"\) \{\s*return \(\s*<div key=\{line\.id\} data-pf-progress-kind="narration" data-pf-progress-variant=\{line\.variant\} style=\{\{ minWidth: 0, \.\.\.PROGRESS_MARKDOWN_STYLE, \.\.\.progressLineStyle\(line\) \}\}>\s*\{line\.text\.length > MAX_MARKDOWN_CHARS\s*\? <div style=\{\{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" \}\}>\{line\.text\}<\/div>\s*: <SafeMarkdownBody>\{line\.text\}<\/SafeMarkdownBody>\}/, "narration (text and thinking) renders as Markdown in its line style; one over the Markdown limit inline in full as plain wrapped text, no click, no box (review s10#3)");
  assert.match(block, /data-pf-progress-kind=\{line\.kind\}\s*data-pf-progress-tone=\{line\.tone \?\? "neutral"\}\s*title=\{line\.event \? `\$\{line\.event\}\\n\$\{line\.text\}` : line\.text\}\s*style=\{\{ whiteSpace: "pre-wrap", overflowWrap: "anywhere", minWidth: 0/, "a marker row wraps in full with the raw kind and full text on hover");
  assert.doesNotMatch(block, /textOverflow|overflow: "hidden"/, "no single-line ellipsis on narration or marker rows; the tool header's ellipsis is the shared card's");
  assert.match(block, /whiteSpace: "pre-wrap", overflowWrap: "anywhere", minWidth: 0, \.\.\.progressLineStyle\(line\)/, "marker rows wrap in full");
  assert.match(source, /import \{ MAX_MARKDOWN_CHARS, SafeMarkdownBody \} from "\.\/SafeMarkdownBody";/, "the transcript's markdown guard, not a copy");
  assert.match(source, /from "\.\/PolyforgeProgressToolCard";/);
  assert.doesNotMatch(source, /from "react-markdown"|<MarkdownBody\b/, "no second markdown path in the panel");
  assert.doesNotMatch(block, /overflowY|maxHeight|virtuali[sz]/, "no nested vertical scroller (pi#120), no virtualisation; chunked rendering bounds each frame");
  assert.doesNotMatch(block, /error|failed/i, "no error readout");
  const styleStart = source.indexOf("function progressLineStyle(");
  const style = source.slice(styleStart, source.indexOf("\n}\n", styleStart));
  assert.match(style, /if \(line\.variant === "thinking"\) return \{ color: "var\(--text-dim\)", fontStyle: "italic", "--text": "var\(--text-dim\)" \} as CSSProperties;/, "thinking narration is dim italic, the Markdown body's own colour included ( review)");
  assert.doesNotMatch(style, /line\.kind === "tool"/, "owner-intended change: a tool line is a card, not a styled text row");
  assert.match(style, /return \{ color: "var\(--text\)" \};/, "text narration keeps the text colour");
  const tones = source.slice(source.indexOf("const PROGRESS_TONE_COLORS"), source.indexOf("};", source.indexOf("const PROGRESS_TONE_COLORS")));
  for (const [tone, color] of [["accent", "var(--accent)"], ["dim", "var(--text-dim)"], ["red", "#dc2626"], ["amber", "#d97706"], ["green", "#16a34a"]]) {
    assert.ok(tones.includes(`${tone}: "${color}"`), `${tone} is ${color}`);
  }
  assert.match(source, /line\.tone \? PROGRESS_TONE_COLORS\[line\.tone\] : "var\(--text-muted\)"/, "an event line without a tone is neutral");
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    for (const key of ["polyforge.progress.title", "polyforge.progress.empty", "polyforge.progress.unavailable"]) {
      assert.match(localeSource, new RegExp(`"${key.replace(/\./g, "\\.")}": "\\S`), `${name} must translate ${key}`);
    }
    assert.match(localeSource, /"polyforge\.progress\.omitted": "[^"]*\{count\}[^"]*"/, `${name} must translate the omitted marker with its count`);
  }
});

test(" (owner intentional change, reversing #238): the detail opens at its bottom, follows while pinned and offers ↓ Latest off the bottom", () => {
  assert.doesNotMatch(source, /detailScrollRef\.current\.scrollTop = 0;/, "a selection change no longer resets the detail to its top");
  const start = source.indexOf("function PolyforgeDetailProgress(");
  const block = source.slice(start, source.indexOf("\n}\n", start));
  assert.match(block, /const pinned = useRef\(true\);/, "a selection and a remount start pinned");
  assert.match(block, /const streamed = Boolean\(snapshot\?\.available && lines\.length > 0\);/, "nothing is forced while loading, empty or unavailable");
  assert.match(block, /useLayoutEffect\(\(\) => \{\s*const region = scrollRef\.current;\s*if \(region && streamed && pinned\.current\) region\.scrollTop = region\.scrollHeight;\s*\}, \[snapshot, visibleCount, streamed, shownEchoes\.length, scrollRef\]\);/, "the first stream, every later chunk and an echoed owner message end at the whole region's bottom while pinned");
  assert.match(block, /setVisibleCount\(PROGRESS_RENDER_CHUNK\);/, "another job re-chunks its stream");
  assert.match(block, /const onScroll = \(\) => \{\s*const at = isProgressPinnedToBottom\(region\);\s*pinned\.current = at;\s*setAtBottom\(at\);\s*\};/, "a passive listener tracks the follow state and the control's visibility");
  assert.match(block, /if \(region\) region\.scrollTop = region\.scrollHeight;\s*pinned\.current = true;/, "a click on ↓ Latest jumps immediately and restores following");
  assert.match(block, /!atBottom && \(/, "the control is visible exactly while the region is off its bottom");
  assert.doesNotMatch(block, /scrollTo\(|smooth/i, "the jump is not animated");
  assert.doesNotMatch(block, /unread/i, "the control carries no unread count");
});

test("an analysis card's task link switches to Work › Tasks and hands the id to the Tasks surface", () => {
  const start = source.indexOf("const openTask = useCallback(");
  const body = source.slice(start, source.indexOf("}, [onActiveTab, onWorkTab]);", start));
  assert.match(body, /setTaskToOpen\(taskId\);\s*onWorkTab\("tasks"\);\s*onActiveTab\("work"\);/);
  assert.match(source, /onOpenTask=\{openTask\}/);
  assert.match(source, /openTaskId=\{taskToOpen\}\s*onOpenTaskHandled=\{clearTaskToOpen\}/);
});

test("every block has a group head with step, status mark, duration and count; the head absorbs the start and terminal lines", () => {
  const start = source.indexOf("function PolyforgeDetailProgress(");
  const block = source.slice(start, source.indexOf("\n}\n", start));
  assert.match(block, /const durationSec = progressBlockDurationSec\(block\);/);
  assert.match(block, /const header = `\$\{open \? "▾" : "▸"\} \$\{block\.step \?\? ""\}\$\{block\.spawn \? ` #\$\{block\.spawn\}` : ""\}\$\{mark \? ` \$\{mark\}` : ""\}\$\{durationSec !== undefined \? ` \$\{formatCardDuration\(durationSec\)\}` : ""\} · \$\{t\("polyforge\.progress\.lines", \{ count: block\.count \}\)\}`;/, "step, spawn, mark, the duration only when known, the line count");
  assert.match(block, /const absorbed = \[lines\[block\.start\], block\.terminal !== undefined \? lines\[block\.terminal\] : undefined\]/, "the start and terminal lines are absorbed");
  assert.match(block, /\.map\(\(line\) => `\$\{line\.event \?\? line\.kind\}\\n\$\{line\.text\}`\)\.join\("\\n\\n"\);/, "their raw kinds and full texts on the head's title");
  assert.match(block, /title=\{absorbed\}/);
  // the block spans from its first line (a daemon run's
  // queued/admitted lead-in lines, else its start line); the body is still
  // every line of the block but the two absorbed ones, in order.
  assert.match(block, /const blockBody = \(block: PolyforgeProgressBlock\) => \{\s*const first = block\.first \?\? block\.start;\s*return lines\.slice\(first, first \+ block\.count\)\s*\.map\(\(line, offset\) => \(first \+ offset === block\.start \|\| first \+ offset === block\.terminal \? null : lineRow\(line, first \+ offset\)\)\);\s*\};/, "the body is every other line of the block, in order — nothing else dropped");
  const helper = source.slice(source.indexOf("function progressBlockDurationSec("), source.indexOf("\n}\n", source.indexOf("function progressBlockDurationSec(")));
  assert.match(helper, /if \(block\.durationMs !== undefined\) return Math\.round\(block\.durationMs \/ 1000\);/, "a finished block: its lifecycle times");
  assert.match(helper, /if \(block\.status === "running" && block\.startAt !== undefined\) return Math\.max\(0, Math\.round\(liveElapsedSec\(block\.startAt\)\)\);/, "a running block: its age at render");
  assert.match(helper, /return undefined;/, "no times: no invented duration");
});

test("a tool card's detail is read only on its expansion, once per call per selection, through the card module", () => {
  const start = source.indexOf("function PolyforgeDetailProgress(");
  const block = source.slice(start, source.indexOf("\n}\n", start));
  assert.match(block, /const loadDetail = useCallback\(\(id: string\) => \{\s*const held = detailsRef\.current\.get\(id\);\s*if \(!wi \|\| \(held && held\.status !== "unavailable"\)\) return;/, "loading or loaded: never asked again; unavailable: asked again on the next expansion");
  assert.match(block, /if \(selection\.current !== asked\) return;\s*setDetails\(\(current\) => new Map\(current\)\.set\(id, state\)\);\s*setFoldVersion\(\(current\) => current \+ 1\);/, "an answer for another selection writes nothing; a settled detail re-measures the follow state like a fold (review s10#2)");
  assert.match(block, /const toggleTool = useCallback\(\(id: string, open: boolean\) => \{\s*if \(open\) loadDetail\(id\);\s*setFoldVersion\(\(current\) => current \+ 1\);\s*\}, \[loadDetail\]\);/, "a card toggle is a fold: it re-measures the follow state and only an expansion asks for the detail (review s10#2)");
  assert.match(block, /void fetchProgressToolDetail\(asked, id, profile\)\.then\(settle\);/);
  assert.match(block, /setDetails\(new Map\(\)\);/, "the cache resets per selection, never persisted");
  assert.doesNotMatch(source, /fetch\(/, "the panel itself still adds no read");
});

test("the tool card wording exists in all three locales", () => {
  const keys = ["toolStatus.ok", "toolStatus.error", "toolStatus.running", "toolInput", "toolOutput", "toolNoResult", "toolUnavailable"];
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    for (const key of keys) assert.match(localeSource, new RegExp(`"polyforge\\.progress\\.${key.replace(/\./g, "\\.")}": "\\S`), `${name} ${key}`);
  }
  assert.match(enSource, /"polyforge\.progress\.toolStatus\.ok": "ok"/);
  assert.match(enSource, /"polyforge\.progress\.toolNoResult": "No result yet"/);
  assert.match(enSource, /"polyforge\.progress\.toolUnavailable": "Tool detail unavailable"/);
});

test("/#499: no 决策 sub-tab and no 交互 button; the walk's task reaches Tasks; task summaries ride to Tasks; every class is labelled", () => {
  assert.doesNotMatch(source, /"decisions"|PolyforgeDecisions\b|decisionsTab/, "the Decisions sub-tab is gone");
  const frame = source.slice(source.indexOf('data-pf-detail-frame=""'));
  assert.doesNotMatch(frame, /position: "absolute", right: 8, bottom: 8/, "no floating button over the detail");
  assert.match(source, /setTaskToOpen\(taskRequest\.id\);/, "a walk's task reaches the Tasks surface through the existing hand-off");
  assert.match(source, /decisions=\{decisionsSurface\.feed\?\.decisions \?\? \[\]\}\s*onDismissDecisions=\{decisionsSurface\.onDismiss\}/, "task summaries ride to the Tasks tab");
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    assert.doesNotMatch(localeSource, /polyforge\.work\.decisionsTab/, `${name} has no Decisions tab key`);
    for (const cls of ["needs_input", "blocked", "failed", "completed", "canceled", "paused", "terminal", "step_dead", "candidates_exhausted",
      "carrier_push_failed", "submit_rejected", "submit_dropped", "daemon_restarted", "capped", "interrupted"]) {
      assert.match(localeSource, new RegExp(`"polyforge\\.decisions\\.class\\.${cls}": "\\S`), `${name} labels the ${cls} class`);
    }
  }
});

test("PR #860 review: the walk's task request is dropped once handed; every blocked kind is labelled", () => {
  assert.match(source, /if \(!taskRequest\) return;\s*setTaskToOpen\(taskRequest\.id\);\s*onTaskRequestHandled\(\);/, "minor 1: handed once, then cleared — a dock reopen does not reopen the task");
  for (const [name, localeSource] of [["en", enSource], ["zh-CN", zhCNSource], ["zh-TW", zhTWSource]]) {
    for (const kind of ["incomplete", "driver", "contract", "write_failed"]) assert.match(localeSource, new RegExp(`"polyforge\\.blocked\\.kind\\.${kind}": "\\S`), `${name} labels the ${kind} block`);
  }
});

// a step the lane skipped (its execution record says
// `skipped`) reads as grayed out as a whole — gray name, dashed frame — while
// its place in the declared sequence and its accessible name stay.
test(" DAG: a skipped step is grayed", () => {
  const context = vm.createContext({});
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("buildStepGraph").replace(/export /g, "")), context);
  const graph = JSON.parse(JSON.stringify(context.buildStepGraph(
    [{ name: "build", status: "done" }, { name: "publish", status: "done" }, { name: "merge", status: "skipped" }],
    [{ id: "build" }, { id: "publish" }, { id: "merge" }],
    [],
  )));
  assert.deepEqual(graph.nodes, [
    { id: "build", status: "done" }, { id: "publish", status: "done" }, { id: "merge", status: "skipped" },
  ], "the record's skipped status reaches the node");
  assert.deepEqual(graph.sequenceEdges, [["build", "publish"], ["publish", "merge"]], "the order edges keep the skipped step");

  // The colour map states skipped explicitly (gray), not through the fallback.
  const colorMap = source.match(/const STEP_STATUS_COLORS: Record<string, string> = \{[\s\S]*?\n\};/);
  assert.ok(colorMap, "STEP_STATUS_COLORS map literal");
  assert.match(colorMap[0], /\n  skipped: "var\(--text-dim\)",/, "skipped has its own explicit entry");
  vm.runInContext(stripTypeScriptTypes(colorMap[0]), context);
  vm.runInContext(stripTypeScriptTypes(extractPureFunction("stepNodeColor").replace(/export /g, "")), context);
  assert.equal(context.stepNodeColor("skipped"), "var(--text-dim)");

  // The listitem: gray text and a dashed frame for skipped, the usual look otherwise.
  const nodes = source.slice(source.indexOf("graph.nodes.map((node, index) => {"), source.indexOf("graph.rewindEdges.length > 0 &&"));
  assert.match(nodes, /const skipped = node\.status === "skipped";/);
  const itemAt = nodes.indexOf('role="listitem"');
  const item = nodes.slice(itemAt, nodes.indexOf("{shortenCandidate(node.id)}", itemAt));
  const style = item.slice(item.indexOf("style={{"), item.indexOf("}}", item.indexOf("style={{")) + 2);
  assert.match(style, /border: skipped \? "1px dashed var\(--border\)" : "1px solid var\(--border\)"/, "a dashed frame for skipped");
  assert.match(style, /color: skipped \? "var\(--text-dim\)" : "var\(--text\)"/, "gray text for skipped");
  // Still named with its status for hover and assistive tech: "merge · 已跳过".
  assert.match(item, /title=\{`\$\{node\.id\} · \$\{stepStatusLabel\(node\.status\)\}/);
  assert.match(item, /aria-label=\{`\$\{node\.id\} · \$\{stepStatusLabel\(node\.status\)\}`\}/);
  assert.match(zhCNSource, /"polyforge\.stepStatus\.skipped": "已跳过",/);
});
