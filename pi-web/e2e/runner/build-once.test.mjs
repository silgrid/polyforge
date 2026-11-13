// build-once mode of the parallel e2e runner.
//
// With E2E_SERVER_MODE=start e2e/all.mjs runs one `next build` into
// `.next/e2e-build` before the lanes start and hands it to every suite as
// E2E_PREBUILT_DIST; e2e/next-server.mjs then starts each suite's server with
// `next start` on a private copy of that build in the suite's own
// `.next/e2e/<stem>`, for every caller that does not pass `mode` itself.
//
// The test copies the real runner and the real helper into a throwaway root
// (the lib/e2e-all-*.test.mjs pattern) with a stub `next` (build writes a
// BUILD_ID and a webpack cache; dev/start serve /api/sessions and report what
// they were started with) and a stub `playwright` for the runner's preflight
// and shared browser.
// Fixture suites call startNextServer as the suites do, without a mode.
import "#test-env";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const read = (rel) => readFileSync(new URL(rel, import.meta.url), "utf8");
const runnerSource = read("../all.mjs");
const jobsSource = read("./jobs.mjs");
const helperSource = read("../next-server.mjs");

// `next build` records each build (dist dir and HOME) and fails on demand;
// `next dev|start` answer /api/sessions with their mode, dist dir and what that
// dir holds while they run.
const nextStub = `import { appendFileSync, existsSync, mkdirSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { join } from "node:path";
const [command, ...rest] = process.argv.slice(2);
const dist = process.env.PI_WEB_DIST_DIR ?? null;
if (command === "build") {
  appendFileSync("builds.jsonl", JSON.stringify({ args: rest, dist, home: process.env.HOME }) + "\\n");
  if (process.env.FIXTURE_BUILD_FAIL) { console.error("Type error: fixture build failure"); process.exit(1); }
  if (process.env.FIXTURE_BUILD_NO_ID) process.exit(0);
  const out = dist ?? ".next";
  mkdirSync(join(out, "cache", "webpack"), { recursive: true });
  mkdirSync(join(out, "server"), { recursive: true });
  writeFileSync(join(out, "cache", "webpack", "pack"), "cache");
  writeFileSync(join(out, "server", "page.js"), "page");
  writeFileSync(join(out, "BUILD_ID"), "fixture-build");
  process.exit(0);
}
const port = Number(rest[rest.indexOf("-p") + 1]);
const served = dist ?? ".next";
createServer((req, res) => {
  res.setHeader("content-type", "application/json");
  res.end(JSON.stringify({
    command, dist,
    hasBuild: existsSync(join(served, "BUILD_ID")),
    hasPage: existsSync(join(served, "server", "page.js")),
    hasCache: existsSync(join(served, "cache")),
  }));
}).listen(port, "127.0.0.1");
`;

// The runner's preflight checks executablePath; its shared browser
// is a launchServer whose process never exits here (the
// fixture suites do not connect to it), so no real process is started.
const playwrightStub = `import { EventEmitter } from "node:events";
export const chromium = {
  executablePath: () => process.execPath,
  launchServer: async () => {
    const child = new EventEmitter();
    return { wsEndpoint: () => "ws://127.0.0.1:9/fixture", process: () => child, close: async () => {} };
  },
};
`;

// A browser suite for the runner (it names chromium${".launch"}) that starts its
// server through the shared helper without passing a mode.
const suite = (stem) => `// a browser suite: chromium${".launch"}
import { appendFileSync } from "node:fs";
import { startNextServer } from "./next-server.mjs";
const started = await startNextServer({ name: ${JSON.stringify(stem)}, logFile: ${JSON.stringify(`${stem}.server.log`)} });
const seen = await (await fetch(started.base + "/api/sessions")).json();
appendFileSync("seen.jsonl", JSON.stringify({ suite: ${JSON.stringify(stem)}, envDist: process.env.PI_WEB_DIST_DIR ?? null, prebuilt: process.env.E2E_PREBUILT_DIST ?? null, ...seen }) + "\\n");
await started.stop();
console.log("PASS: SC1");
`;

function fixtureRoot() {
  const work = mkdtempSync(join(tmpdir(), "e2e-build-once-"));
  mkdirSync(join(work, "e2e/runner"), { recursive: true });
  mkdirSync(join(work, "node_modules/next/dist/bin"), { recursive: true });
  mkdirSync(join(work, "node_modules/playwright"), { recursive: true });
  writeFileSync(join(work, "package.json"), JSON.stringify({ scripts: {} }));
  writeFileSync(join(work, "e2e/all.mjs"), runnerSource);
  writeFileSync(join(work, "e2e/runner/jobs.mjs"), jobsSource);
  writeFileSync(join(work, "e2e/next-server.mjs"), helperSource);
  writeFileSync(join(work, "node_modules/next/dist/bin/next"), nextStub);
  writeFileSync(join(work, "node_modules/next/package.json"), JSON.stringify({ name: "next", type: "module" }));
  writeFileSync(join(work, "node_modules/playwright/package.json"), JSON.stringify({ name: "playwright", type: "module", main: "index.js" }));
  writeFileSync(join(work, "node_modules/playwright/index.js"), playwrightStub);
  writeFileSync(join(work, "e2e/alpha.mjs"), suite("alpha"));
  writeFileSync(join(work, "e2e/beta.mjs"), suite("beta"));
  return work;
}

// The runner's environment: this process's, without an inherited mode or build.
function runAll(work, own, args = []) {
  const env = { ...process.env, E2E_JOBS: "2" };
  delete env.E2E_SERVER_MODE;
  delete env.E2E_PREBUILT_DIST;
  const run = spawnSync(process.execPath, ["e2e/all.mjs", ...args], {
    cwd: work,
    encoding: "utf8",
    timeout: 50_000,
    env: { ...env, ...own },
  });
  return { status: run.status, output: `${run.stdout}${run.stderr}` };
}
const lines = (work, file) => existsSync(join(work, file))
  ? readFileSync(join(work, file), "utf8").trim().split("\n").filter(Boolean).map((l) => JSON.parse(l))
  : [];
const bySuite = (seen) => Object.fromEntries(seen.map((s) => [s.suite, s]));

test("start mode builds once and each suite's next start serves a private copy", { timeout: 60_000 }, () => {
  const work = fixtureRoot();
  try {
    const { status, output } = runAll(work, { E2E_SERVER_MODE: "start" });
    assert.equal(status, 0, output);
    assert.match(output, /^e2e\/all: 2 suite\(s\), browser pool 2, serial lane 1, next start \(build once\)$/m);
    const builds = lines(work, "builds.jsonl");
    assert.equal(builds.length, 1, `one build for two suites:\n${output}`);
    assert.deepEqual(builds[0].args, ["--webpack"], "the build is the package's `npm run build`");
    assert.equal(builds[0].dist, ".next/e2e-build");
    assert.notEqual(builds[0].home, process.env.HOME, "the build runs with a throwaway HOME, not the operator's");
    const seen = bySuite(lines(work, "seen.jsonl"));
    for (const stem of ["alpha", "beta"]) {
      const s = seen[stem];
      assert.ok(s, `${stem} never reached its server:\n${output}`);
      assert.equal(s.command, "start", `${stem} ran next ${s.command}, not next start`);
      assert.equal(s.prebuilt, ".next/e2e-build");
      assert.equal(s.envDist, `.next/e2e/${stem}`, "the suite keeps its own dist dir");
      assert.equal(s.dist, `.next/e2e/${stem}`, "next start serves the suite's own dir, not the shared build");
      assert.equal(s.hasBuild && s.hasPage, true, `${stem}'s dir holds the build while its server runs`);
      assert.equal(s.hasCache, false, "the private copy leaves out webpack's cache");
    }
    // The per-suite cleanup removes the passing suites' copies, never the shared build.
    assert.equal(existsSync(join(work, ".next/e2e/alpha")), false, output);
    assert.equal(existsSync(join(work, ".next/e2e/beta")), false, output);
    assert.ok(existsSync(join(work, ".next/e2e-build/BUILD_ID")), `the shared build was removed:\n${output}`);
    assert.ok(existsSync(join(work, ".next/e2e-build/cache/webpack/pack")), "the shared build's cache stays for a warm rebuild");
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

// No suite opts out: three suites used to pin `mode: "dev"` and so never ran
// on `next start`. The helper takes no mode; a stale argument changes nothing.
test("a suite passing its own mode still follows E2E_SERVER_MODE", { timeout: 60_000 }, () => {
  const work = fixtureRoot();
  try {
    writeFileSync(join(work, "e2e/alpha.mjs"), suite("alpha").replace("startNextServer({ name:", 'startNextServer({ mode: "dev", name:'));
    assert.match(readFileSync(join(work, "e2e/alpha.mjs"), "utf8"), /mode: "dev"/);
    const { status, output } = runAll(work, { E2E_SERVER_MODE: "start" }, ["alpha.mjs"]);
    assert.equal(status, 0, output);
    assert.equal(bySuite(lines(work, "seen.jsonl")).alpha.command, "start", `a pinned mode overrode the run's:\n${output}`);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

// The staleness stamp needs a git tree: the fixture root becomes a repository
// whose runtime output is ignored, as the package's is.
function gitFixture() {
  const work = fixtureRoot();
  writeFileSync(join(work, ".gitignore"), "/.next/\n/node_modules/\n*.jsonl\n*.server.log\n");
  const git = (...args) => {
    const r = spawnSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@example.invalid", ...args], { cwd: work, encoding: "utf8" });
    assert.equal(r.status, 0, `git ${args.join(" ")}: ${r.stderr}`);
  };
  git("init", "-q");
  git("add", "-A");
  git("commit", "-qm", "fixture");
  return { work, git };
}

test("a stamped build of this tree is reused; a stale or unstamped one is refused", { timeout: 90_000 }, () => {
  const { work, git } = gitFixture();
  try {
    const first = runAll(work, { E2E_SERVER_MODE: "start" }, ["alpha.mjs"]);
    assert.equal(first.status, 0, first.output);
    const stamp = JSON.parse(readFileSync(join(work, ".next/e2e-build/e2e-build-stamp.json"), "utf8"));
    assert.match(stamp.tree, /^[0-9a-f]{40,64}$/, `the stamp names the package tree: ${JSON.stringify(stamp)}`);
    assert.equal(stamp.dirty, false);

    const reuse = runAll(work, { E2E_SERVER_MODE: "start", E2E_PREBUILT_DIST: ".next/e2e-build" }, ["beta.mjs"]);
    assert.equal(reuse.status, 0, reuse.output);
    assert.match(reuse.output, /next start on the prebuilt \.next\/e2e-build \(its stamp matches this tree\), no build/);
    assert.equal(lines(work, "builds.jsonl").length, 1, `a matching prebuilt dir must not be rebuilt:\n${reuse.output}`);
    assert.equal(bySuite(lines(work, "seen.jsonl")).beta.command, "start");

    // An uncommitted source change makes the build stale ...
    mkdirSync(join(work, "lib"));
    writeFileSync(join(work, "lib/new.ts"), "export const changed = 1;\n");
    const dirty = runAll(work, { E2E_SERVER_MODE: "start", E2E_PREBUILT_DIST: ".next/e2e-build" }, ["beta.mjs"]);
    assert.equal(dirty.status, 2, dirty.output);
    assert.match(dirty.output, /E2E_PREBUILT_DIST=\.next\/e2e-build is refused: uncommitted changes were none \(now dirty\)/);
    // ... and so does committing it (another tree), and a changed lockfile.
    git("add", "-A");
    git("commit", "-qm", "change");
    writeFileSync(join(work, "package-lock.json"), "{}\n");
    git("add", "-A");
    git("commit", "-qm", "lock");
    const moved = runAll(work, { E2E_SERVER_MODE: "start", E2E_PREBUILT_DIST: ".next/e2e-build" }, ["beta.mjs"]);
    assert.equal(moved.status, 2, moved.output);
    assert.match(moved.output, /refused: package tree [0-9a-f]{12} \(HEAD [0-9a-f]{12}\) is now [0-9a-f]{12}/);
    assert.match(moved.output, /package-lock\.json changed/);
    assert.equal(lines(work, "builds.jsonl").length, 1, "a refused prebuilt dir starts no build");

    // A build the runner did not make carries no stamp.
    mkdirSync(join(work, ".next/ci-build/server"), { recursive: true });
    writeFileSync(join(work, ".next/ci-build/BUILD_ID"), "prebuilt");
    const unstamped = runAll(work, { E2E_SERVER_MODE: "start", E2E_PREBUILT_DIST: ".next/ci-build" }, ["beta.mjs"]);
    assert.equal(unstamped.status, 2, unstamped.output);
    assert.match(unstamped.output, /refused: it has no e2e-build-stamp\.json \(not built by e2e\/all\.mjs\)/);
    assert.deepEqual(Object.keys(bySuite(lines(work, "seen.jsonl"))).sort(), ["alpha", "beta"], "no refused run reached a suite");
    assert.equal(lines(work, "seen.jsonl").length, 2);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

test("E2E_PREBUILT_DIST without a build is refused before any suite runs", { timeout: 60_000 }, () => {
  const work = fixtureRoot();
  try {
    const { status, output } = runAll(work, { E2E_SERVER_MODE: "start", E2E_PREBUILT_DIST: ".next/nothing" });
    assert.equal(status, 2, output);
    assert.match(output, /E2E_PREBUILT_DIST=\.next\/nothing holds no build/);
    assert.deepEqual(lines(work, "seen.jsonl"), []);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

test("a failing build fails the run and starts no suite", { timeout: 60_000 }, () => {
  const work = fixtureRoot();
  try {
    const { status, output } = runAll(work, { E2E_SERVER_MODE: "start", FIXTURE_BUILD_FAIL: "1" });
    assert.equal(status, 1, output);
    assert.match(output, /^===== FAIL next build \(\d+s\) =====$/m);
    assert.match(output, /Type error: fixture build failure/, "the build's own output is printed");
    assert.match(output, /next build into \.next\/e2e-build failed; no suite ran/);
    assert.deepEqual(lines(work, "seen.jsonl"), [], `a suite ran without a build:\n${output}`);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

test("a build that exits 0 but leaves no BUILD_ID fails the run", { timeout: 60_000 }, () => {
  const work = fixtureRoot();
  try {
    const { status, output } = runAll(work, { E2E_SERVER_MODE: "start", FIXTURE_BUILD_NO_ID: "1" });
    assert.equal(status, 1, output);
    assert.match(output, /^===== FAIL next build \(\d+s\) =====$/m);
    assert.match(output, /next build exited 0 but left no \.next\/e2e-build\/BUILD_ID/);
    assert.deepEqual(lines(work, "seen.jsonl"), [], `a suite ran without a build:\n${output}`);
    assert.equal(existsSync(join(work, ".next/e2e-build/e2e-build-stamp.json")), false, "no stamp for a failed build");
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

test("dev mode is unchanged: no build, next dev in each suite's own dir", { timeout: 60_000 }, () => {
  const work = fixtureRoot();
  try {
    const { status, output } = runAll(work, {});
    assert.equal(status, 0, output);
    assert.match(output, /^e2e\/all: 2 suite\(s\), browser pool 2, serial lane 1$/m);
    assert.equal(lines(work, "builds.jsonl").length, 0, output);
    const seen = bySuite(lines(work, "seen.jsonl"));
    for (const stem of ["alpha", "beta"]) {
      assert.equal(seen[stem].command, "dev");
      assert.equal(seen[stem].dist, `.next/e2e/${stem}`);
      assert.equal(seen[stem].prebuilt, null);
    }
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

test("an unknown E2E_SERVER_MODE is refused", { timeout: 60_000 }, () => {
  const work = fixtureRoot();
  try {
    const { status, output } = runAll(work, { E2E_SERVER_MODE: "prod" });
    assert.equal(status, 2, output);
    assert.match(output, /E2E_SERVER_MODE must be dev or start, got "prod"/);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

// CI's path (pi-web-ci.yml): `npm run build` into `.next`, then a suite run
// directly with E2E_SERVER_MODE=start and no PI_WEB_DIST_DIR. The helper serves
// `.next` in place and leaves the child's PI_WEB_DIST_DIR unset, as before.
test("a suite run directly in start mode serves .next in place (CI)", { timeout: 60_000 }, () => {
  const work = fixtureRoot();
  try {
    const env = { ...process.env, E2E_SERVER_MODE: "start" };
    delete env.E2E_PREBUILT_DIST;
    delete env.PI_WEB_DIST_DIR;
    const direct = () => spawnSync(process.execPath, ["e2e/alpha.mjs"], { cwd: work, encoding: "utf8", timeout: 30_000, env });
    const missing = direct();
    assert.notEqual(missing.status, 0);
    assert.match(`${missing.stdout}${missing.stderr}`, /next start needs a production build in \.next \(no BUILD_ID\)/);

    mkdirSync(join(work, ".next/server"), { recursive: true });
    writeFileSync(join(work, ".next/BUILD_ID"), "ci");
    writeFileSync(join(work, ".next/server/page.js"), "page");
    const run = direct();
    assert.equal(run.status, 0, `${run.stdout}${run.stderr}`);
    const { alpha } = bySuite(lines(work, "seen.jsonl"));
    assert.equal(alpha.command, "start");
    assert.equal(alpha.dist, null, "the child's PI_WEB_DIST_DIR stays unset");
    assert.equal(alpha.hasPage, true);
    assert.ok(existsSync(join(work, ".next/BUILD_ID")), ".next is served in place, never replaced");
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});
