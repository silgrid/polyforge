// carries: package.json, scripts/test-env-setup.mjs, *.test.mjs
// A bare `node --test <file>` runs without tmp-guard: the test file's first
// import, `import "#test-env";`, is what isolates it from the runner. Every
// test file `npm test` runs must open with that import, and a direct run of
// test files that call pf must neither reach a PF_BIN or PATH pf nor change
// its results when the runner sets PF_BIN/PI_WEB_*.
import "#test-env";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, existsSync, globSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const SETUP_IMPORT = 'import "#test-env";';
const RUNNER_SENTINEL = "runner-sentinel = true\n";
const FILES = [
  "lib/polyforge-detect.test.mjs",
  "lib/polyforge-config-write.test.mjs",
  "lib/polyforge-messages.test.mjs",
  "lib/rpc-manager-idle-timeout.test.mjs",
];

/** The first line after leading blank, `//` and `/* … *\/` lines. */
function firstStatement(source) {
  let inBlock = false;
  for (const raw of source.split("\n")) {
    const line = raw.trim();
    if (inBlock) {
      if (line.includes("*/")) inBlock = false;
      continue;
    }
    if (line === "" || line.startsWith("//")) continue;
    if (line.startsWith("/*")) {
      inBlock = !line.includes("*/", 2);
      continue;
    }
    return line;
  }
  return undefined;
}

/** Every file below `dir`, relative to it. */
function walk(dir, prefix = "") {
  return readdirSync(dir).sort().flatMap((name) => {
    const rel = prefix ? `${prefix}/${name}` : name;
    return statSync(path.join(dir, name)).isDirectory() ? walk(path.join(dir, name), rel) : [rel];
  });
}

/**
 * The child's TAP summary. The reporter is pinned because node's default
 * differs by version (22 picks tap off a TTY, 24 picks spec), and spec is
 * colored when the runner hands this file FORCE_COLOR=1 on a TTY.
 */
function summary(stdout) {
  const count = (name) => Number(stdout.match(new RegExp(`^# ${name} (\\d+)$`, "m"))?.[1]);
  return { tests: count("tests"), pass: count("pass"), fail: count("fail") };
}

test("direct node --test: every test file imports #test-env first, and PF_BIN/PI_WEB_* never change a run or reach the runner's pf", async (t) => {
  await t.test("every npm test file's first statement is the setup import", () => {
    const script = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8")).scripts.test;
    const globs = [...script.matchAll(/"([^"]+\.test\.mjs)"/g)].map((match) => match[1]);
    assert.ok(globs.length > 0, `no test globs in: ${script}`);
    const files = [...new Set(globs.flatMap((glob) => globSync(glob, { cwd: root })))].sort();
    assert.ok(files.length > 0, `the globs match nothing: ${globs.join(" ")}`);
    const missing = files.filter((file) => firstStatement(readFileSync(path.join(root, file), "utf8")) !== SETUP_IMPORT);
    assert.deepEqual(missing, []);
  });

  await t.test("a direct run with PF_BIN/PI_WEB_* set never calls pf and matches the run without them", (t) => {
    const scratch = mkdtempSync(path.join(tmpdir(), "test-env-direct-run-"));
    t.after(() => rmSync(scratch, { recursive: true, force: true }));
    const home = path.join(scratch, "home");
    const tmp = path.join(scratch, "tmp");
    const fake = path.join(scratch, "fake");
    const calls = path.join(scratch, "calls.log");
    mkdirSync(path.join(home, ".config", "polyforge"), { recursive: true });
    writeFileSync(path.join(home, ".config", "polyforge", "cli.toml"), RUNNER_SENTINEL);
    mkdirSync(tmp);
    mkdirSync(fake);
    writeFileSync(path.join(fake, "pf"), `#!/bin/sh\necho "$*" >> ${JSON.stringify(calls)}\nexit 1\n`);
    chmodSync(path.join(fake, "pf"), 0o755);
    const base = {
      ...process.env,
      HOME: home, TMPDIR: tmp, TEMP: tmp, TMP: tmp,
      PATH: `${fake}${path.delimiter}${process.env.PATH ?? ""}`,
      // keep node's compile cache, if any, out of `tmp`
      NODE_COMPILE_CACHE: path.join(scratch, "compile-cache"),
    };
    // this file runs as a node --test child: the spawned runner must not think it is one too
    delete base.NODE_TEST_CONTEXT;
    const run = (env) => spawnSync(process.execPath, ["--experimental-strip-types", "--test", "--test-reporter=tap", ...FILES], { cwd: root, encoding: "utf8", env });

    const a = run({ ...base, PF_BIN: path.join(fake, "pf"), PI_WEB_IDLE_TIMEOUT_MS: "0", PF_WI_ID: "x" });
    const b = run(base);

    const logged = existsSync(calls) ? readFileSync(calls, "utf8") : "";
    assert.equal(logged, "", "the runner's pf was called");
    const sa = summary(a.stdout);
    assert.deepEqual(sa, summary(b.stdout), a.stdout.slice(-4000));
    assert.equal(sa.fail, 0, a.stdout.slice(-4000));
    assert.equal(a.status, 0, a.stdout.slice(-4000) + a.stderr);
    assert.deepEqual(walk(home), [".config/polyforge/cli.toml"]);
    assert.deepEqual(readdirSync(tmp).filter((name) => name !== "jiti"), []);
  });
});
