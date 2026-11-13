import "#test-env";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { after, test } from "node:test";
import { createJiti } from "jiti";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { spawnSync } from "node:child_process";

const jiti = createJiti(import.meta.url);
const {
  POLYFORGE_MINIMUM_PF_VERSION,
  claudeSessionStartHookPresent,
  claudeSkillsSymlinkExists,
  conventionalPolyforgeRoot,
  detectPolyforge,
  isPolyforgePackageEnabled,
  isPolyforgeVersionBelowMinimum,
  polyforgeLineFromOrigin,
  polyforgeOriginOwnerRepo,
  polyforgePackageRoot,
} = await jiti.import("./polyforge-detect.ts");

// ---- pure helpers ----

test("minimum-version hint: semver-ish core compare, leading v tolerated, unparseable → no hint", () => {
  assert.equal(POLYFORGE_MINIMUM_PF_VERSION, "2.18.1");
  assert.equal(isPolyforgeVersionBelowMinimum("v2.17.0-e2e"), true, "below the floor carries the hint");
  assert.equal(isPolyforgeVersionBelowMinimum("v2.18.0"), true);
  assert.equal(isPolyforgeVersionBelowMinimum("v2.18.1"), false, "the floor itself is not below");
  assert.equal(isPolyforgeVersionBelowMinimum("v2.18.2-rc.1"), false);
  assert.equal(isPolyforgeVersionBelowMinimum("v2.19.1"), false);
  assert.equal(isPolyforgeVersionBelowMinimum("v2.20.0-e2e"), false);
  assert.equal(isPolyforgeVersionBelowMinimum("2.17.0"), true, "leading v tolerated");
  assert.equal(isPolyforgeVersionBelowMinimum(null), false, "no version → no hint, never a guess");
  assert.equal(isPolyforgeVersionBelowMinimum("banana"), false);
  assert.equal(isPolyforgeVersionBelowMinimum("v2.17.0", "v2.18.1"), true, "the floor is a parameter (pure)");
  // the named v2.20.x -> v0.2.x version-line cutover — the
  // unified line's pf is newer than every 2.20.x floor; v0.1.x and an older
  // 2.20.x stay below.
  for (const floor of ["2.18.1", "2.20.6", "2.20.12", "2.20.15", "2.20.18", "2.20.20", "2.20.21"]) {
    assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0", floor), false, `v0.2.0 is not below ${floor}`);
    assert.equal(isPolyforgeVersionBelowMinimum("v0.2.3", floor, true), false, `v0.2.3 is not below ${floor}`);
  }
  assert.equal(isPolyforgeVersionBelowMinimum("v0.1.9", "2.20.12"), true, "v0.1.9 stays below 2.20.12");
  assert.equal(isPolyforgeVersionBelowMinimum("v2.20.11", "2.20.12"), true, "2.20.11 stays below 2.20.12");
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0", "v0.2.1"), true, "inside the v0.2 line the compare is ordinary");
});

// (AC1): a suffixed release version vX.Y.Z-<name>.N orders by
// SemVer 2.0 precedence on every floor; the v0.2 line mapping stays.
test("suffixed versions order by semver: pre-release below its core, identifiers one by one", () => {
  assert.equal(isPolyforgeVersionBelowMinimum("v2.18.1-fork.1"), true, "a pre-release is below its core release");
  assert.equal(isPolyforgeVersionBelowMinimum("v2.18.2-fork.1"), false);
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0-fork.9", "v0.2.0-fork.10"), true, "numeric identifiers by number");
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0-fork.10", "v0.2.0-fork.9"), false);
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0-fork.5", "v0.2.1-fork.1"), true, "the core decides first");
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.12", "v0.2.0-fork.1"), false);
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0-fork.1", "2.20.21"), false, "the v0.2 line mapping stays");
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0-fork.1", "2.20.21", true), false);
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0-fork.1", "v0.2.0"), true);
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0", "v0.2.0-fork.1"), false);
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0-fork.1", "v0.2.0-fork.1"), false, "equal is not below");
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0-fork", "v0.2.0-fork.1"), true, "a shorter prefix first");
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0-1", "v0.2.0-fork"), true, "numeric below alphanumeric");
  assert.equal(isPolyforgeVersionBelowMinimum("v0.2.0-alpha.1", "v0.2.0-fork.1"), true, "alphanumeric in ASCII order");
});

// the opt-in unknown-is-below flag (the summarize floor).
test("unknownIsBelow counts a null/unparseable version below the floor only when asked", () => {
  assert.equal(isPolyforgeVersionBelowMinimum(null, "2.20.21", true), true, "null is below when asked");
  assert.equal(isPolyforgeVersionBelowMinimum("not-a-version", "2.20.21", true), true, "garbage is below when asked");
  assert.equal(isPolyforgeVersionBelowMinimum("2.20.20", "2.20.21", true), true);
  assert.equal(isPolyforgeVersionBelowMinimum("2.20.21", "2.20.21", true), false, "the floor itself is not below");
  assert.equal(isPolyforgeVersionBelowMinimum("v2.21.0", "2.20.21", true), false);
  assert.equal(isPolyforgeVersionBelowMinimum(null, "2.20.21"), false, "the default stays: unknown is never a guess");
  assert.equal(isPolyforgeVersionBelowMinimum("not-a-version", "2.20.21", false), false);
});

test("origin owner/repo pairs parse from both URL forms, .git stripped", () => {
  assert.equal(polyforgeOriginOwnerRepo("git@github.com:example-org/internal-forge.git"), "example-org/internal-forge");
  assert.equal(polyforgeOriginOwnerRepo("https://github.com/example-org/internal-forge.git"), "example-org/internal-forge");
  assert.equal(polyforgeOriginOwnerRepo("https://github.com/silgrid/some-other-repo.git"), "silgrid/some-other-repo");
  assert.equal(polyforgeOriginOwnerRepo("git@github.com:silgrid/repo"), "silgrid/repo", ".git optional");
  assert.equal(polyforgeOriginOwnerRepo("ssh://git@host:2222/owner/repo.git"), "owner/repo", "ssh URL form");
  for (const url of ["", "   ", "git@github.com", "git@github.com:owner/repo/deeper.git", "https://github.com/only-owner", "not a url", "git@github.com:/repo.git"]) {
    assert.equal(polyforgeOriginOwnerRepo(url), null, `${JSON.stringify(url)} carries no owner/repo pair`);
  }
});

test("line: 内部线 for the digest-matching origin, 公开线 otherwise, 未知 when undeterminable", () => {
  // The internal branch is driven through the digest parameter with a
  // neutral pair's sha256 (default = the built-in constant); the real
  // internal pair never appears here.
  const internalForge = "example-org/internal-forge";
  const internalForgeDigest = createHash("sha256").update(internalForge).digest("hex");
  assert.equal(polyforgeLineFromOrigin(`git@github.com:${internalForge}.git\n`, internalForgeDigest), "internal");
  assert.equal(polyforgeLineFromOrigin(`https://github.com/${internalForge}.git`, internalForgeDigest), "internal");
  assert.equal(polyforgeLineFromOrigin("git@github.com:example-org/internal-forge-x.git", internalForgeDigest), "public", "the pair must match exactly");
  assert.equal(polyforgeLineFromOrigin("https://github.com/silgrid/some-other-repo.git"), "public", "the default digest constant reports any other parseable origin as 公开线");
  assert.equal(polyforgeLineFromOrigin(""), "unknown");
  assert.equal(polyforgeLineFromOrigin("git@github.com:no-pair"), "unknown");
});

test("package root resolves only to a directory whose package.json name is polyforge", () => {
  const work = mkdtempSync(join(tmpdir(), "pi-web-pf-detect-"));
  try {
    const clone = join(work, "clone");
    mkdirSync(join(clone, "skills", "polyforge"), { recursive: true });
    writeFileSync(join(clone, "package.json"), `${JSON.stringify({ name: "polyforge", pi: { skills: ["./skills/polyforge"] } })}\n`);
    const other = join(work, "other");
    mkdirSync(other);
    writeFileSync(join(other, "package.json"), `${JSON.stringify({ name: "something-else" })}\n`);
    assert.equal(polyforgePackageRoot(clone), clone);
    assert.equal(polyforgePackageRoot(other), null, "a different package name is not the harness package");
    assert.equal(polyforgePackageRoot(join(work, "missing")), null, "a missing target is not the harness package");
    writeFileSync(join(clone, "readme.md"), "# readme\n");
    assert.equal(polyforgePackageRoot(join(clone, "readme.md")), clone, "a file source resolves to the package root beside it");
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

test("enabled = a plain string, or an object entry the disabled shape does not refuse", () => {
  const clone = "/clone/release/current";
  assert.equal(isPolyforgePackageEnabled(clone), true);
  assert.equal(isPolyforgePackageEnabled({ source: clone, skills: ["./skills/polyforge"] }), true);
  assert.equal(isPolyforgePackageEnabled({ source: clone, extensions: [], skills: [], prompts: [], themes: [] }), false);
  assert.equal(isPolyforgePackageEnabled({ source: clone, autoload: false }), true, "an object without arrays is filtered, not disabled");
});

test("claude SessionStart hook: the matcher form and bare hooks, command text only matched", () => {
  const hook = (command) => ({ hooks: { SessionStart: [{ hooks: [{ type: "command", command }] }] } });
  assert.equal(claudeSessionStartHookPresent(hook("polyforge hook session-start --token SECRET-VALUE")), true);
  assert.equal(claudeSessionStartHookPresent({ hooks: { SessionStart: [{ type: "command", command: "polyforge start" }] } }), true, "a bare hook list is accepted");
  assert.equal(claudeSessionStartHookPresent({ hooks: { SessionStart: [{ hooks: [{ type: "command", command: "echo hello" }] }] } }), false);
  assert.equal(claudeSessionStartHookPresent({ hooks: { SessionStart: [] } }), false);
  assert.equal(claudeSessionStartHookPresent({ hooks: {} }), false);
  assert.equal(claudeSessionStartHookPresent({}), false);
  assert.equal(claudeSessionStartHookPresent(null), false);
  assert.equal(claudeSessionStartHookPresent("banana"), false);
  assert.equal(claudeSessionStartHookPresent({ hooks: { SessionStart: "banana" } }), false);
  assert.equal(claudeSessionStartHookPresent({ hooks: { SessionStart: [{ hooks: "banana" }] } }), false);
});

test("claude skills symlink resolves through the link; broken or missing reads absent", () => {
  const work = mkdtempSync(join(tmpdir(), "pi-web-pf-detect-"));
  try {
    const claudeDir = join(work, ".claude");
    mkdirSync(join(claudeDir, "skills"), { recursive: true });
    mkdirSync(join(work, "target"));
    writeFileSync(join(work, "target", "SKILL.md"), "# target\n");
    assert.equal(claudeSkillsSymlinkExists(claudeDir), false, "no symlink → absent");
    symlinkSync(join(work, "target", "SKILL.md"), join(claudeDir, "skills", "polyforge"));
    assert.equal(claudeSkillsSymlinkExists(claudeDir), true);
    rmSync(join(claudeDir, "skills", "polyforge"));
    symlinkSync(join(work, "gone"), join(claudeDir, "skills", "polyforge"));
    assert.equal(claudeSkillsSymlinkExists(claudeDir), false, "an unresolvable symlink reads absent");
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
});

test("the conventional clone root is install.sh's fixed path", () => {
  assert.equal(conventionalPolyforgeRoot("/home/e2e"), join("/home/e2e", ".local", "share", "polyforge", "release", "current"));
});

// ---- assembled states (a throwaway HOME + a stub pf first on PATH) ----

const work = mkdtempSync(join(tmpdir(), "pi-web-pf-detect-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const claudeDir = join(home, ".claude");
const stubDir = join(work, "stub-bin");
const emptyPathDir = join(work, "no-pf");
mkdirSync(join(agentDir, "sessions"), { recursive: true });
mkdirSync(join(claudeDir, "skills"), { recursive: true });
mkdirSync(stubDir);
mkdirSync(emptyPathDir);

const clone = join(home, ".local", "share", "polyforge", "release", "current");
mkdirSync(join(clone, "skills", "polyforge"), { recursive: true });
writeFileSync(join(clone, "package.json"), `${JSON.stringify({ name: "polyforge", version: "0.0.0", pi: { skills: ["./skills/polyforge"] } }, null, 2)}\n`);
writeFileSync(join(clone, "skills", "polyforge", "SKILL.md"), "# polyforge skill\n");
const gitSync = (...args) => {
  const result = spawnSync("git", ["-C", clone, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(`git ${args.join(" ")}: ${result.stderr}`);
};
gitSync("init");
const setOrigin = (url) => {
  spawnSync("git", ["-C", clone, "remote", "remove", "origin"]);
  if (url) gitSync("remote", "add", "origin", url);
};

const stderrTail = "E2E-STDERR-TAIL-SENTINEL-122";
const stubStderr = `pf: daemon not reachable (connection refused) ${" ".repeat(600)}${stderrTail}`;
const stateFile = join(stubDir, "state.json");
const setStub = (patch) => writeFileSync(stateFile, JSON.stringify({
  mode: "ok",
  version: "v2.19.1",
  ops: { queue: { pending: 0, running: 1, max_concurrent: 4 } },
  ...patch,
}));
setStub();
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename)); const args = process.argv.slice(2);
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
if (args.includes("--version")) { process.stdout.write(String(s.version) + "\\n"); process.exit(0); }
if (s.mode === "down") { process.stderr.write(s.stderr + "\\n"); process.exit(1); }
process.stdout.write(JSON.stringify(s.ops) + "\\n");
`);
chmodSync(join(stubDir, "pf"), 0o755);

const globalSettings = join(agentDir, "settings.json");
const claudeSettings = join(claudeDir, "settings.json");
const setGlobalPackages = (packages) => {
  if (packages === null) rmSync(globalSettings, { force: true });
  else writeFileSync(globalSettings, `${JSON.stringify({ packages }, null, 2)}\n`);
};
const setClaude = ({ symlink = true, hook = true } = {}) => {
  rmSync(join(claudeDir, "skills", "polyforge"), { force: true });
  if (symlink) symlinkSync(join(clone, "skills", "polyforge"), join(claudeDir, "skills", "polyforge"));
  if (hook) writeFileSync(claudeSettings, `${JSON.stringify({
    hooks: { SessionStart: [{ hooks: [{ type: "command", command: "polyforge hook session-start --token E2E-SECRET-HOOK-122" }] }] },
  }, null, 2)}\n`);
  else rmSync(claudeSettings, { force: true });
};

const savedEnv = { HOME: process.env.HOME, PATH: process.env.PATH, AGENT_DIR: process.env.PI_CODING_AGENT_DIR };
function withEnv(pathMode) {
  process.env.HOME = home;
  process.env.PI_CODING_AGENT_DIR = agentDir;
  process.env.PATH = pathMode === "absent" ? emptyPathDir : `${stubDir}${delimiter}${savedEnv.PATH}`;
}
function restoreEnv() {
  process.env.HOME = savedEnv.HOME;
  process.env.PATH = savedEnv.PATH;
  if (savedEnv.AGENT_DIR === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = savedEnv.AGENT_DIR;
}

const resetFixtures = () => {
  setStub({});
  setOrigin("git@github.com:example-org/public-forge.git");
  setClaude();
  setGlobalPackages([{ source: clone, skills: ["./skills/polyforge"] }]);
};

after(() => {
  restoreEnv();
  rmSync(work, { recursive: true, force: true });
});

test("pf absent: 200-shaped flat JSON, every dependent field absent, daemon 不适用", async () => {
  setGlobalPackages(null);
  withEnv("absent");
  try {
    const status = await detectPolyforge(true);
    assert.deepEqual(status, {
      pfPresent: false,
      pfVersion: null,
      pfVersionBelowMinimum: false,
      line: "unknown",
      piSkillInstalled: false,
      piSkillEnabled: false,
      claudeSkillsSymlink: false,
      claudeSessionStartHook: false,
      daemon: null,
      capabilities: [],
      cellsSupported: false,
    }, "the absent state is flat, complete and carries no error field");
    // R8 surface: exactly the eleven keys (adds the
    // capability pair), no home paths anywhere.
    assert.deepEqual(Object.keys(status).sort(), [
      "capabilities", "cellsSupported", "claudeSessionStartHook", "claudeSkillsSymlink", "daemon", "line",
      "pfPresent", "pfVersion", "pfVersionBelowMinimum", "piSkillEnabled", "piSkillInstalled",
    ]);
    assert.ok(!JSON.stringify(status).includes(home), "the response carries no home paths");
  } finally {
    restoreEnv();
  }
});

test("pf present: version verbatim, no hint above the floor, line/pi-skill/claude/daemon all reported", async () => {
  resetFixtures();
  withEnv("stub");
  try {
    const status = await detectPolyforge(true);
    assert.equal(status.pfPresent, true);
    assert.equal(status.pfVersion, "v2.19.1");
    assert.equal(status.pfVersionBelowMinimum, false);
    assert.equal(status.line, "public", "the neutral public fixture origin reports 公开线 (内部线 is covered by the pure digest test)");
    assert.equal(status.piSkillInstalled, true);
    assert.equal(status.piSkillEnabled, true);
    assert.equal(status.claudeSkillsSymlink, true);
    assert.equal(status.claudeSessionStartHook, true);
    assert.deepEqual(status.daemon, { reachable: true });
    assert.ok(!JSON.stringify(status).includes(home), "the response carries no home paths");
    assert.ok(!JSON.stringify(status).includes("E2E-SECRET-HOOK-122"), "the hook command text never leaves");
  } finally {
    restoreEnv();
  }
});

test("below the floor: the hint blocks nothing — every other field still reported", async () => {
  resetFixtures();
  setStub({ version: "v2.17.0-e2e" });
  withEnv("stub");
  try {
    const status = await detectPolyforge(true);
    assert.equal(status.pfVersion, "v2.17.0-e2e");
    assert.equal(status.pfVersionBelowMinimum, true, "below v2.18.1 carries the hint");
    assert.equal(status.pfPresent, true);
    assert.equal(status.line, "public", "the hint must not block the line detection");
    assert.equal(status.piSkillInstalled, true);
    assert.equal(status.piSkillEnabled, true);
    assert.equal(status.claudeSkillsSymlink, true);
    assert.equal(status.claudeSessionStartHook, true);
    assert.equal(status.daemon?.reachable, true, "the hint must not block the daemon detection");
  } finally {
    restoreEnv();
  }
});

test("line by origin: public, unknown (no origin), never a guess — 内部线 by the pure digest test", async () => {
  resetFixtures();
  withEnv("stub");
  try {
    setOrigin("git@github.com:example-org/public-forge.git");
    assert.equal((await detectPolyforge(true)).line, "public");
    setOrigin("https://github.com/silgrid/some-other-repo.git");
    assert.equal((await detectPolyforge(true)).line, "public");
    setOrigin(null);
    assert.equal((await detectPolyforge(true)).line, "unknown", "no origin → unknown");
    setOrigin("git@github.com:example-org/public-forge.git");
  } finally {
    restoreEnv();
  }
});

test("pi skill from the global entry only: enabled, disabled shape, project scope ignored, missing target", async () => {
  resetFixtures();
  withEnv("stub");
  try {
    setGlobalPackages([{ source: clone, skills: ["./skills/polyforge"] }]);
    let status = await detectPolyforge(true);
    assert.equal(status.piSkillInstalled, true);
    assert.equal(status.piSkillEnabled, true);
    setGlobalPackages([{ source: clone, extensions: [], skills: [], prompts: [], themes: [] }]);
    status = await detectPolyforge(true);
    assert.equal(status.piSkillInstalled, true);
    assert.equal(status.piSkillEnabled, false, "empty resource arrays report 未启用");
    // A project-scope entry is never consulted: with no global entry the
    // skill reads 未安装 even though the conventional clone still resolves
    // the line (the fixture project's own .pi/settings.json carries one).
    setGlobalPackages(null);
    status = await detectPolyforge(true);
    assert.equal(status.piSkillInstalled, false, "no global entry → 未安装 (project scope ignored)");
    assert.equal(status.line, "public", "the line still resolves through the conventional root");
    setGlobalPackages([{ source: join(work, "no-such-package"), skills: [] }]);
    status = await detectPolyforge(true);
    assert.equal(status.piSkillInstalled, false, "a missing target reports 未安装");
    // An unparsable global settings file degrades to no entries, never an error.
    writeFileSync(globalSettings, "{ not json");
    status = await detectPolyforge(true);
    assert.equal(status.piSkillInstalled, false, "an unparsable settings file degrades to 未安装");
    setGlobalPackages([{ source: clone, skills: ["./skills/polyforge"] }]);
  } finally {
    restoreEnv();
  }
});

test("claude items are separate: each reports its own absent state", async () => {
  resetFixtures();
  withEnv("stub");
  try {
    setClaude({ symlink: false, hook: true });
    let status = await detectPolyforge(true);
    assert.equal(status.claudeSkillsSymlink, false, "the symlink item reports its own absent state");
    assert.equal(status.claudeSessionStartHook, true, "the hook item does not follow the symlink");
    setClaude({ symlink: true, hook: false });
    status = await detectPolyforge(true);
    assert.equal(status.claudeSkillsSymlink, true);
    assert.equal(status.claudeSessionStartHook, false);
    setClaude({ symlink: true, hook: true });
    status = await detectPolyforge(true);
    assert.equal(status.claudeSkillsSymlink, true);
    assert.equal(status.claudeSessionStartHook, true);
  } finally {
    restoreEnv();
  }
});

test("daemon: reachable via pf --json ops status, unreachable with pf stderr capped at 512", async () => {
  resetFixtures();
  withEnv("stub");
  try {
    rmSync(join(stubDir, "argv.log"), { force: true });
    let status = await detectPolyforge(true);
    assert.equal(status.daemon?.reachable, true);
    const { readFile } = await import("node:fs/promises");
    const argv = (await readFile(join(stubDir, "argv.log"), "utf8")).trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
    assert.ok(
      argv.some((call) => call.includes("--json") && call.includes("ops") && call.includes("status")),
      "daemon reachability is probed via pf --json ops status",
    );
    setStub({ mode: "down", stderr: stubStderr });
    status = await detectPolyforge(true);
    assert.equal(status.daemon?.reachable, false);
    assert.ok(String(status.daemon?.error ?? "").length <= 512, "pf stderr is capped at 512 bytes");
    assert.ok(!JSON.stringify(status).includes(stderrTail), "stderr beyond the cap never leaves");
    setStub({});
  } finally {
    restoreEnv();
  }
});

test("the presence cache carries the version; force bypasses it, non-force reads it", async () => {
  resetFixtures();
  withEnv("stub");
  try {
    assert.equal((await detectPolyforge(true)).pfVersion, "v2.19.1");
    setStub({ version: "v2.20.0-e2e" });
    assert.equal((await detectPolyforge(false)).pfVersion, "v2.19.1", "inside the 60s window the cache wins");
    assert.equal((await detectPolyforge(true)).pfVersion, "v2.20.0-e2e", "force bypasses the cache");
    setStub({});
  } finally {
    restoreEnv();
  }
});