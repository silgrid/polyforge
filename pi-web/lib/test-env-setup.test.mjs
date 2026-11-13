// carries: scripts/test-env-setup.mjs, scripts/test-env.mjs
// A bare `node --test <file>` runs without tmp-guard, so the only thing that can
// isolate it from the runner is what the test file imports first. A probe test
// file whose first import is the real setup module must see no PI_WEB_*/PF_*
// key and a temp HOME (removed at exit), never the runner's pf config — run
// directly, and the same under tmp-guard.
import "#test-env";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const RUNNER_SENTINEL = "runner-sentinel = true\n";

const probeSource = (setupUrl) => `import ${JSON.stringify(setupUrl)};
import { readFileSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const read = (file) => { try { return readFileSync(file, "utf8"); } catch (error) { return error.code; } };
test("probe", () => {
  writeFileSync(process.env.TEST_ENV_PROBE_OUT, JSON.stringify({
    keys: Object.keys(process.env).filter((key) => /^(PI_WEB_|PF_)/.test(key)).sort(),
    home: homedir(),
    xdgConfigHome: process.env.XDG_CONFIG_HOME,
    tmp: tmpdir(),
    config: read(join(process.env.XDG_CONFIG_HOME ?? join(homedir(), ".config"), "polyforge", "cli.toml")),
    homeConfig: read(join(homedir(), ".config", "polyforge", "cli.toml")),
  }));
});
`;

test("test-env setup: a test file importing it first sees no PI_WEB_*/PF_* and a temp HOME it removes at exit, run directly or under tmp-guard", (t) => {
  const scratch = mkdtempSync(path.join(tmpdir(), "test-env-setup-"));
  t.after(() => rmSync(scratch, { recursive: true, force: true }));
  const home = path.join(scratch, "home");
  const tmp = path.join(scratch, "tmp");
  const probe = path.join(scratch, "probe", "probe.test.mjs");
  const out = path.join(scratch, "out.json");
  mkdirSync(path.join(home, ".config", "polyforge"), { recursive: true });
  writeFileSync(path.join(home, ".config", "polyforge", "cli.toml"), RUNNER_SENTINEL);
  mkdirSync(tmp);
  mkdirSync(path.dirname(probe));
  writeFileSync(probe, probeSource(pathToFileURL(path.join(root, "scripts", "test-env-setup.mjs")).href));
  const env = {
    ...process.env,
    HOME: home, TMPDIR: tmp, TEMP: tmp, TMP: tmp,
    XDG_CONFIG_HOME: path.join(home, ".config"),
    XDG_STATE_HOME: path.join(home, ".state"),
    PF_BIN: path.join(scratch, "pf"),
    PI_WEB_IDLE_TIMEOUT_MS: "0",
    PF_WI_ID: "x",
    PF_ANY_NEW: "y",
    TEST_ENV_PROBE_OUT: out,
    // tmp-guard defaults node's compile cache into the real temp dir, here `tmp`: keep it out
    NODE_COMPILE_CACHE: path.join(scratch, "compile-cache"),
  };
  // this file runs as a node --test child: the probe's runner must not think it is one too
  delete env.NODE_TEST_CONTEXT;
  const seen = () => {
    const value = JSON.parse(readFileSync(out, "utf8"));
    rmSync(out);
    return value;
  };

  // (a) directly: nothing but the probe's first import isolates it
  const direct = spawnSync(process.execPath, ["--experimental-strip-types", "--test", probe], { cwd: root, encoding: "utf8", env });
  assert.equal(direct.status, 0, direct.stdout + direct.stderr);
  const a = seen();
  assert.deepEqual(a.keys, []);
  assert.ok(a.home.startsWith(tmp + path.sep), `HOME ${a.home} is not under ${tmp}`);
  assert.notEqual(a.home, home);
  assert.equal(a.xdgConfigHome, path.join(a.home, ".config"));
  assert.equal(a.config, "ENOENT");
  assert.equal(a.homeConfig, "ENOENT");
  assert.deepEqual(readdirSync(tmp), [], "the temp HOME was not removed at exit");

  // (b) under tmp-guard: its leak check passes, so the setup's HOME is removed too
  const guarded = spawnSync(process.execPath, [path.join(root, "scripts", "tmp-guard.mjs"), "node", "--experimental-strip-types", "--test", probe], { cwd: root, encoding: "utf8", env });
  assert.equal(guarded.status, 0, guarded.stdout + guarded.stderr);
  const b = seen();
  assert.deepEqual(b.keys, []);
  assert.ok(b.tmp.startsWith(tmp + path.sep), `tmp-guard's private dir ${b.tmp} is not under ${tmp}`);
  assert.ok(b.home.startsWith(b.tmp + path.sep), `HOME ${b.home} is not inside tmp-guard's private dir ${b.tmp}`);
  assert.deepEqual(readdirSync(tmp), ["jiti"]);
});
