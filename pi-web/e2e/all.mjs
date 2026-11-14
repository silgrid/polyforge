// Parallel runner for every standalone e2e suite.
//
//   npm run test:e2e:all                 # every suite
//   npm run test:e2e:all -- a.mjs b.mjs  # only those suites
//   E2E_JOBS=2 E2E_SUITE_TIMEOUT_MS=300000 npm run test:e2e:all
//
// Suites are discovered like the release verify-scope engine does: every
// e2e/*.mjs except files other e2e files import (helpers), the conf's
// `exclude=` globs, and this file. Browser suites each start their own
// `next dev`; the runner gives each one its own Next distDir
// (PI_WEB_DIST_DIR=.next/e2e/<stem>) so they can share one checkout, one
// node_modules and one Chromium install (and one running Chromium server, see
// "Shared browser" below). Once every suite has settled, the
// dist dir of each suite that passed is removed (about 360 MB of `next dev`
// output per suite); a failed suite keeps its dir so a re-run of it starts
// warm, and an interrupted run removes nothing. Only
// `.next/e2e/<stem>` is ever removed: `.next/dev` and `.next` are not the
// runner's. Suites that touch host-global resources (tmux, pgrep, ...) are
// not browser suites and run one at a time in a separate serial lane.
// Suites whose assertions are wall-clock responsiveness bounds, or whose
// scenarios are proven load-sensitive on a shared host, opt in
// with the `e2e/all: isolated` marker: they run one at a time after both lanes
// drain, because a second `next dev` + Chromium on the host can push such a
// bound over even though the suite passes alone (full bar:
// 20167ms vs 10s).
// Build-once mode: with E2E_SERVER_MODE=start the runner runs
// `next build --webpack` (the package's `npm run build`) once, into
// `.next/e2e-build`, before the lanes start, and hands it to every suite as
// E2E_PREBUILT_DIST; e2e/next-server.mjs then starts each suite's server with
// `next start` on a private copy of that build in the suite's own
// `.next/e2e/<stem>` (own port, HOME, agent dir and tmux root, as in dev mode).
// E2E_PREBUILT_DIST naming an existing build (relative to this package) skips
// the build, but only when the build's stamp (`e2e-build-stamp.json`, written
// by the runner: this package's git tree, a hash of its uncommitted changes and
// of package-lock.json and next.config.ts) matches the current tree; a stale or
// unstamped dir is refused. `.next/e2e-build` is not a per-suite dir,
// so the cleanup below never removes it; the next start-mode run rebuilds over
// it (warm: webpack's cache is kept). Unset or `dev` keeps every suite on its
// own `next dev`.
// Output is buffered and printed in sorted suite order; the exit code is
// non-zero when any suite fails. A hung suite is killed after the timeout.
// When anything failed, a `===== failures =====` section before the summary
// names each failing unit in one line, `FAIL e2e/<suite>: <scenario> — <first
// message line>` (or `FAIL e2e/<suite>: no scenario marker — <last line>` for a
// crash, a timeout kill or a spawn error), and prints each failing suite's
// targeted re-run, `re-run: npm run test:e2e:all -- <suite>`.
// A suite's own tally line (`FAIL …: <n> … scenario(s) …`, a count of red
// scenarios, not a scenario) is not listed; each failing suite also prints the
// host's load when it settled, `host loadavg 1m/5m/15m: x / y / z`, the
// evidence a load-sensitive flake classification needs.
// Shared browser: before the first browser suite starts the
// runner starts one Chromium server (`chromium.launchServer()`) and names its
// ws endpoint in E2E_BROWSER_WS for every browser suite (the isolated ones
// too); serial-lane suites never get the variable. A suite connects through
// e2e/harness.mjs `launchBrowser` and closing it drops only its own contexts
// and connection; the runner closes the server once every lane has settled
// (or on SIGINT/SIGTERM). When the server process exits on its own, each
// browser suite running on it at that moment gets a runner line
// `[all.mjs] shared browser crashed (Target crashed): …` in its output, so its
// FAIL block reads as a browser crash to verify (units.py BROWSER_CRASH); the
// runner names every crash before the summary, and the next browser suite
// starts a new server.
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { availableParallelism, homedir, loadavg, tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { resolveJobs } from "./runner/jobs.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const self = "all.mjs";
// Browser pool: E2E_JOBS, else min(6, max(2, floor(cpus / 2))) (e2e/runner/jobs.mjs), where cpus is
// availableParallelism(): the CPUs this process may use. os.cpus() counts the host's cores, so in an
// 8-cpu verification container it read 32 and ran 6 browser suites on 8 cpus.
const jobs = resolveJobs(process.env.E2E_JOBS, availableParallelism());
const timeoutMs = Number(process.env.E2E_SUITE_TIMEOUT_MS) || 10 * 60_000;
const serverMode = process.env.E2E_SERVER_MODE || "dev";
if (serverMode !== "dev" && serverMode !== "start") {
  console.error(`E2E_SERVER_MODE must be dev or start, got ${JSON.stringify(serverMode)}`);
  process.exit(2);
}
const sharedBuild = ".next/e2e-build";
const stampFile = "e2e-build-stamp.json";

// what a build was made from. `tree` is this package's tree in
// HEAD (`head` is kept for the message only: a commit outside the package does
// not make the build stale); `dirty` is false or a hash of the uncommitted
// changes under the package (the tracked diff plus every untracked, not
// ignored file); `inputs` hash the two build inputs that sit outside the
// source graph. Without git, `tree` is null and no prebuilt dir is accepted.
function treeStamp() {
  const sha = (data) => createHash("sha256").update(data).digest("hex");
  const file = (rel) => (existsSync(join(root, rel)) ? sha(readFileSync(join(root, rel))) : "absent");
  const stamp = { head: null, tree: null, dirty: null, inputs: { "package-lock.json": file("package-lock.json"), "next.config.ts": file("next.config.ts") } };
  const git = (...args) => spawnSync("git", args, { cwd: root, maxBuffer: 1 << 30 });
  const head = git("rev-parse", "HEAD");
  const tree = git("rev-parse", "HEAD:./");
  const diff = git("diff", "HEAD", "--binary", "--", ".");
  const untracked = git("ls-files", "-o", "--exclude-standard", "-z", "--", ".");
  if ([head, tree, diff, untracked].some((r) => r.status !== 0)) return stamp;
  stamp.head = head.stdout.toString().trim();
  stamp.tree = tree.stdout.toString().trim();
  const extra = untracked.stdout.toString().split("\0").filter(Boolean).sort();
  if (!diff.stdout.length && !extra.length) {
    stamp.dirty = false;
    return stamp;
  }
  const hash = createHash("sha256").update(diff.stdout);
  for (const rel of extra) {
    hash.update(`\0${rel}\0`);
    try { hash.update(readFileSync(join(root, rel))); } catch { /* gone, or not a file */ }
  }
  stamp.dirty = hash.digest("hex");
  return stamp;
}

// Why a prebuilt dir may not be reused for the current tree, or null when it may.
function staleness(dir) {
  const path = resolve(root, dir, stampFile);
  if (!existsSync(path)) return `it has no ${stampFile} (not built by e2e/all.mjs)`;
  let built;
  try { built = JSON.parse(readFileSync(path, "utf8")); } catch { return `its ${stampFile} is unreadable`; }
  const now = treeStamp();
  if (!now.tree) return "the current tree cannot be read with git, so the build's tree cannot be checked";
  const why = [];
  if (built.tree !== now.tree) why.push(`package tree ${String(built.tree).slice(0, 12)} (HEAD ${String(built.head).slice(0, 12)}) is now ${now.tree.slice(0, 12)} (HEAD ${now.head.slice(0, 12)})`);
  if (built.dirty !== now.dirty) why.push(`uncommitted changes ${built.dirty ? "differ" : "were none"} (now ${now.dirty ? "dirty" : "clean"})`);
  for (const [name, digest] of Object.entries(now.inputs)) {
    if (built.inputs?.[name] !== digest) why.push(`${name} changed`);
  }
  return why.length ? why.join("; ") : null;
}

const files = readdirSync(join(root, "e2e")).filter((f) => f.endsWith(".mjs")).sort();
const source = new Map(files.map((f) => [f, readFileSync(join(root, "e2e", f), "utf8")]));
const imported = new Set();
for (const text of source.values()) {
  for (const m of text.matchAll(/from\s+["']\.\/([\w.-]+\.mjs)["']/g)) imported.add(m[1]);
}
const confPath = join(root, ".polyforge/verify-scope.conf");
const excludes = existsSync(confPath)
  ? readFileSync(confPath, "utf8").split("\n").filter((l) => l.startsWith("exclude=")).map((l) => l.slice(8).trim())
  : [];
const globToRe = (g) => new RegExp("^" + g.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*/g, ".*") + "$");
const excluded = (f) => excludes.some((g) => globToRe(g).test(f) || globToRe(g).test(`e2e/${f}`));
const all = files.filter((f) => f !== self && !imported.has(f) && !excluded(f));

const requested = process.argv.slice(2).map((a) => a.replace(/^e2e\//, ""));
const unknown = requested.filter((r) => !all.includes(r));
if (unknown.length) {
  console.error(`Unknown suite(s): ${unknown.join(", ")}\nKnown: ${all.join(" ")}`);
  process.exit(2);
}
const suites = requested.length ? all.filter((f) => requested.includes(f)) : all;
const routingRoot = mkdtempSync(join(tmpdir(), "pi-web-e2e-routing-"));
const browserCache = process.env.PLAYWRIGHT_BROWSERS_PATH ?? (process.platform === "darwin"
  ? join(homedir(), "Library", "Caches", "ms-playwright")
  : join(process.env.XDG_CACHE_HOME || join(homedir(), ".cache"), "ms-playwright"));
process.once("exit", () => {
  for (const suite of readdirSync(routingRoot)) {
    const socket = join(routingRoot, suite, "tmux", `tmux-${process.getuid?.() ?? 0}`, "default");
    if (existsSync(socket)) spawnSync("tmux", ["-S", socket, "kill-server"], { stdio: "ignore" });
  }
  rmSync(routingRoot, { recursive: true, force: true });
});

// Same command as the package.json script that names the suite (e.g. the
// strip-types flag), else plain `node e2e/<suite>`.
const pkg = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
function argsFor(suite) {
  for (const cmd of Object.values(pkg.scripts ?? {})) {
    if (!cmd.startsWith("node ") || !cmd.endsWith(`e2e/${suite}`)) continue;
    return cmd.split(/\s+/).slice(1);
  }
  return [`e2e/${suite}`];
}
const browserMarker = "chromium" + ".launch";
const isBrowser = (suite) => source.get(suite).includes(browserMarker);

// One preflight instead of per-suite installs: dependencies and the browser.
if (!existsSync(join(root, "node_modules"))) {
  console.error("node_modules/ is missing: run `npm ci` once, then re-run.");
  process.exit(2);
}
let chromium = null;
if (suites.some(isBrowser)) {
  ({ chromium } = await import("playwright"));
  if (!existsSync(chromium.executablePath())) {
    console.error("Playwright Chromium is missing: run `npx playwright install chromium` once, then re-run.");
    process.exit(2);
  }
}

// the run's shared browser server, started lazily for the first
// browser suite (one promise, so concurrent lane workers share one start) and
// again after a crash. `onShared` maps each running browser suite to the server
// it was given; `crashNotes` collects the runner lines a crash adds to a suite.
let shared = null;
const onShared = new Map();
const crashNotes = new Map();
const crashes = [];
function sharedBrowser() {
  if (shared) return shared;
  const starting = (async () => {
    // The runner owns SIGINT/SIGTERM (it SIGTERMs the suites and prints its
    // summary); Playwright's own handlers would exit the process first.
    const server = await chromium.launchServer({ handleSIGINT: false, handleSIGTERM: false, handleSIGHUP: false });
    const entry = { server, ws: server.wsEndpoint(), closing: false };
    server.process().once("exit", (code, signal) => {
      if (entry.closing) return;
      if (shared === starting) shared = null;
      const affected = [...onShared].filter(([, e]) => e === entry).map(([suite]) => suite);
      const why = `its server process exited (code ${code}, signal ${signal})`;
      crashes.push({ why, at: new Date().toISOString(), affected });
      for (const suite of affected) crashNotes.get(suite).push(`[all.mjs] shared browser crashed (Target crashed): ${why} while this suite ran`);
    });
    return entry;
  })();
  shared = starting;
  starting.catch(() => { if (shared === starting) shared = null; });
  return starting;
}
// Closes the current server once; a second caller (the end of the run after a
// signal) waits for the same close.
let sharedClosed = null;
function closeSharedBrowser() {
  const current = shared;
  shared = null;
  if (current) {
    sharedClosed = (async () => {
      try {
        const entry = await current;
        entry.closing = true;
        await entry.server.close();
      } catch { /* it never started, or is already gone */ }
    })();
  }
  return sharedClosed ?? Promise.resolve();
}

const running = new Set();
let interrupted = false;
function killGroup(child, signal) {
  try { process.kill(-child.pid, signal); } catch { /* already gone */ }
}
for (const sig of ["SIGINT", "SIGTERM"]) {
  process.once(sig, () => {
    interrupted = true;
    for (const child of running) killGroup(child, "SIGTERM");
    void closeSharedBrowser();
    setTimeout(() => process.exit(130), 11_000).unref();
  });
}

// A suite's environment: the runner's own minus the operator's pf/tmux/XDG and
// Pi Web settings, plus the per-suite values in `own`.
function scrubbedEnv(own) {
  const env = { ...process.env, ...own };
  for (const key of ["PF_BIN", "PF_SERVER", "PF_API_KEY", "PF_DAEMON_SOCKET", "PF_PROFILE", "PF_CONFIG", "TMUX", "TMUX_PANE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"]) delete env[key];
  for (const key of Object.keys(env)) {
    if (key.startsWith("PIWEB_") || (key.startsWith("PI_WEB_") && key !== "PI_WEB_DIST_DIR")) delete env[key];
  }
  return env;
}

// the one `next build` of a start-mode run, in the suites'
// scrubbed environment with its own throwaway HOME and agent dir (a build reads
// no operator state). Its output is printed only when it fails.
function buildOnce() {
  const stamp = treeStamp();
  return new Promise((resolve) => {
    const started = Date.now();
    const scratch = join(routingRoot, "next-build");
    mkdirSync(join(scratch, "home"), { recursive: true });
    mkdirSync(join(scratch, "agent"), { recursive: true });
    const env = scrubbedEnv({ HOME: join(scratch, "home"), PI_CODING_AGENT_DIR: join(scratch, "agent"), PI_WEB_DIST_DIR: sharedBuild });
    const child = spawn(process.execPath, [join(root, "node_modules/next/dist/bin/next"), "build", "--webpack"], {
      cwd: root,
      env,
      stdio: ["ignore", "pipe", "pipe"],
      detached: true,
    });
    running.add(child);
    let output = "";
    child.stdout.on("data", (d) => { output += d; });
    child.stderr.on("data", (d) => { output += d; });
    const timer = setTimeout(() => {
      output += `\n[all.mjs] next build killed after ${timeoutMs}ms timeout\n`;
      killGroup(child, "SIGTERM");
      setTimeout(() => killGroup(child, "SIGKILL"), 10_000).unref();
    }, timeoutMs);
    const finish = (code) => {
      clearTimeout(timer);
      running.delete(child);
      const built = existsSync(join(root, sharedBuild, "BUILD_ID"));
      if (code === 0 && !built) output += `\n[all.mjs] next build exited 0 but left no ${sharedBuild}/BUILD_ID\n`;
      // The stamp names the tree as it was when the build started reading it.
      if (code === 0 && built) writeFileSync(join(root, sharedBuild, stampFile), `${JSON.stringify(stamp, null, 2)}\n`);
      resolve({ ok: code === 0 && built, seconds: Math.round((Date.now() - started) / 1000), output });
    };
    child.once("error", (error) => { output += `\n[all.mjs] spawn error: ${error.message}\n`; finish(1); });
    child.once("close", finish);
  });
}

async function runSuite(suite) {
  let browser = null;
  if (isBrowser(suite)) {
    const started = Date.now();
    try {
      browser = await sharedBrowser();
    } catch (error) {
      const output = `[all.mjs] the shared browser did not start: ${error?.message ?? error}\n`;
      return { suite, ok: false, seconds: Math.round((Date.now() - started) / 1000), output, load: loadavg().map((n) => n.toFixed(2)) };
    }
    // An interrupt that came while the server started has already SIGTERMed
    // `running`; a suite spawned now would be orphaned.
    if (interrupted) {
      return { suite, ok: false, seconds: Math.round((Date.now() - started) / 1000), output: "[all.mjs] not started: interrupted\n", load: loadavg().map((n) => n.toFixed(2)) };
    }
  }
  return new Promise((resolve) => {
    const started = Date.now();
    const stem = suite.replace(/\.mjs$/, "");
    // The runner owns this distDir and nothing runs in it yet, so a lock left by
    // an earlier killed run is stale and would trip the suite's no-dev-server check.
    rmSync(join(root, ".next/e2e", stem, "dev/lock"), { force: true });
    const home = join(routingRoot, stem, "home");
    const tmuxRoot = join(routingRoot, stem, "tmux");
    mkdirSync(home, { recursive: true });
    mkdirSync(tmuxRoot, { recursive: true });
    const env = scrubbedEnv({ HOME: home, TMUX_TMPDIR: tmuxRoot, PLAYWRIGHT_BROWSERS_PATH: browserCache, PI_WEB_DIST_DIR: `.next/e2e/${stem}` });
    if (prebuiltDist) env.E2E_PREBUILT_DIST = prebuiltDist;
    // Only browser suites get the shared browser; no suite inherits the operator's.
    if (browser) env.E2E_BROWSER_WS = browser.ws;
    else delete env.E2E_BROWSER_WS;
    const child = spawn(process.execPath, argsFor(suite), {
      cwd: root,
      env,
      stdio: ["ignore", "pipe", "pipe"],
      detached: true,
    });
    running.add(child);
    if (browser) {
      onShared.set(suite, browser);
      crashNotes.set(suite, []);
    }
    let output = "";
    let timedOut = false;
    child.stdout.on("data", (d) => { output += d; });
    child.stderr.on("data", (d) => { output += d; });
    const timer = setTimeout(() => {
      timedOut = true;
      killGroup(child, "SIGTERM");
      setTimeout(() => killGroup(child, "SIGKILL"), 10_000).unref();
    }, timeoutMs);
    const finish = (code, signal) => {
      clearTimeout(timer);
      running.delete(child);
      const ok = code === 0 && !timedOut;
      if (timedOut) output += `\n[all.mjs] killed after ${timeoutMs}ms timeout\n`;
      else if (!ok && signal) output += `\n[all.mjs] terminated by ${signal}\n`;
      for (const note of crashNotes.get(suite) ?? []) output += `\n${note}\n`;
      onShared.delete(suite);
      crashNotes.delete(suite);
      const load = loadavg().map((n) => n.toFixed(2));
      resolve({ suite, ok, seconds: Math.round((Date.now() - started) / 1000), output, load });
    };
    child.once("error", (error) => { output += `\n[all.mjs] spawn error: ${error.message}\n`; finish(1, null); });
    child.once("close", finish);
  });
}

async function lane(queue, width) {
  const results = [];
  const work = async () => {
    while (queue.length && !interrupted) results.push(await runSuite(queue.shift()));
  };
  await Promise.all(Array.from({ length: Math.min(width, queue.length) }, work));
  return results;
}

const isolatedMarker = "e2e/all: isolated";
const isolated = new Set(all.filter((f) => source.get(f).includes(isolatedMarker)));
const solo = suites.filter((s) => isolated.has(s));
const regular = suites.filter((s) => !isolated.has(s));

const started = Date.now();
const startMode = serverMode === "start" && suites.some(isBrowser);
console.log(`e2e/all: ${suites.length} suite(s), browser pool ${jobs}, serial lane 1`
  + (solo.length ? `, ${solo.length} isolated` : "")
  + (startMode ? ", next start (build once)" : ""));
let prebuiltDist = null;
if (startMode) {
  const given = process.env.E2E_PREBUILT_DIST;
  if (given) {
    if (!existsSync(resolve(root, given, "BUILD_ID"))) {
      console.error(`E2E_PREBUILT_DIST=${given} holds no build (no BUILD_ID under ${resolve(root, given)})`);
      process.exit(2);
    }
    const stale = staleness(given);
    if (stale) {
      console.error(`E2E_PREBUILT_DIST=${given} is refused: ${stale}. Unset E2E_PREBUILT_DIST to build this tree once.`);
      process.exit(2);
    }
    prebuiltDist = given;
    console.log(`e2e/all: next start on the prebuilt ${given} (its stamp matches this tree), no build`);
  } else {
    const build = await buildOnce();
    if (interrupted) process.exit(130);
    if (!build.ok) {
      console.log(`\n===== FAIL next build (${build.seconds}s) =====\n${build.output}`);
      console.log(`e2e/all: next build into ${sharedBuild} failed; no suite ran`);
      process.exit(1);
    }
    prebuiltDist = sharedBuild;
    console.log(`e2e/all: next build --webpack into ${sharedBuild} in ${build.seconds}s`);
  }
}
const settled = (await Promise.all([
  lane(regular.filter(isBrowser), jobs),
  lane(regular.filter((s) => !isBrowser(s)), 1),
])).flat();
for (const suite of solo) {
  if (interrupted) break;
  settled.push(await runSuite(suite));
}
await closeSharedBrowser();
settled.sort((a, b) => a.suite.localeCompare(b.suite));

for (const r of settled) {
  console.log(`\n===== ${r.ok ? "PASS" : "FAIL"} ${r.suite} (${r.seconds}s) =====`);
  console.log(r.ok ? r.output.split("\n").filter((l) => /^(PASS|FAIL|SKIP)/.test(l)).join("\n") : r.output);
}
// one line per failing scenario marker (`FAIL: <id>` with an
// indented message below it, or `FAIL <id>: <message>` on one line), else the
// runner's own trailer or the last output line, then the host load when the
// suite settled and the suite's re-run. A suite's tally of red scenarios
// (`FAIL <suite>: 2 scenario(s) failed`) is not a scenario.
const tally = /\b\d+\b.*\bscenario\(s\)/;
const clip = (line) => line.replace(/\s+/g, " ").trim().slice(0, 200);
function failureLines(r) {
  const lines = r.output.split("\n");
  const found = [];
  lines.forEach((line, i) => {
    const m = /^FAIL(?::\s*|\s+)(.+)$/.exec(line);
    if (!m || tally.test(m[1])) return;
    const colon = line.startsWith("FAIL:") ? -1 : m[1].indexOf(": ");
    const scenario = clip(colon < 0 ? m[1] : m[1].slice(0, colon));
    let message = colon < 0 ? "" : clip(m[1].slice(colon + 2));
    for (let j = i + 1; !message && j < lines.length && /^(\s|$)/.test(lines[j]); j++) message = clip(lines[j]);
    found.push(`FAIL e2e/${r.suite}: ${scenario}${message ? ` — ${message}` : ""}`);
  });
  if (!found.length) {
    const nonEmpty = lines.map(clip).filter(Boolean);
    const last = nonEmpty.findLast((l) => l.startsWith("[all.mjs] ")) ?? nonEmpty.at(-1) ?? "no output";
    found.push(`FAIL e2e/${r.suite}: no scenario marker — ${last}`);
  }
  return [...found, `host loadavg 1m/5m/15m: ${r.load.join(" / ")}`, `re-run: npm run test:e2e:all -- ${r.suite}`];
}
if (settled.some((r) => !r.ok)) {
  console.log("\n===== failures =====");
  for (const r of settled) if (!r.ok) console.log(failureLines(r).join("\n"));
}
if (crashes.length) {
  console.log(`\ne2e/all: the shared browser crashed ${crashes.length} time(s): `
    + crashes.map((c) => `at ${c.at} ${c.why} while ${c.affected.length ? c.affected.map((s) => `e2e/${s}`).join(", ") : "no suite"} ran`).join("; ")
    + "; a browser suite started after a crash gets a new one");
}
console.log("\n===== summary =====");
for (const r of settled) console.log(`${r.ok ? "PASS" : "FAIL"} ${r.suite} ${r.seconds}s`);
const failed = settled.filter((r) => !r.ok);
console.log(`${settled.length - failed.length}/${settled.length} passed in ${Math.round((Date.now() - started) / 1000)}s`);
// every suite has settled; drop the passing suites' dist dirs,
// keep the failed ones warm for their re-run. An interrupted run removes nothing.
if (!interrupted) {
  const passed = settled.filter((r) => r.ok);
  for (const r of passed) rmSync(join(root, ".next/e2e", r.suite.replace(/\.mjs$/, "")), { recursive: true, force: true });
  console.log(`e2e/all: removed ${passed.length} passing suite dist dir(s) under .next/e2e`
    + (failed.length ? `, kept ${failed.length} failed for re-run` : ""));
}
process.exit(failed.length || interrupted ? 1 : 0);
