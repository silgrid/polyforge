// carries: components/PolyforgePanel*, hooks/usePolyforge.*, lib/polyforge-status*, lib/polyforge-pf-bin*, app/api/polyforge/status/*, components/PolyforgeJobComposer*, lib/polyforge-conversation-pure*, lib/polyforge-cards-pure*, app/api/polyforge/watch/*
// carries: components/AppShell*, app/api/polyforge/polyforge-routes*, app/api/polyforge/polyforge-status-route*, lib/i18n/*, docs/agents/polyforge-panel.md
// pi#108 (wi_y3y9SMz4IXKg) — Polyforge dock panel: end-to-end acceptance pass.
//
// Authored pre-code against artifacts/spec.md (AC1–AC10; AC11's e2e half is this
// file running against a stub `pf` on PATH; AC12 is regression-only and stays with
// e2e/run.mjs). Standalone, like e2e/terminal.mjs: it starts its own dev server
// twice — phase A with no `pf` reachable on PATH, phase B with a stub `pf` first
// on PATH — and matches the new surfaces only by HTTP routes named in the spec and
// by user-visible roles/text ("Polyforge" button/panel, mirroring bg-tasks markup).
// Each scenario is self-guarding: it logs PASS/FAIL and the run exits 1 on any FAIL.
//
// (owner-intentional change, by ruling — not a regression): the
// pi#112 event tail, its simple/verbose mode toggle, the persisted
// `pi-web:polyforge-event-mode` preference and the collapsed `sync ×N` row are
// gone. The N2–N9 pins below now assert the #238 destination with the same
// fixture: each retained event of the WI112 tail is one line of the job
// detail's execution progress (glyph + whitelisted fields, ruled tone, raw kind
// on hover), read through the real progress route from a throwaway capsule
// (its event log carries the same 25 events, plus a spawn journal and a pi
// transcript); the noise and hidden kinds produce no line; no mode control
// exists and the old stored key is never read or written. needs_recovery and
// push_failed now show their recorded cause by an owner ruling at review, so
// their causes moved from the never-leaks sentinels to FAULT112 — still asserted
// absent from the watch route, whose sanitizer is unchanged (N1).
//
//   node e2e/polyforge.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { polyforgeDock as pfPanel, polyforgeToggle as pfButton } from "./polyforge-dock.mjs";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, reportTitled, root, writeFileAtomic, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
mkdirSync(project);
const SESSION = "e2e-polyforge-session";
const ts = "2026-10-18T00:00:00.000Z";
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E polyforge host session", timestamp: ts });

// ---- stub pf (state.json + argv.log next to it; absolute node shebang) ----
const STALE_KEY = "e2e-stale-key-SECRETVALUE-7731";
const QUESTION = "E2E-PENDING-QUESTION-TEXT which colour?";
const LONG_CANDIDATE = "claude:claude-opus-5-5-extended-context-e2e";
// pi#112 (wi_5qd5PgbQT1Gw) — readable event tail. A second watch fixture whose
// 25-event tail exercises every whitelisted kind/field, noise runs of 5 and 1,
// every severity kind, an unknown kind, and a sentinel in every NON-whitelisted
// body field (the owner-approved never-leaks list).
const SLUG112 = "pi#112";
const WI112 = "wi_E2Edddd0004";
const MODEL112 = "example-gateway/model-a-extended-thinking-very-long-e2e-suffix-abcdef0123456789abcdef0123456789";
const SHORT_MODEL112 = "pi:glm-5.3-short";
const FROM112 = "pi:glm-e2e-old-777";
const TO112 = "claude:opus-e2e-new-888";
// owner-intended change (owner decision 2026-11-09): a needs_input
// question and its id reach the watch response, so the question is no longer a
// sentinel here (QUESTION112, asserted present in N1).
const QUESTION112 = "E2E-QUESTION-112 which way?";
const SECRETS112 = [
  "E2E-SECRET-DETAIL-112", "E2E-SECRET-EVIDENCE-112",
  "E2E-SECRET-ANSWERED-BY-112", "E2E-SECRET-LOCAL-COMMIT-112",
  "E2E-SECRET-FILES-112", "E2E-SECRET-ARCHIVED-112", "E2E-SECRET-REASON-112",
  "E2E-SECRET-CS-CAUSE-112", "E2E-SECRET-FROM-MODEL-112", "E2E-SECRET-TO-MODEL-112",
  "E2E-SECRET-DISPOSITION-112", "E2E-SECRET-WORKER-COMMAND-112", "fp-secret-session-112",
];
// the fault causes the progress shows by ruling; the watch route never does.
const FAULT112 = { reason: "E2E-FAULT-REASON-112", detail: "E2E-FAULT-DETAIL-112", error: "E2E-FAULT-PUSH-ERROR-112 git@10.0.0.5:internal/e2e.git" };
const SECRETS = ["E2E_ENV_NAME_SENTINEL", "/srv/e2e-secret-workspace", "E2E-SECRET-WORKER-COMMAND", "E2E-SECRET-EVENT-BODY", "fp-secret-session", ...SECRETS112];
const job = (id, slug, status, spawn, step, idle, candidate, owner) => ({ work_item_id: id, slug, status, spawn_n: spawn,
  session_fingerprint: "fp-secret-session", step_age_sec: step, last_work_ago_sec: idle, candidate, origin: { kind: "chat", owner } });
const JOBS = [
  job("wi_E2Eaaaa0001", "pi#108", "running", 3, 7325, 4337, LONG_CANDIDATE, "u_alice"),
  job("wi_E2Ebbbb0002", "demo#7", "running", 1, 61, 5, "pi:glm-e2e", "u_bob"),
  job("wi_E2Ecccc0003", "pi#109", "needs_input", 2, 900, 880, "codex:gpt-e2e", "svc_ci"),
  { work_item_id: WI112, slug: SLUG112, status: "running", spawn_n: 2, step_age_sec: 42, last_work_ago_sec: 9,
    candidate: "codex:e2e-watch", session_fingerprint: "fp-secret-session-112", origin: { kind: "chat", owner: "u_e2e" } },
];
// owner-intended change: the selected job's progress read looks the wi up in the daemon's own job
// record; a throwaway data root lists every fixture job (no capsule → 200 `available:false`), as a real
// daemon lists every job it runs, instead of the host's real data root answering 400 for jobs it never ran.
const dataRoot = join(work, "pf-data"); mkdirSync(dataRoot);
writeFileSync(join(dataRoot, "runtime-state.json"), JSON.stringify({ version: 1, jobs: JOBS.map((j) => ({ work_item_id: j.work_item_id })) }));
const ops = (jobs, running = 2) => ({ machine_id: "m-e2e", workspaces: ["/srv/e2e-secret-workspace"], workflows_dir: "/srv/e2e-secret-workspace/wf",
  env_names: ["POLYFORGE_V1_API_KEY", "E2E_ENV_NAME_SENTINEL"], jobs, queue: { pending: 0, running, max_concurrent: 4 } });
const at = (n) => `2026-10-18T10:${String(n - 1).padStart(2, "0")}:00Z`;
// Execution order differs from alphabetical order on purpose.
const STEP_ORDER = ["zulu-grill", "mike-spec", "alpha-plan", "kilo-author", "bravo-code"];
const WATCH_108 = { work_item: "wi_E2Eaaaa0001", slug: "pi#108", known: true, job: { status: "running", origin: { owner: "u_alice" } },
  execution: { workflow_status: "running", current_step: "kilo-author", updated_at: at(60), steps: {
    "alpha-plan": { status: "done", started_at: "2026-10-18T08:10:00Z" }, "bravo-code": { status: "pending" },
    "kilo-author": { status: "in_progress", started_at: "2026-10-18T08:15:00Z" },
    "mike-spec": { status: "done", started_at: "2026-10-18T08:05:00Z" }, "zulu-grill": { status: "done", started_at: "2026-10-18T08:00:00Z" } } },
  progress: { command: "worker run --token=E2E-SECRET-WORKER-COMMAND-0123456789 --flag", tool_calls: 9 },
  events: Array.from({ length: 60 }, (_, i) => ({ at: at(i + 1), kind: i % 2 ? "published" : "step_started", work_item: "wi_E2Eaaaa0001",
    body: { step: "kilo-author", spawn_n: 3, detail: `E2E-SECRET-EVENT-BODY-${i + 1}` }, seq: i + 1 })), last_seq: 60 };
const WATCH_109 = { work_item: "wi_E2Ecccc0003", slug: "pi#109", known: true, job: { status: "needs_input" },
  execution: { workflow_status: "blocked", current_step: "zulu-grill", updated_at: at(3), pending_question: { id: "q109", step: "zulu-grill", question: QUESTION },
    steps: { "zulu-grill": { status: "blocked", started_at: "2026-10-18T09:00:00Z" } } },
  events: [{ at: at(3), kind: "needs_input", work_item: "wi_E2Ecccc0003", body: { question: QUESTION, question_id: "q109", step: "zulu-grill" }, seq: 3 }], last_seq: 3 };
// pi#112 tail (newest last): a step_started, a 5-event noise run
// (published/transcript_missing/exec_init/published/published), 13 consecutive
// meaningful events covering every whitelisted field, an isolated noise event,
// three terminal statuses, and an unknown kind. Bodies mirror the live pf shapes
// (grill-qa evidence table) with sentinels in every non-whitelisted field.
const at2 = (n) => `2026-10-18T11:${String(n - 1).padStart(2, "0")}:00Z`;
const ev112 = (n, kind, body) => ({ at: at2(n), kind, work_item: WI112, body, seq: 100 + n });
const published112 = (n) => ev112(n, "published", { version: 9, local_commit: "E2E-SECRET-LOCAL-COMMIT-112", commit_ms: 10, push_ms: 5, pushed: true });
const EVENTS_112 = [
  ev112(1, "step_started", { step: "zulu-grill", model: SHORT_MODEL112, effort: "high", resume: false, spawn_n: 2 }),
  published112(2),
  ev112(3, "transcript_missing", { detail: "E2E-SECRET-DETAIL-112", spawn_n: 2 }),
  ev112(4, "exec_init", { spawn_n: 2, steps: ["E2E-SECRET-ARCHIVED-112"] }),
  published112(5),
  published112(6),
  ev112(7, "step_started", { step: "kilo-author", model: MODEL112, effort: "high", resume: false, spawn_n: 2 }),
  ev112(8, "candidate_switched", { step: "alpha-plan", from: FROM112, to: TO112, cause: "E2E-SECRET-CS-CAUSE-112" }),
  ev112(9, "step_failed", { step: "bravo-code", cause: "agent-exit-13", detail: "E2E-SECRET-DETAIL-112", disposition: "E2E-SECRET-DISPOSITION-112", failure_count: 2, from_model: "E2E-SECRET-FROM-MODEL-112", to_model: "E2E-SECRET-TO-MODEL-112", spawn_n: 2 }),
  ev112(10, "step_dead", { step: "mike-spec", cause: "timeout-after-3-retries", evidence: "E2E-SECRET-EVIDENCE-112", spawn_n: 2 }),
  ev112(11, "needs_input", { step: "zulu-grill", question: QUESTION112, question_id: "q112", spawn_n: 2 }),
  ev112(12, "answered", { step: "zulu-grill", question_id: "q112", answered_by: "E2E-SECRET-ANSWERED-BY-112", spawn_n: 2 }),
  ev112(13, "gate_verdict", { step: "alpha-plan", spawn_n: 2 }),
  ev112(14, "rewind", { gate: "spec-review", target: "kilo-author", archived: ["E2E-SECRET-ARCHIVED-112"], spawn_n: 2 }),
  ev112(15, "envelope_rejected", { step: "bravo-code", detail: "E2E-SECRET-DETAIL-112", spawn_n: 2 }),
  ev112(16, "needs_recovery", { reason: FAULT112.reason, detail: FAULT112.detail, spawn_n: 2 }),
  ev112(17, "push_failed", { error: FAULT112.error, version: 9 }),
  ev112(18, "job_started", { spawn_n: 2 }),
  ev112(19, "job_stopped", { spawn_n: 2 }),
  ev112(20, "authored_tests_rebaselined", { step: "kilo-author", files: ["E2E-SECRET-FILES-112"], spawn_n: 2 }),
  ev112(21, "transcript_missing", { detail: "E2E-SECRET-DETAIL-112", spawn_n: 2 }),
  ev112(22, "terminal", { status: "completed", reason: "E2E-SECRET-REASON-112", spawn_n: 2 }),
  ev112(23, "terminal", { status: "failed", reason: "E2E-SECRET-REASON-112", spawn_n: 2 }),
  ev112(24, "terminal", { status: "cancelled", reason: "E2E-SECRET-REASON-112", spawn_n: 2 }),
  ev112(25, "future_kind_e2e", { step: "kilo-author", detail: "E2E-SECRET-DETAIL-112", spawn_n: 2 }),
];
// the WI112 capsule the progress route reads — the same 25
// events in the daemon's event log, one spawn in the journal, its pi transcript.
const capsule112 = join(work, "capsules", WI112);
mkdirSync(join(capsule112, "runtime-state"), { recursive: true });
mkdirSync(join(capsule112, "session"), { recursive: true });
mkdirSync(join(dataRoot, "events"), { recursive: true });
writeFileSync(join(dataRoot, "events", `wi-${WI112}.jsonl`), EVENTS_112.map((event) => JSON.stringify(event)).join("\n") + "\n");
const transcript112 = join(capsule112, "session", "kilo-author.jsonl");
writeFileSync(transcript112, [
  { type: "session", id: "s112", timestamp: at2(7) },
  { type: "message", id: "n112", parentId: null, timestamp: "2026-10-18T11:06:30Z", message: { role: "assistant", content: [{ type: "text", text: "E2E-PI112-NARRATION" }] } },
  { type: "message", id: "t112", parentId: null, timestamp: "2026-10-18T11:06:40Z", message: { role: "assistant", content: [{ type: "thinking", thinking: "E2E-PI112-THINKING" }, { type: "toolCall", id: "c", name: "bash", arguments: { command: "echo token=E2E-SECRET-TOKEN-112" } }] } },
].map((record) => JSON.stringify(record)).join("\n") + "\n");
writeFileSync(join(capsule112, "runtime-state", "spawn-journal.jsonl"), JSON.stringify({ at: at2(7), work_item: WI112, step_id: "kilo-author", spawn_n: 2, started_at: at2(7), transcript: transcript112, harness: "pi" }) + "\n");
writeFileSync(join(dataRoot, "runtime-state.json"), JSON.stringify({ version: 1, jobs: JOBS.map((j) => (j.work_item_id === WI112 ? { work_item_id: WI112, capsule_dir: capsule112 } : { work_item_id: j.work_item_id })) }));
const WATCH_112 = { work_item: WI112, slug: SLUG112, known: true, job: { status: "running" },
  execution: { workflow_status: "running", current_step: "kilo-author", updated_at: at2(25),
    steps: { "zulu-grill": { status: "done", started_at: "2026-10-18T09:00:00Z" },
      "mike-spec": { status: "done", started_at: "2026-10-18T09:10:00Z" },
      "alpha-plan": { status: "done", started_at: "2026-10-18T09:20:00Z" },
      "kilo-author": { status: "in_progress", started_at: "2026-10-18T09:30:00Z" },
      "bravo-code": { status: "pending" } } },
  progress: { command: "worker run --token=E2E-SECRET-WORKER-COMMAND-112-0123456789 --flag", tool_calls: 3 },
  events: EVENTS_112, last_seq: 125 };
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const setState = (patch) => writeFileAtomic(stateFile, JSON.stringify({ mode: "ok", ops: ops(JOBS), watch: { "pi#108": WATCH_108,
  wi_E2Eaaaa0001: WATCH_108, "pi#109": WATCH_109, wi_E2Ecccc0003: WATCH_109, [SLUG112]: WATCH_112, [WI112]: WATCH_112 }, ...patch }));
setState({});
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename)); const args = process.argv.slice(2);
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
if (args.includes("--version")) { console.log("pf 0.0.0-e2e"); process.exit(0); }
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const out = (v) => { process.stdout.write(JSON.stringify(v) + "\\n"); process.exit(0); };
const run = () => {
  if (s.mode === "down") { process.stderr.write("pf: daemon not reachable (connection refused)\\n"); process.exit(1); }
  if (s.mode === "auth") { process.stderr.write("pf: 401 unauthorized (invalid api key)\\n"); process.exit(1); }
  const rest = args.filter((a) => a !== "--json");
  if (rest[0] === "ops" && rest[1] === "status") out(s.ops);
  if (rest[0] === "watch") {
    const i = rest.indexOf("--events"); const n = i >= 0 ? Number(rest[i + 1]) : 8;
    const w = s.watch[rest[1]] || { work_item: rest[1], known: false, events: [], last_seq: 0 };
    out({ ...w, events: (w.events || []).slice(-n) });
  }
  process.stderr.write("pf: unknown command\\n"); process.exit(2);
};
if (s.mode === "hang") setTimeout(run, 30000); else run();
`);
chmodSync(join(stubDir, "pf"), 0o755);
// Phase A PATH: every PATH dir holding a pf is replaced by a symlink farm without it.
const absentPath = (process.env.PATH || "").split(delimiter).map((dir, n) => {
  if (!existsSync(join(dir, "pf"))) return dir;
  const farm = join(work, `nopf-${n}`);
  mkdirSync(farm);
  for (const name of readdirSync(dir)) if (name !== "pf") symlinkSync(join(dir, name), join(farm, name));
  return farm;
}).join(delimiter);

// ---- scenario bookkeeping ----
const T = {
  AC1: "AC1: Topbar Polyforge button renders iff the server finds pf",
  AC2: "AC2: Badge polls every 3s open / 30s closed, only with pf, no badge when daemon down",
  AC3: "AC3: Unavailable answers 200 {status:null}, malformed ?wi= answers 400, no 5xx or console errors",
  AC4: "AC4: Job list shows every job on the machine with its fields and the queue badge",
  AC5: "AC5: Selected WI shows header, ordered steps and the last 40 events",
  AC6: "AC6: Selected WI stays selected after it leaves the job list",
  AC7: "AC7: Blocked WI shows its status, and its needs_input question verbatim with its id",
  AC8: "AC8: Routes validate refs before argv, time out to unavailable and leak no env values or hidden fields",
  AC9: "AC9: Panel docks 460px (resizable) right on desktop / 62% bottom sheet on mobile, sharing the bg-tasks slot",
  AC10: "AC10: Panel strings are localized in zh-CN and zh-TW",
  // pi#112 (wi_5qd5PgbQT1Gw) — readable event tail, authored pre-code against
  // artifacts/spec.md. One scenario per new-behaviour acceptance criterion.
  N1: "N1: Server whitelist exposes per-kind body fields, never non-whitelisted values",
  N2: "N2: Lifecycle lines render readable with whitelisted context (en; formerly event rows)",
  N3: "N3: zh-CN and zh-TW show the same glyph lines under a localized heading",
  N4: "N4: Dead kind labels and i18n keys removed from all three locales",
  N5: "N5: Noise kinds produce no line and no sync row (formerly the simple-mode collapse)",
  N6: "N6: No event mode exists and the hidden kinds produce no line (formerly verbose mode)",
  N7: "N7: Raw kind on every lifecycle line's hover title",
  N8: "N8: Severity emphasis: amber, red, green, neutral (plus accent starts)",
  N9: "N9: The retired event-mode preference is never read or written and changes nothing",
  N10: "N10: Sanitization docs describe the watch whitelist and the progress line whitelist",
};
const failures = new Map(Object.values(T).map((title) => [title, []]));
const ran = new Set();
const check = createCheck({ failures: ([title, label], text) => failures.get(title).push(`${label}: ${text}`), before: ([title]) => ran.add(title), pass: null, fail: null });
// A check that drives its own fresh browser page (a failure stays local to the scenario).
const checkPage = (title, label, opts, fn) => check(title, label, () => h.withPage(opts, fn));

let base; const serverLogs = [];
async function get(path) {
  const response = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(30_000) });
  const text = await response.text();
  let json = null; try { json = JSON.parse(text); } catch {}
  return { status: response.status, text, json };
}
const statusRequests = [];
const h = createHarness({
  // `locale` is the page's language only (the context stays en-US); none: the browser's.
  shape: (o) => ({ ...o, locale: "en-US", storage: { "pi-web:split-view-enabled": "false", ...o.storage, "pi-locale": o.locale } }),
  errors: ["pageerror", "console", "5xx"],
  setup(page) {
    page.on("request", (r) => { if (new URL(r.url()).pathname === "/api/polyforge/status") statusRequests.push(Date.now()); });
  },
  // Navigation headroom only: the navigation and the readiness wait use
  // NAV_MS (e2e/timeouts.mjs), since a cold `next dev` compile under host load
  // outlasts the 10s action default that assertions keep. Readiness is
  // locale-independent: the host session's first message is mounted.
  session: SESSION,
  ready: "E2E polyforge host session",
  server(name, path) {
    const logFile = join(work, `server-${name}.log`); serverLogs.push(logFile);
    return { name, logFile, env: { PATH: path, PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: STALE_KEY, POLYFORGE_DATA_DIR: dataRoot } };
  },
  started: (started) => { base = started.base; },
});
const { errors, startServer } = h;
async function openPanel(page) { await pfButton(page).click(); await pfPanel(page).first().waitFor(); return pfPanel(page).first(); }
const countSince = (t0) => statusRequests.filter((t) => t >= t0).length;
// (declared scope extension, owner-answered rewind): since
// (18ceaea2) the server shares one `pf ops status` read between
// the status and cards polls and reuses a completed read for
// OPS_STATUS_REUSE_MS (lib/polyforge-status.ts). After a fixture switch an
// answer can still carry the pre-switch snapshot, so each status assertion
// below first waits (bounded at 2s, polling every 100ms) on the switch's
// observable effect — the status route's body, or the stub argv log for a
// hanging read — and then asserts exactly as before: only the wait is
// legitimate, no assertion is relaxed.
const STATUS_SWITCH_WAIT_MS = 2000;
const STATUS_SWITCH_POLL_MS = 100;
async function awaitStatusCondition(describe, predicate) {
  const until = Date.now() + STATUS_SWITCH_WAIT_MS;
  let lastSeen = "no status answer";
  for (;;) {
    try {
      const r = await get("/api/polyforge/status");
      if (predicate(r)) return r;
      lastSeen = `status ${r.status} ${r.text.slice(0, 120)}`;
    } catch (error) {
      lastSeen = `request failed: ${String(error?.message ?? error)}`;
    }
    if (Date.now() >= until) {
      throw new Error(`waited ${STATUS_SWITCH_WAIT_MS}ms for ${describe}; last answer: ${lastSeen}`);
    }
    await delay(STATUS_SWITCH_POLL_MS);
  }
}
const awaitStatusUnavailable = (what) => awaitStatusCondition(
  `${what}: /api/polyforge/status answers status:null with an error`,
  (r) => r.json?.status === null && typeof r.json?.error === "string" && r.json.error.length > 0,
);
const awaitStatusQueueRunning = (running, what) => awaitStatusCondition(
  `${what}: /api/polyforge/status queue.running === ${running}`,
  (r) => r.json?.status?.queue?.running === running,
);
// A hanging `pf` writes its argv before it stalls, so the argv log shows the
// post-switch read starting without waiting for that read to finish. Polling
// the route starts the read; the status GETs that return the warm cached body
// answer at once, and the ones that join the hanging read are bounded by the
// route's own read timeout.
async function awaitOpsStatusReadAfterSwitch() {
  const before = argvCalls().length;
  const until = Date.now() + STATUS_SWITCH_WAIT_MS;
  for (;;) {
    if (argvCalls().slice(before).some((args) => args.length === 3 && args[0] === "--json" && args[1] === "ops" && args[2] === "status")) return;
    if (Date.now() >= until) {
      throw new Error(`waited ${STATUS_SWITCH_WAIT_MS}ms for a post-switch pf --json ops status invocation in the stub argv log`);
    }
    void get("/api/polyforge/status").catch(() => {});
    await delay(STATUS_SWITCH_POLL_MS);
  }
}
function arrayOf(value, pred) { // first array (deep) whose object items satisfy pred
  if (Array.isArray(value) && value.length && value.every((item) => item && typeof item === "object" && pred(item))) return value;
  for (const child of value && typeof value === "object" ? Object.values(value) : []) { const hit = arrayOf(child, pred); if (hit) return hit; }
  return null;
}
const values = (o) => (o && typeof o === "object" ? Object.values(o).flatMap(values) : [o]);
// AC8's straggler filter: the argv of a page's own watch read
// (lib/polyforge-status.ts): `[--profile <cell>] --json watch <ref> --once`,
// optionally followed by `--demand --events <n>`, on a fixture work item, and
// nothing else.
const FIXTURE_REFS = new Set([...JOBS.flatMap((j) => [j.work_item_id, j.slug]), SLUG112, WI112]);
const isPagePoll = (argv) => {
  const a = argv[0] === "--profile" ? argv.slice(2) : argv;
  if (a.length < 4 || a[0] !== "--json" || a[1] !== "watch" || !FIXTURE_REFS.has(a[2]) || a[3] !== "--once") return false;
  const tail = a.slice(4);
  return tail.length === 0 || (tail.length === 3 && tail[0] === "--demand" && tail[1] === "--events" && /^\d+$/.test(tail[2]));
};
// The dock's decision feed rides every status read (
// lib/polyforge-decisions.ts): exactly `--json watch decisions`, Host-level, no
// ref and no flag. A closed page's in-flight status read can land it after
// `before` too; anything longer or different is still a leak.
const isDecisionFeedRead = (argv) => argv.length === 3 && argv[0] === "--json" && argv[1] === "watch" && argv[2] === "decisions";
// Self-check: a malformed ref that leaked split still counts.
assert.equal(isPagePoll(["--json", "watch", "pi#108", "--chat", "x", "--once", "--demand", "--events", "40"]), false, "AC8 filter: a split bad ref must be flagged");
assert.equal(isPagePoll(["--json", "watch", "pi#108", "--once", "--demand", "--events", "40"]), true, "AC8 filter: the page's own poll must be recognised");
assert.equal(isDecisionFeedRead(["--json", "watch", "decisions"]), true, "AC8 filter: the dock's decision-feed read must be recognised");
assert.equal(isDecisionFeedRead(["--json", "watch", "decisions", "--once"]), false, "AC8 filter: a longer decisions call must be flagged");
assert.equal(isDecisionFeedRead(["--profile", "x", "--json", "watch", "decisions"]), false, "AC8 filter: the feed is Host-level, a profiled one must be flagged");
const argvCalls = () => (existsSync(argvLog) ? readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((l) => JSON.parse(l)) : []);

// ---- progress-line helpers (pi#112's event-row pins, moved): a
// lifecycle line is a `[data-pf-progress-kind="event"]` element of the job
// detail's execution progress; its hover title starts with the raw kind. The
// colour checks read the computed colour a user sees. ----
const eventLines = (panel, kind) => panel.first().evaluate((root, kind) => [...root.querySelectorAll('[data-pf-progress] [data-pf-progress-kind="event"]')]
  .filter((el) => kind === null || el.getAttribute("title").split("\n")[0] === kind)
  .map((el) => {
    const m = /rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/.exec(getComputedStyle(el).color);
    const [r, g, b] = m ? [Number(m[1]), Number(m[2]), Number(m[3])] : [0, 0, 0];
    const max = Math.max(r, g, b); const min = Math.min(r, g, b);
    const family = max - min < 25 ? "gray" : r >= max && r > b + 40 && g > b + 20 && r > g + 25 ? "amber"
      : r >= max && r > g + 40 && r > b + 40 ? "red" : g >= max && g > r + 25 ? "green" : "other";
    const cs = getComputedStyle(el);
    return { kind: el.getAttribute("title").split("\n")[0], title: el.getAttribute("title"), text: (el.textContent || "").trim(),
      tone: el.getAttribute("data-pf-progress-tone"), family, ellipsis: cs.whiteSpace === "nowrap" && cs.textOverflow === "ellipsis" };
  }), kind ?? null);
const lineBody = (line) => line?.text.replace(/^\d{2}:\d{2}:\d{2} /, "");
const RETAINED112 = ["step_started", "step_failed", "step_dead", "needs_input", "answered", "gate_verdict", "rewind", "needs_recovery", "push_failed", "job_stopped", "terminal"];
const DROPPED112 = ["published", "transcript_missing", "exec_init", "job_started", "candidate_switched", "envelope_rejected", "authored_tests_rebaselined", "future_kind_e2e"];
// The expected line bodies (clock stripped) — glyphs and recorded field
// values only, identical in every locale.
const LINES112 = [
  ["step_started", `▶ zulu-grill #2 · ${SHORT_MODEL112}`], ["step_started", `▶ kilo-author #2 · ${MODEL112}`],
  ["step_failed", "✕ bravo-code · agent-exit-13"], ["step_dead", "✕ mike-spec · timeout-after-3-retries"],
  ["needs_input", "? zulu-grill"], ["answered", "✓ zulu-grill"], ["gate_verdict", "◆ alpha-plan"],
  ["rewind", "↺ spec-review → kilo-author"], ["needs_recovery", `⚠ ${FAULT112.reason} · ${FAULT112.detail}`],
  ["push_failed", `⚠ ${FAULT112.error}`], ["job_stopped", "⏸"],
  ["terminal", "⏹ completed"], ["terminal", "⏹ failed"], ["terminal", "⏹ cancelled"],
];
// owner-intended change: each spawn block's group head absorbs its
// step_started line and its terminal line (its last step_done / step_failed /
// step_dead — here step_dead in the kilo-author block): those two kinds of line
// are no longer body rows; each absorbed line's raw kind and full text are on
// the head's title (`<kind>\n<text>`, entries separated by a blank line). The
// whole retained list is still pinned: the marker rows plus the head lines are
// exactly LINES112.
const ABSORBED112 = [
  [["step_started", `▶ zulu-grill #2 · ${SHORT_MODEL112}`]],
  [["step_started", `▶ kilo-author #2 · ${MODEL112}`], ["step_dead", "✕ mike-spec · timeout-after-3-retries"]],
];
const MARKERS112 = LINES112.filter(([kind]) => kind !== "step_started" && kind !== "step_dead");
const headLines = (panel) => panel.first().evaluate((root) => [...root.querySelectorAll("[data-pf-progress] [data-pf-progress-block-toggle]")].map((el) => {
  const cs = getComputedStyle(el);
  return { status: el.closest("[data-pf-progress-block]").getAttribute("data-pf-progress-block-status"), text: (el.textContent || "").trim(),
    ellipsis: cs.whiteSpace === "nowrap" && cs.textOverflow === "ellipsis",
    lines: (el.getAttribute("title") || "").split("\n\n").map((entry) => { const at = entry.indexOf("\n"); return { kind: entry.slice(0, at), text: entry.slice(at + 1) }; }) };
}));
// two spawns form two folded blocks and only the newest is
// expanded by default. A scenario that pins the WHOLE retained line list (or
// both step_started lines) expands every block first; no line is dropped, so
// the old full-list pins stay intact.
async function expandProgressBlocks(panel) {
  for (let attempt = 0; attempt < 20; attempt++) {
    const collapsed = panel.locator('[data-pf-progress-block-toggle][aria-expanded="false"]');
    if ((await collapsed.count()) === 0) return;
    await collapsed.first().evaluate((el) => el.click());
  }
  throw new Error("a progress block stayed folded");
}

async function ensureDetail112(page) {
  // The pi#112 detail is showing iff its title row names the slug and its
  // execution progress has rendered lifecycle lines. The job card TOGGLES
  // selection, so never blind-click it: verify first, click only when the
  // detail is absent, retry bounded (a click during a select-fetch gap
  // deselects; the next attempt re-selects).
  const showing = async (panel) => (await panel.locator("[data-pf-detail-title]").getByText(SLUG112, { exact: true }).count()) > 0
    && (await panel.locator('[data-pf-progress] [data-pf-progress-kind="event"]').count()) > 0;
  for (let attempt = 0; attempt < 20; attempt++) {
    if ((await pfPanel(page).count()) === 0) { await openPanel(page); continue; }
    const panel = pfPanel(page).first();
    if (await showing(panel)) return panel;
    // Work (the first top-level tab at every locale), then its Jobs sub-tab
    // (the first), before looking for the fixture card.
    const workTab = panel.locator('[data-pf-tabstrip="top"] [role="tab"]').first();
    if ((await workTab.count()) > 0 && (await workTab.getAttribute("aria-selected")) !== "true") {
      await workTab.click({ timeout: 2000 }).catch(() => {});
      await panel.locator('[data-pf-tabstrip="work"]').waitFor({ timeout: 5_000 }).catch(() => {});
      continue;
    }
    const jobsTab = panel.locator('[data-pf-tabstrip="work"] [role="tab"]').first();
    if ((await jobsTab.count()) > 0 && (await jobsTab.getAttribute("aria-selected")) !== "true") {
      await jobsTab.click({ timeout: 2000 }).catch(() => {});
      await panel.locator(`[data-pf-card-wi="${WI112}"]`).waitFor({ timeout: 5_000 }).catch(() => {});
      continue;
    }
    const card112 = panel.locator(`[data-pf-card-wi="${WI112}"]`);
    if ((await card112.count()) === 0) { await card112.waitFor({ timeout: 5_000 }).catch(() => {}); continue; }
    if ((await panel.locator("[data-pf-detail-title]").getByText(SLUG112, { exact: true }).count()) === 0) await card112.click().catch(() => {});
    // Wait for the selection's own progress read (bounded), not a fixed sleep.
    await panel.locator('[data-pf-progress] [data-pf-progress-kind="event"]').first().waitFor({ state: "attached", timeout: 10_000 }).catch(() => {});
  }
  throw new Error("the pi#112 detail did not render");
}
const MODE_LABELS = /^(simple|verbose|简洁|详细|簡潔|詳細)$/i;

try {
  await h.launchBrowser(() => chromium.launch());
  // ================= Phase A: no pf on PATH =================
  await startServer("absent", absentPath);
  let absentBody = null;
  await check(T.AC3, "pf absent: status route", async () => {
    const r = await get("/api/polyforge/status");
    assert.equal(r.status, 200, `status route answered ${r.status}`); assert.equal(r.json?.status, null); absentBody = r.json;
  });
  await check(T.AC3, "pf absent: watch route", async () => {
    const r = await get("/api/polyforge/watch?wi=pi%23108");
    assert.equal(r.status, 200, `watch route answered ${r.status}`); assert.equal(r.json?.status, null);
  });
  // (owner-intended contract change, not a weakened pin): while
  // pf is absent the client re-probes the status route every 30 s and on
  // window focus, so the 33 s window from this page's mount probe holds the
  // mount probe plus one re-probe (a focus re-probe may add one).
  const beforeAbsentPage = statusRequests.length;
  await checkPage(T.AC1, "pf absent: page", {}, async (page) => {
    for (const deadline = Date.now() + NAV_MS; statusRequests.length <= beforeAbsentPage && Date.now() < deadline;) await delay(100);
    assert.ok(statusRequests.length > beforeAbsentPage, "the page issued no mount status probe");
    const t0 = statusRequests[beforeAbsentPage];
    await delay(Math.max(0, t0 + 33_000 - Date.now()));
    const inWindow = statusRequests.filter((t) => t >= t0 && t <= t0 + 33_000).length;
    await check(T.AC1, "pf absent: button", async () => assert.equal(await pfButton(page).count(), 0, "button rendered without pf"));
    await check(T.AC2, "pf absent: polling", async () => assert.ok(inWindow >= 2 && inWindow <= 3, `${inWindow} status requests in the 33s from the mount probe without pf (want the mount probe plus the 30s re-probe, at most one focus re-probe)`));
  });
  await check(T.AC3, "pf absent: error signals", async () => assert.deepEqual(errors.splice(0), []));
  await h.stop();

  // ================= Phase B: stub pf first on PATH =================
  await startServer("stub", `${stubDir}${delimiter}${process.env.PATH}`);
  await checkPage(T.AC1, "pf present: button opens the panel", {}, async (page) => { await openPanel(page); });

  // AC2 — cadence and badge
  await checkPage(T.AC2, "cadence and badge", {}, async (page) => {
    await pfButton(page).waitFor();
    assert.match(await pfButton(page).innerText(), /2/, "closed badge must show queue running 2");
    let t0 = Date.now(); await delay(35_000);
    const closed = countSince(t0);
    assert.ok(closed >= 1 && closed <= 2, `closed panel: ${closed} polls in 35s (want 30s cadence)`);
    await openPanel(page);
    t0 = Date.now(); await delay(10_000);
    const open = countSince(t0);
    assert.ok(open >= 2 && open <= 5, `open panel: ${open} polls in 10s (want 3s cadence)`);
    setState({ ops: ops(JOBS, 3) });
    await pfButton(page).filter({ hasText: "3" }).waitFor({ timeout: 8_000 });
  });
  setState({});
  await awaitStatusQueueRunning(2, "AC4 after the AC2 cadence restore");

  // AC4 — job list
  await check(T.AC4, "status route fields", async () => {
    const r = await get("/api/polyforge/status");
    assert.equal(r.status, 200); assert.ok(r.json?.status, "status must be non-null with the daemon up");
    const leaves = values(r.json);
    for (const j of JOBS) for (const v of [j.slug, j.status, j.spawn_n, j.step_age_sec, j.last_work_ago_sec, j.candidate]) {
      assert.ok(leaves.includes(v), `status route lacks ${j.slug} field value ${v}`);
    }
    assert.ok(leaves.includes(2) && leaves.includes(4), "status route lacks queue running/max");
  });
  await checkPage(T.AC4, "panel list", {}, async (page) => {
    const panel = await openPanel(page);
    await panel.getByText("demo#7").first().waitFor();
    const text = await panel.innerText();
    for (const j of JOBS) assert.ok(text.includes(j.slug), `list lacks ${j.slug} (owner ${j.origin.owner})`);
    for (const v of ["needs_input|needs input", "running"]) assert.match(text, new RegExp(v, "i"));
    assert.match(text, /2\s*\/\s*4/, "queue running/max badge 2/4 missing");
    // (owner-approved intentional change ③): the candidate left
    // the card for the job detail, still shortened to 24 chars + ….
    assert.ok(!text.includes(LONG_CANDIDATE.slice(0, 24)), "the candidate is no longer on the card");
    await panel.getByText("pi#108", { exact: true }).first().click();
    await panel.getByRole("button", { name: /back to job list|deselect/i }).waitFor({ timeout: 15_000 });
    const detail = await panel.innerText();
    assert.ok(detail.includes(`${LONG_CANDIDATE.slice(0, 24)}…`) && !detail.includes(LONG_CANDIDATE), "candidate not shortened to 24 chars + … in the detail");
  });

  // AC5 — watch route and detail
  await check(T.AC5, "watch route", async () => {
    const r = await get("/api/polyforge/watch?wi=pi%23108");
    assert.equal(r.status, 200); assert.ok(r.json?.status, "watch status must be non-null");
    const steps = arrayOf(r.json, (s) => values(s).some((v) => STEP_ORDER.includes(v)));
    assert.deepEqual(steps?.map((s) => values(s).find((v) => STEP_ORDER.includes(v))), STEP_ORDER, "steps not in execution order");
    const events = arrayOf(r.json, (e) => values(e).some((v) => typeof v === "string" && v.startsWith("2026-10-18T10:")));
    assert.equal(events?.length, 40, `events tail has ${events?.length} entries, want 40`);
    const times = values(events);
    assert.ok(times.includes(at(60)) && times.includes(at(21)) && !times.includes(at(20)), "tail is not the newest 40");
    const call = argvCalls().findLast((a) => a.includes("watch"));
    assert.ok(call && call[call.indexOf("--events") + 1] === "40", `pf watch argv ${JSON.stringify(call)} does not ask for 40 events`);
    // pi#119: every watch read arms the summarize lane — --demand is always
    // on the argv (the stub pf ignores it; the route never omits it).
    assert.ok(call?.includes("--demand"), `pf watch argv ${JSON.stringify(call)} does not arm the summarize lane (--demand)`);
  });
  await checkPage(T.AC5, "detail panel", {}, async (page) => {
    const panel = await openPanel(page);
    await panel.getByText("pi#108", { exact: true }).first().click();
    await panel.getByText("zulu-grill").first().waitFor();
    // pi#114/#116: the step names render only as flow-graph nodes (no step-chip
    // row in any mode), so this order check guards the graph; the strict
    // node-order/edge contract lives in e2e/polyforge-graph.mjs.
    const text = await panel.innerText();
    const idx = STEP_ORDER.map((s) => text.indexOf(s));
    assert.ok(idx.every((i) => i >= 0), `steps missing: ${JSON.stringify(idx)}`);
    assert.ok(idx[0] < idx[1] && idx[1] < idx[2], "steps not in execution order");
  });

  // AC6 — selection persists
  await checkPage(T.AC6, "selection persists", {}, async (page) => {
    const panel = await openPanel(page);
    await panel.getByText("pi#108", { exact: true }).first().click();
    await panel.getByText("zulu-grill").first().waitFor();
    const watch = JSON.parse(readFileSync(stateFile, "utf8")).watch;
    delete watch["pi#108"]; delete watch.wi_E2Eaaaa0001;
    setState({ ops: ops(JOBS.slice(1), 1), watch });
    await delay(9_000);
    const text = await panel.innerText();
    assert.ok(text.includes("pi#108") && text.includes("zulu-grill") && text.includes("mike-spec"), "selected WI snapshot dropped after it left the list");
  });
  setState({});

  // AC7 — blocked WI
  await checkPage(T.AC7, "blocked detail", {}, async (page) => {
    const r = await get("/api/polyforge/watch?wi=pi%23109");
    assert.equal(r.status, 200); assert.ok(r.json?.status, "watch status must be non-null");
    // owner-intended change (owner decision 2026-11-09): a
    // needs_input event carries its question text and question id to the
    // browser (pi#112's whitelist relaxed for that kind only).
    const asked = r.json.status.events.find((event) => event.kind === "needs_input");
    assert.deepEqual([asked?.question, asked?.question_id], [QUESTION, "q109"], "the watch route carries the needs_input question and its id");
    // owner-intended change pi#109 waits for an answer, so the
    // Polyforge button's opening walks to its detail.
    const panel = await openPanel(page);
    // owner-intended change: the workflow status moved from the old header's visible text into the
    // title row's hover title (the row shows the job status); the blocked status is read there.
    const titleRow = panel.locator('[data-pf-detail-title][title*="Blocked" i]').first();
    await titleRow.waitFor();
    assert.match(await titleRow.innerText(), /pi#109/, "the opening walked to the waiting pi#109");
    assert.match(await titleRow.getAttribute("title") ?? "", /blocked/i, "the detail shows the blocked workflow status");
    assert.ok(!(await titleRow.getAttribute("title") ?? "").includes("E2E-PENDING-QUESTION-TEXT"), "the hover shows the pending question");
    // owner-intended change (workflow contract v2, change 14): the question
    // shows verbatim in the job card's conversation — highlighted above its
    // one input box, with its id — only there; the job card has no
    // Interactions button.
    const question = panel.locator("[data-pf-chat-question]");
    await question.locator("[data-pf-chat-question-text]").waitFor();
    assert.equal(await question.locator("[data-pf-chat-question-text]").innerText(), QUESTION, "the composer shows the question verbatim");
    assert.equal(await question.getAttribute("data-pf-chat-question-id"), "q109", "the composer names the question id");
    assert.equal(await panel.getByRole("button", { name: /^(Interactions|交互)/ }).count(), 0, "the job card has no Interactions button,");
  });

  // AC8 — argv safety, hidden fields, env leakage, timeout
  await check(T.AC8, "bad ref never reaches argv", async () => {
    const before = argvCalls().length;
    for (const bad of ["pi%23108%20--chat%20x", "--help", "wi_x%3Brm%20-rf"]) {
      const r = await get(`/api/polyforge/watch?wi=${bad}`);
      assert.equal(r.status, 400, `?wi=${bad} answered ${r.status}`);
    }
    // The page the previous check closed can still land its own watch poll
    // here (its request was already at the server when the page went), which
    // on a loaded host arrives after `before` and failed this check. Only a
    // call of exactly a page read's shape on a fixture work item is that
    // poll, and exactly `--json watch decisions` its status read's decision
    // feed; every other watch call is a leak — a bad ref passed whole, split
    // (`watch pi#108 --chat x …`) or truncated included.
    const leaked = argvCalls().slice(before).filter((a) => a.includes("watch") && !isPagePoll(a) && !isDecisionFeedRead(a));
    assert.deepEqual(leaked, [], `a malformed ref reached pf argv: ${JSON.stringify(leaked)}`);
  });
  await check(T.AC8, "hidden fields", async () => {
    const s = await get("/api/polyforge/status"); const w = await get("/api/polyforge/watch?wi=pi%23108");
    assert.ok(s.json?.status && w.json?.status, "routes must answer non-null status with the daemon up");
    for (const secret of SECRETS) assert.ok(!s.text.includes(secret) && !w.text.includes(secret), `response leaks ${secret}`);
  });
  await check(T.AC8, "stale key auth failure", async () => {
    setState({ mode: "auth" });
    await awaitStatusUnavailable("AC8 stale key auth failure");
    const r = await get("/api/polyforge/status");
    assert.equal(r.status, 200); assert.equal(r.json?.status, null);
    assert.match(String(r.json?.error), /401 unauthorized/, "pf's own stderr must surface");
    assert.ok(!r.text.includes(STALE_KEY), "response leaks an env value");
  });
  await check(T.AC8, "hang times out to unavailable", async () => {
    setState({ mode: "hang" });
    await awaitOpsStatusReadAfterSwitch();
    const t0 = Date.now(); const r = await get("/api/polyforge/status");
    assert.ok(r.status === 200 && r.json && r.json.status === null, `hang answered ${r.status} ${r.text.slice(0, 80)}`);
    assert.ok(Date.now() - t0 < 20_000, `hang took ${Date.now() - t0}ms (want ~10s timeout)`);
  });

  // AC3/AC2 — daemon down
  setState({ mode: "down" });
  await awaitStatusUnavailable("AC3/AC2 daemon down");
  await check(T.AC3, "daemon down routes", async () => {
    const s = await get("/api/polyforge/status"); const w = await get("/api/polyforge/watch?wi=pi%23108");
    assert.ok(s.status === 200 && s.json && s.json.status === null && s.json.error, `status route: ${s.status} ${s.text.slice(0, 80)}`);
    assert.ok(w.status === 200 && w.json && w.json.status === null && w.json.error, `watch route: ${w.status} ${w.text.slice(0, 80)}`);
    assert.notDeepEqual(s.json, absentBody, "pf presence is not distinguishable from pf absence");
  });
  await check(T.AC3, "malformed wi 400", async () => {
    const r = await get("/api/polyforge/watch?wi=not%20a%20ref");
    assert.equal(r.status, 400); assert.ok(r.json?.error, "400 needs a field-level error body");
  });
  await checkPage(T.AC2, "no badge when daemon down", {}, async (page) => {
    await pfButton(page).waitFor(); await delay(3_000);
    assert.doesNotMatch(await pfButton(page).innerText(), /\d/, "badge shown while the daemon is down");
    await openPanel(page);
  });
  setState({});

  // AC9 — dock slot
  // pi#117: the desktop dock opens at 460 by default (resizable via its
  // left-edge handle; the e2e asserts the default width only — the clamp,
  // persistence and dismissal-exemption contracts are pinned by unit tests).
  // owner-intended change: the pi session's top bar no longer carries
  // a "Background tasks" toggle. This scenario now pins that absence and that
  // the Polyforge dock still opens at 460px; the bg-tasks/Polyforge mutual
  // exclusion is pinned by components/AppShell.polyforge.test.mjs (the
  // notification-jump source pin; owner-approved narrowing, grill row 4).
  await checkPage(T.AC9, "desktop dock, no bg-tasks toggle", {}, async (page) => {
    assert.equal(
      await page.getByRole("button", { name: "Background tasks", exact: true }).count(),
      0,
      "the pi session's top bar still has a \"Background tasks\" button",
    );
    const box = await (await openPanel(page)).boundingBox();
    assert.ok(Math.abs(box.width - 460) <= 2, `desktop dock is ${box.width}px wide`);
  });
  // 600px: inside the app's mobile breakpoint (<= 640px) but above the narrow
  // (<= 480px) layout that tucks toolbar actions behind a "more" menu.
  await checkPage(T.AC9, "mobile sheet", { viewport: { width: 600, height: 900 } }, async (page) => {
    const panel = await openPanel(page);
    const ratio = await panel.evaluate((el) => el.getBoundingClientRect().height / el.offsetParent.getBoundingClientRect().height);
    assert.ok(ratio > 0.3 && ratio <= 0.63, `mobile sheet covers ${ratio} of the height`);
  });

  // AC10 — locales
  for (const locale of ["zh-CN", "zh-TW"]) {
    await checkPage(T.AC10, locale, { locale }, async (page) => {
      const text = await (await openPanel(page)).innerText();
      assert.match(text, /[一-鿿]/, `${locale} panel has no translated strings`);
      assert.doesNotMatch(text, /polyforge\.[a-z]/i, `${locale} panel shows a raw i18n key`);
    });
  }

  // ================= pi#112 (wi_5qd5PgbQT1Gw): readable event tail =================
  // Authored pre-code against artifacts/spec.md's Acceptance criteria. The
  // pi#112 stub watch tail (EVENTS_112) drives every new behaviour; the
  // pi#108 fixtures above are untouched, so the existing scenarios keep
  // guarding the pi#108 bar (including the E2E-SECRET-EVENT-BODY sentinel —
  // now also enforced for the widened pi#112 bodies via SECRETS112).

  // N1 — server-side whitelist: per-kind body fields pass, nothing else
  // (the change leaves the watch sanitizer unchanged).
  await check(T.N1, "watch route whitelist", async () => {
    const r = await get(`/api/polyforge/watch?wi=pi%23112`);
    assert.equal(r.status, 200, `watch route answered ${r.status}`);
    assert.ok(r.json?.status, "watch status must be non-null for pi#112");
    const events = arrayOf(r.json, (e) => values(e).some((v) => typeof v === "string" && v.startsWith("2026-10-18T11:")));
    assert.ok(events, "no events array in the watch response");
    const eventsJson = JSON.stringify(events);
    for (const v of [MODEL112, FROM112, TO112, "agent-exit-13", "timeout-after-3-retries", "spec-review", "kilo-author", "completed", "failed", "cancelled", "future_kind_e2e"]) {
      assert.ok(eventsJson.includes(v), `events lack whitelisted value ${JSON.stringify(v)}`);
    }
    for (const kind of ["answered", "authored_tests_rebaselined", "future_kind_e2e"]) {
      const event = events.find((e) => e && e.kind === kind);
      assert.ok(event, `${kind} event missing from the tail`);
      assert.ok(JSON.stringify(event).includes("zulu-grill") || JSON.stringify(event).includes("kilo-author"), `${kind} event lost its step`);
    }
    // owner-intended change: needs_input carries its question and id, nothing else of its body.
    const asked = events.find((e) => e && e.kind === "needs_input");
    assert.deepEqual(asked && { step: asked.step, question: asked.question, question_id: asked.question_id }, { step: "zulu-grill", question: QUESTION112, question_id: "q112" }, "needs_input carries its question and id");
    assert.equal(asked.spawn_n, undefined, "needs_input passes no other body field");
    const switchedEvent = events.find((e) => e && e.kind === "candidate_switched");
    assert.ok(switchedEvent && JSON.stringify(switchedEvent).includes("alpha-plan"), "candidate_switched.step is not exposed by the watch route");
    for (const secret of [...SECRETS112, ...Object.values(FAULT112)]) assert.ok(!r.text.includes(secret), `watch response leaks ${secret}`);
  });

  // N2 — readable lines (en): each retained event is one line, glyph +
  // whitelisted fields, in event order; the long model wraps in full (no
  // ellipsis); nothing else leaks.
  await checkPage(T.N2, "readable lines", {}, async (page) => {
    const panel = await ensureDetail112(page);
    await expandProgressBlocks(panel);
    const lines = await eventLines(panel, null);
    // owner-intended change: the marker rows plus the group heads' absorbed
    // lines are the whole retained list (see ABSORBED112).
    assert.deepEqual(lines.map((line) => [line.kind, lineBody(line)]), MARKERS112, `the lifecycle marker rows: ${JSON.stringify(lines.map((line) => line.text))}`);
    const heads = await headLines(panel);
    assert.deepEqual(heads.map((head) => head.lines.map((line) => [line.kind, lineBody(line)])), ABSORBED112, `the heads' absorbed lines: ${JSON.stringify(heads)}`);
    assert.equal(lines.length + heads.flatMap((head) => head.lines).length, LINES112.length, "no retained line is dropped");
    for (const line of [...lines, ...heads.flatMap((head) => head.lines)]) assert.match(line.text, /^\d{2}:\d{2}:\d{2} /, `each line starts with its clock: ${line.text}`);
    // owner-intended change: the long model rode the kilo-author start line,
    // which the head absorbs — it is on the head's title in full, and neither the
    // head nor any marker row is ellipsized.
    const long = heads[1].lines.find((line) => line.text.includes(MODEL112));
    assert.ok(long && !heads.some((head) => head.ellipsis) && !lines.some((line) => line.ellipsis), `the long model line stays whole, not ellipsized: ${JSON.stringify(heads[1])}`);
    assert.ok(heads[1].text.includes("kilo-author #2"), `the head names the absorbed start's step and spawn: ${heads[1].text}`);
    const text = await panel.innerText();
    // owner-intended change: the tool line is the shared tool card — its
    // header shows the name and the masked argument, its title the full header.
    const toolTitles = await panel.locator('[data-pf-progress] [data-pf-progress-kind="tool"] button[aria-expanded]').evaluateAll((els) => els.map((el) => el.getAttribute("title")));
    assert.ok(text.includes("E2E-PI112-NARRATION") && text.includes("E2E-PI112-THINKING") && text.includes("echo token=***") && toolTitles.includes("bash: echo token=***"), "the spawn's text, thinking and tool lines share the stream");
    for (const secret of [...SECRETS112, "E2E-SECRET-TOKEN-112"]) assert.ok(!text.includes(secret), `panel leaks ${secret}`);
    const titles = JSON.stringify([...lines.map((line) => line.title), ...heads.flatMap((head) => head.lines), ...toolTitles]);
    for (const secret of [...SECRETS112, "E2E-SECRET-TOKEN-112"]) assert.ok(!titles.includes(secret), `a hover title leaks ${secret}`);
  });

  // N3 — zh-CN and zh-TW: the same lines (glyphs and recorded values need no
  // per-kind words), a translated progress heading, no raw polyforge.* key.
  for (const [locale, heading] of [["zh-CN", "执行进度"], ["zh-TW", "執行進度"]]) {
    await checkPage(T.N3, locale, { locale }, async (page) => {
      const panel = await ensureDetail112(page);
      await expandProgressBlocks(panel);
      const lines = await eventLines(panel, null);
      // owner-intended change: marker rows + the heads' absorbed lines.
      assert.deepEqual(lines.map((line) => [line.kind, lineBody(line)]), MARKERS112, `${locale}: the lifecycle marker rows`);
      assert.deepEqual((await headLines(panel)).map((head) => head.lines.map((line) => [line.kind, lineBody(line)])), ABSORBED112, `${locale}: the heads' absorbed lines`);
      assert.equal(await panel.locator("[data-pf-progress]").getByText(heading, { exact: true }).count(), 1, `${locale}: the progress heading is translated`);
      assert.doesNotMatch(await panel.innerText(), /polyforge\.[a-z]/i, `${locale} panel shows a raw i18n key`);
    });
  }

  // N4 — the three never-occurring kind labels and their i18n keys are gone.
  await check(T.N4, "message files", async () => {
    for (const loc of ["en", "zh-CN", "zh-TW"]) {
      const source = readFileSync(join(root, "lib", "i18n", "messages", `${loc}.ts`), "utf8");
      for (const kind of ["step_blocked", "job_done", "job_blocked"]) {
        assert.ok(!source.includes(`polyforge.eventKind.${kind}`), `${loc} still ships the dead label ${kind}`);
      }
    }
  });

  // N5 — (was: simple mode collapses noise runs) the noise kinds produce no
  // line and no collapsed `sync ×N` row; meaningful events never collapse.
  await checkPage(T.N5, "no noise, no sync row", {}, async (page) => {
    const panel = await ensureDetail112(page);
    await expandProgressBlocks(panel);
    for (const kind of ["published", "transcript_missing", "exec_init", "job_started"]) {
      assert.equal((await eventLines(panel, kind)).length, 0, `${kind} produces no line`);
    }
    assert.equal(await panel.getByText(/sync\s*[×x]\s*\d/i).count(), 0, "no collapsed sync row");
    assert.equal((await eventLines(panel, "terminal")).length, 3, "terminal events never collapse");
    // owner-intended change: both step_started lines live on their group heads.
    const starts = (await headLines(panel)).flatMap((head) => head.lines).filter((line) => line.kind === "step_started");
    assert.equal(starts.length, 2, "step_started events never collapse");
  });

  // N6 — (was: verbose mode) there is no mode: no control switches the
  // stream, and the hidden kinds produce no line in any state.
  await checkPage(T.N6, "no mode, hidden kinds", {}, async (page) => {
    const panel = await ensureDetail112(page);
    assert.equal(await panel.getByRole("button", { name: MODE_LABELS }).count(), 0, "no simple/verbose control exists");
    for (const kind of DROPPED112) assert.equal((await eventLines(panel, kind)).length, 0, `${kind} produces no line`);
    const text = await panel.locator("[data-pf-progress]").innerText();
    for (const value of [FROM112, TO112, "future_kind_e2e"]) assert.ok(!text.includes(value), `a hidden kind's field shows: ${value}`);
  });

  // N7 — the raw kind is one hover away on every lifecycle line.
  await checkPage(T.N7, "hover titles", {}, async (page) => {
    const panel = await ensureDetail112(page);
    // owner-intended change: an absorbed start/terminal line's raw kind and
    // full text are on its group head's title; every other kind is a marker row.
    const absorbed = (await headLines(panel)).flatMap((head) => head.lines);
    for (const kind of RETAINED112) {
      const lines = await eventLines(panel, kind);
      const onHeads = absorbed.filter((line) => line.kind === kind);
      assert.ok(lines.length + onHeads.length >= 1, `no line carries the raw kind ${kind} on hover`);
      for (const line of lines) assert.equal(line.title, `${kind}\n${line.text}`, `${kind}: the hover is the raw kind and the full line`);
      for (const line of onHeads) assert.match(line.text, /^\d{2}:\d{2}:\d{2} [▶■✕]/, `${kind}: the head's hover is the raw kind and the full line (${line.text})`);
    }
  });

  // N8 — severity: needs_input amber; failures and faults red; terminal
  // completed green, failed red, cancelled neutral; the rest neutral; starts
  // accent. Asserted on the tone hook and on the computed colour alike.
  await checkPage(T.N8, "severity", {}, async (page) => {
    const panel = await ensureDetail112(page);
    const first = async (kind) => (await eventLines(panel, kind))[0];
    const needsInput = await first("needs_input");
    assert.ok(needsInput.tone === "amber" && needsInput.family === "amber", `the needs_input line is not amber: ${JSON.stringify(needsInput)}`);
    for (const kind of ["step_failed", "push_failed", "needs_recovery"]) {
      const line = await first(kind);
      assert.ok(line.tone === "red" && line.family === "red", `the ${kind} line is not red: ${JSON.stringify(line)}`);
    }
    // owner-intended change: the step_dead line is its block's absorbed
    // terminal — the block's head carries the failed status and its ✕ mark.
    const heads = await headLines(panel);
    assert.ok(heads[1].lines.some((line) => line.kind === "step_dead") && heads[1].status === "failed" && heads[1].text.includes("✕"), `the step_dead block reads failed: ${JSON.stringify(heads[1])}`);
    const terminals = await eventLines(panel, "terminal");
    assert.equal(terminals.length, 3, `expected 3 terminal lines, found ${terminals.length}`);
    assert.ok(terminals[0].tone === "green" && terminals[0].family === "green", "the terminal completed line is not green");
    assert.ok(terminals[1].tone === "red" && terminals[1].family === "red", "the terminal failed line is not red");
    const plainColour = (line) => !["red", "amber", "green"].includes(line.family);
    assert.ok(terminals[2].tone === "neutral" && plainColour(terminals[2]), `the terminal cancelled line is not neutral: ${JSON.stringify(terminals[2])}`);
    for (const kind of ["answered", "gate_verdict", "rewind", "job_stopped"]) {
      const line = await first(kind);
      assert.ok(line.tone === "neutral" && plainColour(line), `the ${kind} line is not neutral: ${JSON.stringify(line)}`);
    }
    // owner-intended change: start lines are absorbed into the group heads;
    // the older head carries no mark, the head of the block with a failure ✕.
    assert.deepEqual(heads.map((head) => head.status), ["none", "failed"], "each head carries its block's status");
  });

  // N9 — (was: the toggle persists) the toggle is gone and the old
  // `pi-web:polyforge-event-mode` key is never read or written: a page with
  // the key seeded renders the same lines, and the key stays as seeded.
  await checkPage(T.N9, "retired preference", {}, async (page) => {
    const plain = await eventLines(await ensureDetail112(page), null);
    const context = await h.browser.newContext({ viewport: { width: 1280, height: 800 }, locale: "en-US" });
    try {
      await context.addInitScript(() => {
        try {
          localStorage.setItem("pi-web:split-view-enabled", "false");
          localStorage.setItem("pi-web:polyforge-event-mode", "verbose");
          const touched = [];
          window.__eventModeTouched = touched;
          for (const name of ["getItem", "setItem", "removeItem"]) {
            const original = Storage.prototype[name];
            Storage.prototype[name] = function (key, ...rest) { if (key === "pi-web:polyforge-event-mode") touched.push(name); return original.call(this, key, ...rest); };
          }
        } catch { /* storage unavailable */ }
      });
      const seeded = await context.newPage();
      seeded.setDefaultTimeout(10_000);
      await seeded.goto(`${base}/?session=${SESSION}`, { waitUntil: "domcontentloaded", timeout: NAV_MS });
      await seeded.getByText("E2E polyforge host session").first().waitFor({ state: "attached", timeout: NAV_MS });
      const lines = await eventLines(await ensureDetail112(seeded), null);
      assert.deepEqual(lines.map((line) => [line.kind, line.text, line.tone]), plain.map((line) => [line.kind, line.text, line.tone]), "the seeded key changes nothing");
      assert.equal(await pfPanel(seeded).first().getByRole("button", { name: MODE_LABELS }).count(), 0, "no mode control appears for the seeded key");
      assert.deepEqual(await seeded.evaluate(() => window.__eventModeTouched), [], "the retired key is never read, written or cleared");
      assert.equal(await seeded.evaluate(() => localStorage.getItem("pi-web:polyforge-event-mode")), "verbose", "the seeded value stays as it was");
    } finally {
      await context.close();
    }
  });

  // N10 — the Sanitization docs change with the code: the watch route's
  // per-kind whitelist and the progress lifecycle lines' field whitelist.
  await check(T.N10, "sanitization docs", async () => {
    const doc = readFileSync(join(root, "docs", "agents", "polyforge-panel.md"), "utf8");
    const section = /## Sanitization[\s\S]*?(?=\n## |$)/.exec(doc)?.[0] ?? "";
    assert.ok(section.length > 0, "the doc has no Sanitization section");
    for (const word of ["step_started", "model", "candidate_switched", "terminal", "status", "question", "error"]) {
      assert.ok(new RegExp(word, "i").test(section), `the Sanitization section does not mention ${word}`);
    }
    for (const needle of ["**Progress lifecycle lines.**", "`composeProgressEventLine`", "`needs_recovery` → `reason` + `detail`", "`push_failed` → `error`", "`candidate_switched`, `envelope_rejected`, `authored_tests_rebaselined`"]) {
      assert.ok(section.includes(needle), `the Sanitization section does not describe ${needle}`);
    }
  });

  // N11 — DELETED before completion: "N11: Unknown kind renders its raw kind
  // string" PASSED on the pre-implementation tree (pi#108's eventKindLabel
  // already passes unknown kinds through raw) — existing behaviour, recorded
  // as already_satisfied. Its never-a-raw-polyforge.*-key half stays guarded
  // by the AC10 locale checks, which now also cover the pi#112 kinds.
  await check(T.AC3, "pf present: no 5xx or console errors", async () => assert.deepEqual(errors.splice(0), []));
  await h.stop();
  await check(T.AC8, "no env value in server logs", async () => {
    for (const file of serverLogs) assert.ok(!readFileSync(file, "utf8").includes(STALE_KEY), `${file} contains an env value`);
  });
} catch (error) {
  console.error(error);
  process.exitCode = 1;
} finally {
  await h.close().catch(() => {});
  reportTitled(failures, ran);
  if (process.exitCode) console.log(`Artifacts kept: ${work}`); else rmSync(work, { recursive: true, force: true });
}
