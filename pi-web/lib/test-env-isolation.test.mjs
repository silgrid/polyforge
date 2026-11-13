// carries: scripts/tmp-guard.mjs, scripts/test-env.mjs
// `npm test` runs every test file through scripts/tmp-guard.mjs. Whatever the
// runner's environment holds (an idle timeout, a pf binary, task markers, its
// own pf config under HOME or XDG_CONFIG_HOME), the test process must see none
// of it: no PI_WEB_*/PF_* key, a HOME inside the private temp dir, and an XDG
// config dir under that HOME.
import "#test-env";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const RUNNER_SENTINEL = "runner-sentinel = true\n";
const PROBE_SENTINEL = "probe-sentinel = true\n";

// Runs inside the guarded test process: report what it sees, and resolve pf's
// config path the way pf does ($XDG_CONFIG_HOME, else ~/.config).
const PROBE = `
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const read = (file) => { try { return fs.readFileSync(file, "utf8"); } catch (error) { return error.code; } };
const configDir = process.env.XDG_CONFIG_HOME || path.join(os.homedir(), ".config");
const file = path.join(configDir, "polyforge", "cli.toml");
const homeFile = path.join(os.homedir(), ".config", "polyforge", "cli.toml");
const before = [read(file), read(homeFile)];
fs.mkdirSync(path.dirname(file), { recursive: true });
fs.writeFileSync(file, ${JSON.stringify(PROBE_SENTINEL)});
const resolved = read(file);
fs.rmSync(path.join(configDir, "polyforge"), { recursive: true, force: true });
process.stdout.write(JSON.stringify({
  keys: Object.keys(process.env).filter((key) => /^(PI_WEB_|PF_)/.test(key)).sort(),
  home: os.homedir(),
  xdgConfigHome: process.env.XDG_CONFIG_HOME,
  xdgStateHome: process.env.XDG_STATE_HOME,
  tmp: os.tmpdir(),
  before,
  resolved,
}));
`;

test("tmp-guard: the test process sees no PI_WEB_*/PF_* and a temp HOME and XDG config dir, never the runner's pf config", (t) => {
  const scratch = mkdtempSync(path.join(tmpdir(), "test-env-isolation-"));
  t.after(() => rmSync(scratch, { recursive: true, force: true }));
  const runnerHome = path.join(scratch, "home");
  mkdirSync(path.join(runnerHome, ".config", "polyforge"), { recursive: true });
  writeFileSync(path.join(runnerHome, ".config", "polyforge", "cli.toml"), RUNNER_SENTINEL);

  const run = spawnSync(process.execPath, [path.join(root, "scripts", "tmp-guard.mjs"), "node", "-e", PROBE], {
    cwd: root,
    encoding: "utf8",
    env: {
      ...process.env,
      HOME: runnerHome,
      XDG_CONFIG_HOME: path.join(runnerHome, ".config"),
      XDG_STATE_HOME: path.join(runnerHome, ".state"),
      PI_WEB_IDLE_TIMEOUT_MS: "0",
      PF_BIN: path.join(scratch, "pf"),
      PF_WI_ID: "x",
      PF_ANY_NEW: "y",
    },
  });
  assert.ok(run.stdout, `the probe printed nothing; stderr:\n${run.stderr}`);
  const seen = JSON.parse(run.stdout);

  assert.deepEqual(seen.keys, []);
  assert.ok(seen.home.startsWith(seen.tmp + path.sep), `HOME ${seen.home} is not inside the private temp dir ${seen.tmp}`);
  assert.notEqual(seen.home, runnerHome);
  assert.equal(seen.xdgConfigHome, path.join(seen.home, ".config"));
  assert.notEqual(seen.xdgStateHome, path.join(runnerHome, ".state"));
  assert.ok(!seen.before.includes(RUNNER_SENTINEL), "the runner's pf config was reachable");
  assert.equal(seen.resolved, PROBE_SENTINEL);
  assert.equal(run.status, 0, run.stderr);
});
