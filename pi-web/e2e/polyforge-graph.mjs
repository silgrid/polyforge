// carries: hooks/usePolyforgeProgress*, app/api/polyforge/progress/route.ts, lib/polyforge-cards*, app/api/polyforge/cards/*, app/api/polyforge/watch/*
// carries: app/api/polyforge/decisions/*
// pi#114 (wi_fnV3SUypLyIN) — Polyforge step list as a flow graph. Authored
// pre-code against artifacts/spec.md (new-behaviour acceptance criteria only);
// standalone like e2e/polyforge.mjs (own dev server, stub `pf` on PATH). A NODE is
// an element whose aria-label carries its visible name (the node's own text, not
// the visible status label it also shows) and a step status label; a
// sequence EDGE is any visible element/text (or ::after/::before connector) between
// DOM-consecutive nodes; a rewind EDGE is a dashed-styled element (border, outline,
// stroke-dasharray or a ⇢-style glyph) naming gate then target. Any FAIL exits 1.
//
// (owner-intentional change, by ruling — not a regression): the
// standalone event tail and its pi#112 simple/verbose mode are gone, so a
// detail is recognised by its title row's slug, its execution-progress block
// and its rendered graph (no longer by the tail's raw marker kind), nothing
// seeds `pi-web:polyforge-event-mode`, and G1/G13 lose the mode dimension —
// one graph in every state — while every graph assertion stays as it was; G13
// additionally asserts that no mode control and no `sync ×N` row exist.
//
//   node e2e/polyforge-graph.mjs
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { polyforgeDock as pfPanel, polyforgeToggle as pfButton } from "./polyforge-dock.mjs";
import { assertNoDevServer, createCheck, createHarness, reportTitled, root, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-graph-e2e-"));
const agentDir = join(work, "agent"); const project = join(work, "project");
mkdirSync(join(agentDir, "sessions", "e2e"), { recursive: true }); mkdirSync(project);
const SESSION = "e2e-polyforge-graph-session";
const ts = "2026-10-19T00:00:00.000Z";
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E polyforge graph host session", timestamp: ts });

// ---- fixtures (one watch per slug; each tail ends with a unique raw marker
// kind — an unknown kind, which renders nowhere) ----
const ONFAIL_SENTINEL = "E2E-ONFAIL-SENTINEL-114";
const PATH_SENTINEL = "/srv/e2e-secret-workspace-114";
const at = (h, m) => `2026-10-19T${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:00Z`;
const ev = (n, kind, body = {}) => ({ at: at(10, n % 60), kind, work_item: "wi_x", body, seq: n });
const marker = (slug) => `e2e_marker_${slug.slice(3)}`;
const watch = (slug, steps, extra = {}, events = []) => ({ work_item: `wi_E2Egraph${slug.slice(3)}`, slug, known: true, job: { status: "running" },
  execution: { workflow_status: "running", updated_at: at(10, 0), steps, ...extra },
  events: [...events, ev(999, marker(slug), {})], last_seq: 999 });
const S = (status, started) => (started ? { status, started_at: started } : { status });
// 9-step feature-full workflow; started_at order deliberately differs from the declaration.
const NINE = ["grill", "grill-review", "spec", "plan", "e2e-author", "code", "review", "verification", "deliver"];
const ON_FAIL = { "grill-review": "grill", code: "plan", review: "spec", verification: "code", deliver: ONFAIL_SENTINEL };
const W201 = watch("pi#201", { grill: S("done", at(9, 0)), "grill-review": S("done", at(8, 50)), spec: S("done", at(9, 10)), plan: S("done", at(8, 40)),
  "e2e-author": S("in_progress", at(9, 20)), code: S("pending"), review: S("pending"), verification: S("pending"), deliver: S("pending") },
{ declared_steps: NINE.map((id) => ({ id, on_fail: ON_FAIL[id] ?? null, ...(id === "plan" ? { path: PATH_SENTINEL } : {}) })) });
// Fallback fixtures: same steps, declared_steps absent / object / id-less entries / null / empty.
const FALLBACK_STEPS = { "zulu-grill": S("done", at(8, 0)), "mike-spec": S("done", at(8, 5)), "alpha-plan": S("in_progress", at(8, 10)),
  "kilo-author": S("pending"), "bravo-code": S("pending") };
const FALLBACK = { "pi#202": undefined, "pi#203": { grill: "x" }, "pi#204": [{ on_fail: "spec" }, { name: "plan" }], "pi#205": null, "pi#206": [] };
const W207 = watch("pi#207", { grill: S("done", at(9, 0)), spec: S("in_progress", at(9, 10)), plan: S("done", at(8, 0)), code: S("done", at(8, 30)) },
  { declared_steps: [{ id: "grill" }, { id: "spec" }, { id: "ghost-step" }] });
// Rewinds: 46 events → the oldest 6 (incl. rewind code→plan) fall outside the 40-event tail.
const W208 = watch("pi#208", { grill: S("done", at(8, 0)), spec: S("in_progress", at(9, 30)), "grill-review": S("failed", at(8, 20)), plan: S("pending"), code: S("pending") },
  { declared_steps: ["grill", "spec", "grill-review", "plan", "code"].map((id) => ({ id })) },
  [ev(1, "rewind", { gate: "code", target: "plan" }), ...Array.from({ length: 38 }, (_, i) => ev(i + 2, "published", { version: 1 })),
    ev(40, "step_started", { step: "spec" }), ev(41, "step_started", { step: "grill-review" }), ev(42, "rewind", { gate: "grill-review", target: "spec" }),
    ev(43, "rewind", { gate: "ghost-gate", target: "spec" }), ev(44, "rewind", { gate: "spec", target: "ghost-target" }), ev(45, "step_started", { step: "spec" })]);
const LONG = "extraordinarily-long-step-name-e2e";
const W209 = watch("pi#209", { "done-step": S("done", at(8, 0)), "run-step": S("in_progress", at(8, 10)), "wait-step": S("pending"), "fail-step": S("failed", at(8, 1)),
  "block-step": S("blocked", at(8, 2)), "stop-step": S("cancelled", at(8, 3)), "odd-step": S("paused", at(8, 4)), [LONG]: S("done", at(8, 5)) });
const WATCHES = { "pi#201": W201, "pi#207": W207, "pi#208": W208, "pi#209": W209 };
for (const [slug, declared] of Object.entries(FALLBACK)) {
  WATCHES[slug] = watch(slug, FALLBACK_STEPS, declared === undefined ? {} : { declared_steps: declared });
}
const JOBS = Object.keys(WATCHES).map((slug, i) => ({ work_item_id: WATCHES[slug].work_item, slug, status: "running", spawn_n: 1,
  step_age_sec: 10 + i, last_work_ago_sec: 5, candidate: "pi:e2e", origin: { kind: "chat", owner: "u_e2e" } }));
// owner-intended change: the selected running job's progress read
// (GET /api/polyforge/progress) looks the wi up in the daemon's own job record;
// a throwaway data root lists every fixture job (no capsule → a 200
// `available:false` read), as a real daemon lists every job it runs, instead
// of the host's real data root answering 400 for jobs it never ran.
const dataRoot = join(work, "pf-data"); mkdirSync(dataRoot);
writeFileSync(join(dataRoot, "runtime-state.json"), JSON.stringify({ version: 1, jobs: JOBS.map((job) => ({ work_item_id: job.work_item_id })) }));
const stubDir = join(work, "stub-bin"); mkdirSync(stubDir);
writeFileSync(join(stubDir, "state.json"), JSON.stringify({ ops: { machine_id: "m-e2e", jobs: JOBS, queue: { pending: 0, running: JOBS.length, max_concurrent: 20 } }, watch: WATCHES }));
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename)); const args = process.argv.slice(2).filter((a) => a !== "--json");
if (args.includes("--version")) { console.log("pf 0.0.0-e2e"); process.exit(0); }
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const out = (v) => { process.stdout.write(JSON.stringify(v) + "\\n"); process.exit(0); };
if (args[0] === "ops" && args[1] === "status") out(s.ops);
if (args[0] === "watch") {
  const i = args.indexOf("--events"); const n = i >= 0 ? Number(args[i + 1]) : 8;
  const w = s.watch[args[1]] || { work_item: args[1], known: false, events: [], last_seq: 0 };
  out({ ...w, events: (w.events || []).slice(-n) });
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);

// ---- scenario bookkeeping ----
const T = {
  G1: "G1: One graph node per step and no step chips (one graph in every state)",
  G2: "G2: A step re-run after a rewind keeps exactly one node and no attempt counter",
  G3: "G3: declared_steps drives node order and sequence edges, unstarted steps included",
  G4: "G4: Declared on_fail targets are never drawn",
  G5: "G5: Absent, null, empty or malformed declared_steps falls back to started-at order with no error surface",
  G6: "G6: steps/declared_steps mismatch renders declared order, a pending gray placeholder, and unedged extras",
  G7: "G7: Only taken rewinds in the 40-event tail with both endpoints as nodes draw one dashed edge",
  G8: "G8: Node colours encode status and only the in_progress node pulses",
  G9: "G9: Under prefers-reduced-motion no node animates",
  G10: "G10: Node shows the 24-char-cut name; title and aria label carry full name and status",
  G11: "G11: Clicking a node does nothing",
  G12: "G12: Nine nodes wrap onto rows without horizontal scroll in the 460px dock and the mobile sheet",
  G13: "G13: No step name+status chips, no event-mode control and no sync row (the event modes are gone)",
  G14: "G14: Watch route sanitizes declared_steps to ordered ids only",
  G15: "G15: Docs describe the graph contract and polyforge labels stay in all three locales",
};
const failures = new Map(Object.values(T).map((title) => [title, []])); const ran = new Set();
const check = createCheck({ failures: ([title, label], text) => failures.get(title).push(`${label}: ${text}`), before: ([title]) => ran.add(title), pass: null, fail: null });
// One page per fixture: open, select `slug`, read the graph, then run each
// scenario's own check() on it (a setup failure is recorded on every title).
async function onPage(titles, opts, slug, fn) {
  let page;
  try {
    page = await openPage(opts);
    const panel = await select(page, slug);
    await fn(page, panel, await readGraph(panel));
  } catch (error) {
    for (const title of titles) { ran.add(title); failures.get(title).push(`setup ${slug}: ${String(error?.message ?? error).split("\n")[0]}`); }
  } finally { await page?.context().close(); }
}

let base; const logFile = join(work, "server.log");
async function get(path) {
  const response = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(30_000) });
  const text = await response.text(); let json = null; try { json = JSON.parse(text); } catch {}
  return { status: response.status, text, json };
}
// Navigation headroom only: the host is shared and other capsules' e2e
// servers routinely push a cold page load past this page's 10s action
// timeout. Assertions below still use the 10s default.
const h = createHarness({
  storage: { "pi-web:split-view-enabled": "false" },
  shape: (o) => ({ ...o, context: o.reducedMotion ? { reducedMotion: o.reducedMotion } : {} }),
  errors: ["pageerror", "console", "5xx"],
  session: SESSION,
  ready: "E2E polyforge graph host session",
});
const { errors, openPage } = h;
// The detail for `slug` shows iff its title row names the slug, its
// execution-progress block is mounted and its graph has rendered (the watch
// read landed). Job cards toggle selection: verify first, click only when
// absent, retry bounded.
async function select(page, slug) {
  const showing = async (panel) => (await panel.locator("[data-pf-detail-title]").getByText(slug, { exact: true }).count()) > 0
    && (await panel.locator("[data-pf-progress]").count()) > 0
    && (await panel.locator('[data-pf-detail-graph] [role="listitem"]').count()) > 0;
  for (let attempt = 0; attempt < 6; attempt++) {
    if ((await pfPanel(page).count()) === 0) { await pfButton(page).click(); await pfPanel(page).first().waitFor(); continue; }
    const panel = pfPanel(page).first();
    if (await showing(panel)) return panel;
    const cardFor = panel.locator(`[data-pf-card-wi="${WATCHES[slug].work_item}"]`);
    if ((await cardFor.count()) === 0) { await cardFor.waitFor({ timeout: 5_000 }).catch(() => {}); continue; }
    if ((await panel.locator("[data-pf-detail-title]").getByText(slug, { exact: true }).count()) === 0) await cardFor.click().catch(() => {});
    // Wait for the watch read to land (the graph renders), bounded; an early
    // retry would toggle the card again.
    await panel.locator('[data-pf-detail-graph] [role="listitem"]').first().waitFor({ timeout: 5_000 }).catch(() => {});
  }
  throw new Error(`the ${slug} detail did not render`);
}

// ---- in-page graph reader (see header for the node/edge definitions) ----
const STATUS_LABELS = ["Done", "In progress", "Pending", "Failed", "Blocked", "Cancelled", "paused"];
const readGraph = (panel) => panel.first().evaluate((root, labels) => {
  const rgb = (c) => { const m = /rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)(?:\s*[,/]\s*([\d.]+))?/.exec(String(c)); return m && (m[4] === undefined || Number(m[4]) > 0) ? `${m[1]},${m[2]},${m[3]}` : null; };
  const resolve = (v) => { const p = document.createElement("span"); p.style.color = v; root.appendChild(p); const c = rgb(getComputedStyle(p).color); p.remove(); return c; };
  // a node also shows its status label as a child element, so
  // its name is its own text nodes only.
  const ownText = (el) => [...el.childNodes].filter((n) => n.nodeType === 3).map((n) => n.textContent).join("").trim();
  const all = [...root.querySelectorAll("[aria-label][title]")].filter((el) => { const aria = el.getAttribute("aria-label"); const text = ownText(el);
    return !!text && aria.includes(text.replace(/…$/, "")) && labels.some((l) => aria.includes(l)); });
  const nodes = all.filter((el) => !all.some((o) => o !== el && o.contains(el)));
  const colorsOf = (el) => {
    const set = new Set();
    for (const e of [el, ...el.querySelectorAll("*")]) {
      const cs = getComputedStyle(e);
      if ([...e.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim())) set.add(rgb(cs.color));
      set.add(rgb(cs.backgroundColor));
      for (const side of ["Top", "Right", "Bottom", "Left"]) if (parseFloat(cs[`border${side}Width`]) > 0 && cs[`border${side}Style`] !== "none") set.add(rgb(cs[`border${side}Color`]));
      if (e instanceof SVGElement) { set.add(rgb(cs.fill)); set.add(rgb(cs.stroke)); }
    }
    set.delete(null); return [...set].sort();
  };
  const animated = (el) => [el, ...el.querySelectorAll("*")].some((e) => { const cs = getComputedStyle(e); return cs.animationName !== "none" && parseFloat(cs.animationDuration) > 0; });
  const between = (a, b) => {
    const pseudo = (el, p) => { const c = getComputedStyle(el, p).content; return c && c !== "none" && c !== "normal"; };
    if (pseudo(a, "::after") || pseudo(b, "::before")) return true;
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      if (!(a.compareDocumentPosition(n) & Node.DOCUMENT_POSITION_FOLLOWING) || !(n.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)) continue;
      if (a.contains(n) || b.contains(n) || n.contains?.(a) || n.contains?.(b)) continue;
      // owner-intended change: a node's timing line sits under it in its column; it is not a connector.
      if ((n.nodeType === 3 ? n.parentElement : n)?.closest?.("[data-pf-step-timing]")) continue;
      if (n.nodeType === 3 ? n.textContent.trim() : (() => { const r = n.getBoundingClientRect(); return r.width > 0 && r.height > 0; })()) return true;
    }
    return false;
  };
  const dashedEls = [...root.querySelectorAll("*")].filter((e) => {
    if (nodes.some((n) => n.contains(e))) return false;
    const cs = getComputedStyle(e);
    return ["Top", "Right", "Bottom", "Left"].some((s) => cs[`border${s}Style`] === "dashed") || cs.outlineStyle === "dashed"
      || (cs.strokeDasharray && cs.strokeDasharray !== "none") || /[⇢⤏⤑╌┄┅⇠]/.test([...e.childNodes].filter((n) => n.nodeType === 3).map((n) => n.textContent).join(""));
  });
  const dashed = [...new Set(dashedEls.map((e) => { let x = e; for (let i = 0; i < 3 && x.parentElement && x !== root && !x.getAttribute("title") && !x.getAttribute("aria-label"); i++) x = x.parentElement; return x; }))]
    .map((e) => [e.getAttribute("title"), e.getAttribute("aria-label"), e.textContent].filter(Boolean).join(" | "));
  const rows = [...new Set(nodes.map((n) => Math.round(n.getBoundingClientRect().top / 4)))];
  let hscroll = false;
  for (let e = nodes[0]?.parentElement; e && root.contains(e); e = e.parentElement) hscroll ||= e.scrollWidth > e.clientWidth + 1 && ["auto", "scroll"].includes(getComputedStyle(e).overflowX);
  const pr = root.getBoundingClientRect();
  return {
    nodes: nodes.map((n) => { const r = n.getBoundingClientRect(); return { text: ownText(n), aria: n.getAttribute("aria-label"), title: n.getAttribute("title"),
      colors: colorsOf(n), animated: animated(n), inside: r.left >= pr.left - 1 && r.right <= pr.right + 1, top: r.top, left: r.left }; }),
    edges: nodes.slice(1).map((b, i) => between(nodes[i], b)),
    dashed, rowCount: rows.length, hscroll, accent: resolve("var(--accent)") };
}, STATUS_LABELS);
const names = (g) => g.nodes.map((n) => n.text);
const edge = (g, from, to) => { const i = names(g).indexOf(from); return i >= 0 && names(g)[i + 1] === to ? g.edges[i] : undefined; };
const dashedFor = (g, gate, target) => g.dashed.filter((s) => s.includes(gate) && s.includes(target) && s.indexOf(gate) < s.lastIndexOf(target));
function assertWraps(g) {
  requireNodes(g, 9);
  assert.ok(g.rowCount >= 2, `9 nodes sit on ${g.rowCount} row(s)`);
  assert.ok(g.nodes.every((n) => n.inside) && !g.hscroll, "the step area scrolls or overflows horizontally");
}
const requireNodes = (g, n) => assert.ok(g.nodes.length >= n, `graph shows ${g.nodes.length} nodes (${JSON.stringify(names(g))}), want ${n}`);
// Step chips (the pre-graph display removed in pi#116): any element whose text
// is name + status label; must count 0 in every mode — the nodes are the only
// step display. the graph node itself shows name + status (the
// owner-approved visible label), so a node, an element inside it, or a
// wrapper holding nothing but that one node is not a chip.
const chipCount = (panel, steps) => panel.first().evaluate((root, steps) => steps.filter(([name, label]) => [...root.querySelectorAll("*")].some((e) => {
  const NODE = '[role="listitem"][aria-label][title]';
  if (e.closest(NODE)) return false;
  const inner = e.querySelectorAll(NODE);
  if (inner.length === 1 && (e.textContent || "").trim() === (inner[0].textContent || "").trim()) return false;
  const t = (e.textContent || "").replace(/\s+/g, "");
  return t.startsWith(name) && t.includes(label.replace(/\s+/g, ""), name.length) && t.length <= name.length + label.length + 4;
})).length, steps);
const GREEN = "22,163,74"; const RED = "220,38,38"; const AMBER = "217,119,6";

try {
  await h.launchBrowser(() => chromium.launch());
  ({ base } = await h.startServer({ name: "polyforge-graph", logFile, env: { PATH: `${stubDir}${delimiter}${process.env.PATH}`, PI_CODING_AGENT_DIR: agentDir,
    PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_DATA_DIR: dataRoot } }));
  // G14 — sanitizer: ordered ids only, no on_fail, no paths.
  await check(T.G14, "watch route", async () => {
    const r = await get("/api/polyforge/watch?wi=pi%23201");
    assert.equal(r.status, 200, `watch route answered ${r.status}`);
    assert.deepEqual(r.json?.status?.declared_steps, NINE.map((id) => ({ id })), "status.declared_steps is not the ordered [{id}] list");
    for (const leak of ["on_fail", ONFAIL_SENTINEL, PATH_SENTINEL]) assert.ok(!r.text.includes(leak), `watch response leaks ${leak}`);
  });
  const CHIPS201 = NINE.map((n) => [n, n === "e2e-author" ? "In progress" : /grill|spec|plan/.test(n) ? "Done" : "Pending"]);
  await onPage([T.G1, T.G3, T.G4, T.G12], {}, "pi#201", async (page, panel, g) => {
    await check(T.G1, "one graph", async () => {
      requireNodes(g, 9);
      assert.deepEqual([...names(g)].sort(), [...NINE].sort(), "not exactly one node per step");
      assert.equal(await chipCount(panel, CHIPS201), 0, "step chips render");
    });
    await check(T.G3, "declared order", async () => {
      requireNodes(g, 9);
      assert.deepEqual(names(g), NINE, "nodes are not in declared order");
      const reading = [...g.nodes].sort((a, b) => (Math.abs(a.top - b.top) > 4 ? a.top - b.top : a.left - b.left)).map((n) => n.text);
      assert.deepEqual(reading, NINE, "nodes do not read left-to-right in declared order");
      assert.deepEqual(g.edges, NINE.slice(1).map(() => true), `missing sequence edges: ${JSON.stringify(g.edges)}`);
    });
    await check(T.G4, "no on_fail edges", async () => {
      requireNodes(g, 9);
      assert.deepEqual(g.dashed, [], `dashed edges drawn without any rewind: ${JSON.stringify(g.dashed)}`);
      for (const [from, to] of Object.entries(ON_FAIL)) assert.ok(edge(g, from, to) === undefined, `an edge joins ${from} → ${to}`);
    });
    await check(T.G12, "460px dock", async () => assertWraps(g));
  });
  await onPage([T.G12], { viewport: { width: 600, height: 900 } }, "pi#201", async (page, panel, g) => {
    await check(T.G12, "mobile sheet", async () => assertWraps(g));
  });
  await onPage([T.G13], {}, "pi#201", async (page, panel, g) => {
    await check(T.G13, "no chips, no mode, no sync row", async () => {
      requireNodes(g, 9);
      assert.equal(await chipCount(panel, CHIPS201), 0, "the detail shows name+status chips");
      assert.equal(await panel.getByRole("button", { name: /^(simple|verbose)$/i }).count(), 0, "an event-mode control exists");
      assert.equal(await panel.getByText(/sync\s*[×x]\s*\d/i).count(), 0, "a collapsed sync row exists");
      assert.equal(await panel.getByText(marker("pi#201")).count(), 0, "the unknown marker kind renders somewhere");
    });
  });
  const errorsBefore = errors.length;
  for (const slug of Object.keys(FALLBACK)) {
    await onPage([T.G5], {}, slug, async (page, panel, g) => {
      await check(T.G5, slug, async () => {
        const r = await get(`/api/polyforge/watch?wi=${encodeURIComponent(slug)}`);
        assert.ok(r.status === 200 && r.json?.status, `watch answered ${r.status} ${r.text.slice(0, 80)}`);
        requireNodes(g, 5);
        assert.deepEqual(names(g).slice(0, 3), ["zulu-grill", "mike-spec", "alpha-plan"], "started steps not in started_at order");
        assert.deepEqual([...names(g).slice(3)].sort(), ["bravo-code", "kilo-author"], "unstarted steps not after the started ones");
        assert.deepEqual(g.edges, [true, true, false, false], `edges ${JSON.stringify(g.edges)} (want started edged, unstarted unedged)`);
      });
    });
  }
  await check(T.G5, "no error surface", async () => assert.deepEqual(errors.slice(errorsBefore), []));
  await onPage([T.G6], {}, "pi#207", async (page, panel, g) => {
    await check(T.G6, "mismatch", async () => {
      requireNodes(g, 5);
      assert.deepEqual(names(g), ["grill", "spec", "ghost-step", "plan", "code"], "declared order then started-at extras expected");
      assert.equal(edge(g, "grill", "spec"), true, "no edge grill → spec");
      assert.equal(edge(g, "ghost-step", "plan"), false, "the appended step is edged to the declaration");
      assert.equal(edge(g, "plan", "code"), false, "appended steps are edged together");
      const ghost = g.nodes[2]; assert.ok(ghost.aria.includes("Pending"), `ghost-step is not pending: ${ghost.aria}`);
      assert.ok(![GREEN, RED, AMBER, g.accent].some((c) => ghost.colors.includes(c)), `ghost-step is not gray: ${ghost.colors}`);
    });
  });
  await onPage([T.G2, T.G7], {}, "pi#208", async (page, panel, g) => {
    await check(T.G2, "rewind re-run", async () => {
      requireNodes(g, 5);
      assert.deepEqual(names(g), ["grill", "spec", "grill-review", "plan", "code"], "not exactly one node per step");
      assert.ok(g.nodes[1].aria.includes("In progress") && g.nodes[2].aria.includes("Failed"), "nodes do not show the current status");
      assert.doesNotMatch(g.nodes.map((n) => `${n.aria} ${n.title}`).join(" "), /attempt|×\s*\d|#\d|\(\d\)/i, "an attempt counter is shown");
    });
    await check(T.G7, "rewind edges", async () => {
      requireNodes(g, 5);
      assert.equal(dashedFor(g, "grill-review", "spec").length, 1, `want one dashed grill-review → spec edge, got ${JSON.stringify(g.dashed)}`);
      assert.equal(dashedFor(g, "code", "plan").length, 0, "a rewind outside the 40-event tail is drawn");
      assert.ok(!g.dashed.some((s) => s.includes("ghost-gate") || s.includes("ghost-target")), "a rewind with an unknown endpoint is drawn");
    });
  });
  await onPage([T.G8, T.G10, T.G11], {}, "pi#209", async (page, panel, g) => {
    const node = (name) => g.nodes.find((n) => n.aria.includes(name));
    await check(T.G8, "colours", async () => {
      requireNodes(g, 8);
      for (const [name, c] of [["done-step", GREEN], ["fail-step", RED], ["block-step", AMBER], ["run-step", g.accent]]) {
        assert.ok(node(name)?.colors.includes(c), `${name} lacks colour ${c}: ${node(name)?.colors}`);
      }
      const pending = node("wait-step");
      assert.ok(pending && ![GREEN, RED, AMBER, g.accent].some((c) => pending.colors.includes(c)), "pending is not gray");
      assert.deepEqual(node("stop-step")?.colors, pending.colors, "cancelled is not gray like pending");
      assert.deepEqual(node("odd-step")?.colors, pending.colors, "an unknown status is not gray like pending");
      assert.deepEqual(g.nodes.filter((n) => n.animated).map((n) => n.text), ["run-step"], "only the in_progress node must pulse");
    });
    await check(T.G10, "truncation", async () => {
      const long = node(LONG); assert.ok(long, `no node carries ${LONG} in its accessible label`);
      assert.equal(long.text, `${LONG.slice(0, 24)}…`, "visible name not cut at 24 + …");
      for (const v of [long.title, long.aria]) assert.ok(v.includes(LONG) && v.includes("Done"), `title/aria lack full name + status: ${v}`);
      for (const n of g.nodes) assert.ok(STATUS_LABELS.some((l) => n.title.includes(l)), `node ${n.text} title lacks its status`);
    });
    await check(T.G11, "click", async () => {
      requireNodes(g, 8);
      const url = page.url(); const hits = [];
      page.on("request", (r) => { if (r.url().startsWith(base)) hits.push({ t: Date.now(), path: new URL(r.url()).pathname }); });
      const titled = await panel.locator("[title]").count();
      // Synchronize just after a watch poll: the next one is ~3s away.
      await page.waitForRequest((r) => new URL(r.url()).pathname === "/api/polyforge/watch", { timeout: 9000 });
      const t0 = Date.now(); await delay(300);
      for (const n of g.nodes) await panel.getByRole("listitem", { name: n.aria, exact: true }).first().click({ timeout: 2000 });
      await delay(Math.max(0, 2500 - (Date.now() - t0)));
      // Background reads that are not click-caused:
      // - GET /api/agent/running (sidebar poll, RUNNING_SESSIONS_POLL_MS).
      //   Owner-authorized amendment Q 114-code-g11-running-poll-conflict:
      //   PROBE_CLICK=0 showed an identical request signature with zero clicks.
      // - GET /api/polyforge/status and /api/polyforge/messages (dock's own
      //   OPEN_STATUS_POLL_MS interval). The messages read was added here in
      // on code evidence: hooks/usePolyforge.ts reads it on
      //   mount and every 3s, and otherwise only on send or a Messages-tab
      //   switch — never on a graph-node click. It was not PROBE_CLICK-probed
      //   and needs no owner ruling. reached the same exclusion
      //   independently (owner answer Q w123-code-g11-messages-poll-rewind):
      //   that poll's own 3s timer, started at dock open, drifts against the
      //   watch poll.
      // - GET /api/polyforge/cards: the open Jobs tab's card
      //   stream polls on its own browser-stored interval (default 3s),
      //   started at dock open, so it drifts against the watch poll like
      //   the messages read. Its effect depends only on dock-open, active
      //   tab and interval (hooks/usePolyforge.ts), never on a node click.
      // - GET /api/polyforge/progress (owner-intended change): the
      //   selected running job's execution progress reads on its own fixed 2 s
      //   timer (hooks/usePolyforgeProgress.ts), started at selection, never on
      //   a node click — the /api/polyforge/cards precedent.
      // - GET /api/polyforge/decisions (owner-intended change):
      //   the decision feed rides every status read (hooks/usePolyforge.ts
      //   refreshStatus), so it follows the status poll, never a node click.
      // Any other request still fails.
      const background = new Set(["/api/polyforge/status", "/api/agent/running", "/api/polyforge/messages", "/api/polyforge/cards", "/api/polyforge/progress", "/api/polyforge/decisions"]);
      const early = hits.filter((h) => h.t > t0 + 250 && h.t < t0 + 2500 && !background.has(h.path));
      assert.deepEqual(early, [], `clicking nodes issued ${JSON.stringify(early)}`);
      assert.equal(page.url(), url, "clicking a node navigated");
      assert.ok((await panel.locator("[data-pf-detail-title]").getByText("pi#209", { exact: true }).count()) > 0 && (await panel.locator("[title]").count()) === titled, "clicking a node changed the detail");
    });
  });
  await onPage([T.G9], { reducedMotion: "reduce" }, "pi#209", async (page, panel, g) => {
    await check(T.G9, "reduced motion", async () => {
      requireNodes(g, 8);
      assert.deepEqual(g.nodes.filter((n) => n.animated).map((n) => n.text), [], "a node animates under reduced motion");
    });
  });

  // G15 — docs + locales.
  await check(T.G15, "docs and locales", async () => {
    const doc = readFileSync(join(root, "docs", "agents", "polyforge-panel.md"), "utf8");
    for (const word of ["declared_steps", "on_fail", "graph", "rewind", "started_at", "Verbose", "chip"]) assert.ok(new RegExp(word, "i").test(doc), `docs do not mention ${word}`);
    assert.match(doc, /on_fail[^\n]*(drop|strip|never)|(drop|strip|never)[^\n]*on_fail/i, "docs do not say on_fail is dropped");
    const keys = (loc) => new Set([...readFileSync(join(root, "lib", "i18n", "messages", `${loc}.ts`), "utf8").matchAll(/"(polyforge\.[^"]+)":/g)].map((m) => m[1]));
    const en = keys("en");
    for (const loc of ["zh-CN", "zh-TW"]) assert.deepEqual([...keys(loc)].sort(), [...en].sort(), `${loc} polyforge keys differ from en`);
  });
} catch (error) {
  console.error(error);
  process.exitCode = 1;
} finally {
  await h.close().catch(() => {});
  reportTitled(failures, ran);
  if (process.exitCode) console.log(`Artifacts kept: ${work}`); else rmSync(work, { recursive: true, force: true });
}
