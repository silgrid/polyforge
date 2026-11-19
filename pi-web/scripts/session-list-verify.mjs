#!/usr/bin/env node
// polyforge#140: the independent session-list oracles (AC1–AC5). Everything a
// human or the verification step runs on the synthetic corpus; nothing here is
// wired into the app. Usage (run from pi-web/):
//
//   node scripts/session-list-verify.mjs <mode> --fixture "$F" --out "$O/<mode>" [options]
//
// Modes (the work item's acceptance commands, one mode per invocation):
//   responsiveness  --modes summary,normal,force --static /icons/icon-192.png
//                     --interval-ms 100 --min-samples 100 --p99-ms 200
//     During each listed /api/sessions call, an independent probe (it never
//     waits for the previous probe to finish) times the static asset; the run
//     fails when a mode's overlapping-sample p99 reaches its bound.
//   read-volume     --modes normal,summary --calls 2 --max-second-read-ratio 0.05
//     The next-server process's /proc/<pid>/io rchar delta across consecutive
//     unchanged calls, against the corpus total from the fixture manifest.
//   cold-summary    --empty-index --max-ms 1000 --max-read-ratio 0.10
//     A cold process's first summary call: wall time and rchar (startup reads
//     measured separately so a scan cannot be hidden before readiness).
//   golden          --baseline bdbbd6e9 --candidate HEAD
//     The baseline commit (a worktree with its own npm ci) and the candidate
//     serve the same corpus; the lists are compared field by field, missing
//     fields included, order included. Transient envelope fields (generation
//     counters) are listed separately, never silently dropped.
//   mutations       --harnesses pi,claude --changes create,append,delete
//     A write finishes (fsync, close) and the very next ordinary call must
//     reflect it — no sleep, no watcher wait, no force.
//
// Common contract: every mode starts its own `next start` against the
// production build (BUILD_DIR, default .next) with a fully synthetic
// environment (HOME, PI_CODING_AGENT_DIR, CLAUDE_CONFIG_DIR, XDG_CONFIG_HOME,
// TMPDIR under the fixture; POLYFORGE_DATA_DIR pointed at a scratch dir); the
// readiness probe is the static asset, never the session list; the measured
// process is the next-server child, found through /proc by a unique env marker
// (so rchar counts every worker thread's reads). --dry-run validates
// everything and prints the plan without spawning anything.
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import process from "node:process";

const MODES = new Set(["responsiveness", "read-volume", "cold-summary", "golden", "mutations"]);
const CALL_MODES = new Set(["summary", "normal", "force"]);
const HARNESS_PATTERN = /^(pi|claude)$/;
const CHANGE_PATTERN = /^(create|append|delete|replace|truncate|rename|sidecar|index)$/;

function parseArgs(argv) {
  const options = { _: [] };
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (!arg.startsWith("--")) {
      options._.push(arg);
      continue;
    }
    const key = arg.slice(2);
    const next = argv[index + 1];
    if (next === undefined || next.startsWith("--")) {
      options[key] = true;
    } else {
      options[key] = next;
      index += 1;
    }
  }
  return options;
}

function usage(message) {
  if (message) console.error(`session-list-verify: ${message}`);
  console.error("usage: node scripts/session-list-verify.mjs <mode> --fixture DIR --out DIR [--dry-run] [mode options]");
  console.error(`modes: ${[...MODES].join(", ")}`);
  process.exit(2);
}

function numberArg(options, name, { required = false, min, max } = {}) {
  if (options[name] === undefined) {
    if (required) usage(`missing --${name}`);
    return undefined;
  }
  const value = Number(options[name]);
  if (!Number.isFinite(value)) usage(`--${name} must be a number`);
  if (min !== undefined && value < min) usage(`--${name} must be >= ${min}`);
  if (max !== undefined && value > max) usage(`--${name} must be <= ${max}`);
  return value;
}

function listArg(options, name, pattern, { required = true } = {}) {
  if (options[name] === undefined) {
    if (required) usage(`missing --${name}`);
    return [];
  }
  const known = (value) => (pattern instanceof Set ? pattern.has(value) : pattern.test(value));
  const values = String(options[name]).split(",").map((value) => value.trim()).filter(Boolean);
  for (const value of values) {
    if (!known(value)) usage(`--${name}: unknown value "${value}"`);
  }
  return values;
}

function readManifest(fixture) {
  const candidates = [join(fixture, "manifest.json"), join(fixture, "..", "manifest.json")];
  for (const path of candidates) {
    if (!existsSync(path)) continue;
    const manifest = JSON.parse(readFileSync(path, "utf8"));
    if (manifest.schema !== "session-list-fixture/1") usage(`${path}: not a session-list-fixture/1 manifest`);
    return manifest;
  }
  usage(`no manifest.json under --fixture ${fixture}`);
}

function resolveRevision(revision) {
  const result = spawnSync("git", ["rev-parse", `${revision}^{commit}`], { encoding: "utf8" });
  if (result.status !== 0) usage(`cannot resolve revision "${revision}": ${result.stderr.trim()}`);
  return result.stdout.trim();
}

function plan(mode, options) {
  const fixture = resolve(String(options.fixture ?? usage("missing --fixture")));
  const out = resolve(String(options.out ?? usage("missing --out")));
  const manifest = readManifest(fixture);
  const buildDir = resolve(String(options["build-dir"] ?? ".next"));
  const staticPath = String(options.static ?? "/icons/icon-192.png");
  const port = numberArg(options, "port", { min: 1024, max: 65535 }) ?? 0; // 0 = pick a free port at run time
  const details = {
    mode,
    fixture,
    out,
    buildDir,
    staticPath,
    port,
    manifestTotals: manifest.totals,
  };
  if (mode === "responsiveness") {
    details.modes = listArg(options, "modes", CALL_MODES);
    details.intervalMs = numberArg(options, "interval-ms", { min: 1 }) ?? 100;
    details.minSamples = numberArg(options, "min-samples", { min: 1 }) ?? 100;
    details.p99Ms = numberArg(options, "p99-ms", { min: 1 }) ?? 200;
    // The measurement window is the call itself, cold and hot apart: a cold
    // run never warms the route first, and its samples come only from windows
    // overlapping a call, accumulated across independent cold starts until the
    // sample bound is met (review r2#7). The probe is open-loop — scheduled
    // every interval, never waiting for the previous one — because a stalled
    // main thread must surface as many slow samples, not the one a
    // request-response loop can squeeze in (review r3#6); only probes issued
    // inside a measured call's window count, so idle samples never dilute the
    // p99 (review r3#5).
    details.shapes = ["cold", "hot"];
    details.warmCalls = 0;
    details.probe = "open-loop";
    details.overlapOnly = true;
    details.hotIterations = 200;
    details.coldStartsMax = 10;
  } else if (mode === "read-volume") {
    details.modes = listArg(options, "modes", CALL_MODES);
    details.calls = numberArg(options, "calls", { min: 2 }) ?? 2;
    details.maxSecondReadRatio = numberArg(options, "max-second-read-ratio", { min: 0, max: 1 }) ?? 0.05;
    // Each mode measures on its own server from a cold index, in the order the
    // caller named: no mode's run can warm another's (review r2#8).
    details.independentServers = true;
  } else if (mode === "cold-summary") {
    details.emptyIndex = options["empty-index"] !== false;
    details.maxMs = numberArg(options, "max-ms", { min: 1 }) ?? 1000;
    details.maxReadRatio = numberArg(options, "max-read-ratio", { min: 0, max: 1 }) ?? 0.10;
    // The warm-index shape's persisted index is built by a prior run of this
    // same oracle, never assumed (review r2 N12) — and the build round waits,
    // bounded, for the index file to appear on disk before its server stops:
    // a SIGTERM before the scanner's trailing 5 s persist timer fired measured
    // an empty index (review r3#7).
    details.buildsWarmIndex = true;
    details.warmIndexPersisted = true;
    details.warmIndexPersistTimeoutMs = 20_000;
  } else if (mode === "golden") {
    details.baseline = resolveRevision(String(options.baseline ?? "bdbbd6e9"));
    details.candidate = String(options.candidate ?? "HEAD");
    details.transientFields = String(options["transient-fields"] ?? "sessionListVersion").split(",").map((value) => value.trim()).filter(Boolean);
  } else if (mode === "mutations") {
    details.harnesses = listArg(options, "harnesses", HARNESS_PATTERN);
    details.changes = listArg(options, "changes", CHANGE_PATTERN);
  }
  return details;
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  const mode = options._[0] ?? options.mode;
  if (!mode || !MODES.has(mode)) usage(mode ? `unknown mode "${mode}"` : "missing mode");
  const details = plan(mode, options);
  if (options["dry-run"]) {
    console.log(JSON.stringify({ ok: true, dryRun: true, ...details }, null, 2));
    return;
  }
  const implementation = await import("./session-list-verify-run.mjs");
  await implementation.run(details, options);
  return;
}

main().catch((error) => {
  console.error(`session-list-verify: ${error?.stack ?? error}`);
  process.exit(1);
});
