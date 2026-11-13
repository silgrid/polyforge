#!/usr/bin/env node
// tmp-guard: run a command with a private temp dir and fail if anything is
// left in it afterwards. `npm test` runs `node --test` through it.
//
//   node scripts/tmp-guard.mjs <command> [args...]
//
// The command and every process it starts (node --test runs each test file
// in a child process) inherit TMPDIR/TEMP/TMP, so os.tmpdir() and mkdtemp()
// land in the private dir: whatever is left there is this run's leak, even
// while other processes share the machine's temp dir. Tests remove what
// they make (t.after, after).
//
// Two shared caches keep their usual place in the real temp dir, as before:
// node's compile cache through NODE_COMPILE_CACHE, and jiti's, whose path
// has no environment override, through a link pre-made in the private dir —
// the one entry the check expects.
import { spawnSync } from "node:child_process";
import { existsSync, lstatSync, mkdirSync, mkdtempSync, readdirSync, rmSync, symlinkSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { isolatedTestEnv } from "./test-env.mjs";

const [command, ...args] = process.argv.slice(2);
if (!command) {
  console.error("usage: node scripts/tmp-guard.mjs <command> [args...]");
  process.exit(2);
}

const realTmp = tmpdir();
const privateTmp = mkdtempSync(join(realTmp, "pi-web-test-"));
// Tests must not inherit the runner's PI_WEB_*/PF_* settings, pf config or
// tmux server: the child gets the isolated test environment (scripts/test-env.mjs),
// with a HOME of its own inside the private temp dir.
const routing = join(privateTmp, ".routing");
const home = join(routing, "home");
const tmuxRoot = join(routing, "tmux");
mkdirSync(home, { recursive: true });
mkdirSync(tmuxRoot);
const childEnv = {
  ...isolatedTestEnv(process.env, home), TMPDIR: privateTmp, TEMP: privateTmp, TMP: privateTmp,
  TMUX_TMPDIR: tmuxRoot,
  PLAYWRIGHT_BROWSERS_PATH: process.env.PLAYWRIGHT_BROWSERS_PATH ?? (process.platform === "darwin"
    ? join(homedir(), "Library", "Caches", "ms-playwright")
    : join(process.env.XDG_CACHE_HOME || join(homedir(), ".cache"), "ms-playwright")),
  NODE_COMPILE_CACHE: process.env.NODE_COMPILE_CACHE || join(realTmp, "node-compile-cache"),
};
const jitiCache = join(realTmp, "jiti");
mkdirSync(jitiCache, { recursive: true });
symlinkSync(jitiCache, join(privateTmp, "jiti"), "junction");

// `node` is this node: no PATH lookup, no shell on any platform
const result = spawnSync(command === "node" ? process.execPath : command, args, {
  stdio: "inherit",
  env: childEnv,
});

let code = result.status ?? 1;
if (result.error) {
  console.error(`tmp-guard: cannot run ${command}: ${result.error.message}`);
}

// Only this run's socket may be cleaned up, even when the caller is in tmux.
const socket = join(tmuxRoot, `tmux-${process.getuid?.() ?? 0}`, "default");
if (existsSync(socket)) spawnSync("tmux", ["-S", socket, "kill-server"], { env: childEnv, stdio: "ignore" });
const left = readdirSync(privateTmp)
  .filter((name) => !(name === "jiti" && lstatSync(join(privateTmp, name)).isSymbolicLink())
    && !(name === ".routing" && lstatSync(join(privateTmp, name)).isDirectory()))
  .sort();
if (left.length > 0) {
  const shown = left.slice(0, 20).concat(left.length > 20 ? [`... and ${left.length - 20} more`] : []);
  console.error(
    `tmp-guard: FAIL: ${left.length} temp entr${left.length === 1 ? "y was" : "ies were"} left in the private temp dir ${privateTmp}` +
      ` — a test (or a process it started) made them and never removed them; remove them in t.after():\n  ${shown.join("\n  ")}`,
  );
  code ||= 1;
}
// fs.rm does not follow the jiti link: the shared cache stays
rmSync(privateTmp, { recursive: true, force: true });
process.exit(code);
