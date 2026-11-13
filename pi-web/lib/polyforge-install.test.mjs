import "#test-env";
import assert from "node:assert/strict";
import { after, test } from "node:test";
import { createJiti } from "jiti";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";

// Settings › Polyforge's pf install / update entry. The pure
// judgements, then the server module against a stub `gh` (the release list
// and the fetched tools/install.sh), a FAKE install.sh it serves and a stub
// `pf` — never a real installer.

const jiti = createJiti(import.meta.url);
const pure = await jiti.import("./polyforge-install-pure.ts");
// Both modules load before any test is registered: a top-level await after
// the first test lets the runner start (and finish) the registered ones.
const lib = await jiti.import("./polyforge-install.ts");

// ---- pure ----

test("the channel is Pi Web's own release channel and the copyable command's", async () => {
  const marker = JSON.parse(readFileSync(new URL("../bin/pi-web-release-channel.json", import.meta.url), "utf8"));
  assert.equal(pure.POLYFORGE_INSTALL_REPO, marker.repo, "pf installs from the repository whose releases carry Pi Web");
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const text = readFileSync(new URL(`./i18n/messages/${locale}.ts`, import.meta.url), "utf8");
    const command = text.match(/"polyforge\.settings\.installCommand": "((?:[^"\\]|\\.)*)"/)?.[1]?.replace(/\\"/g, '"') ?? "";
    assert.ok(command.includes(`https://raw.githubusercontent.com/${pure.POLYFORGE_INSTALL_REPO}/${pure.POLYFORGE_INSTALL_REF}/tools/install.sh`), `${locale}: the command fetches install.sh from the channel`);
    assert.ok(command.includes(`PF_INSTALL_REPO=${pure.POLYFORGE_INSTALL_REPO} bash -s --`), `${locale}: the command pins PF_INSTALL_REPO to the channel`);
  }
});

test("version flags: update available, a stale daemon, Pi Web out of sync — a missing version raises none", () => {
  const flags = (patch) => pure.polyforgeVersionFlags({ installed: "v0.2.5", latest: "v0.2.5", running: "v0.2.5", piWeb: "0.2.5", ...patch });
  assert.deepEqual(flags({}), { updateAvailable: false, daemonStale: false, piWebOutOfSync: false }, "a leading v does not count");
  assert.equal(flags({ latest: "v0.2.6" }).updateAvailable, true);
  assert.equal(flags({ running: "v0.2.4" }).daemonStale, true, "installed new, daemon still the old process");
  assert.equal(flags({ piWeb: "0.2.4" }).piWebOutOfSync, true);
  assert.deepEqual(flags({ installed: null }), { updateAvailable: false, daemonStale: false, piWebOutOfSync: false });
  assert.deepEqual(flags({ latest: null, running: null, piWeb: "" }), { updateAvailable: false, daemonStale: false, piWebOutOfSync: false });
});

test("install.sh is relevant when pf is absent, behind, its daemon stale, or a run did not succeed", () => {
  const v = (patch) => ({ pfPresent: true, installed: "v0.2.5", latest: "v0.2.5", running: "v0.2.5", piWeb: "0.2.5", ...patch });
  assert.equal(pure.polyforgeInstallRelevant(v({}), null), false, "installed, current, daemon on it");
  assert.equal(pure.polyforgeInstallRelevant(v({ piWeb: "0.2.4" }), null), false, "Pi Web out of sync is not install.sh's to fix");
  assert.equal(pure.polyforgeInstallRelevant(v({ pfPresent: false, installed: null }), null), true);
  assert.equal(pure.polyforgeInstallRelevant(v({ latest: "v0.2.6" }), null), true);
  assert.equal(pure.polyforgeInstallRelevant(v({ running: "v0.2.4" }), null), true);
  assert.equal(pure.polyforgeInstallRelevant(null, null), false, "unknown versions prove nothing");
  for (const state of ["running", "refused", "failed"]) assert.equal(pure.polyforgeInstallRelevant(v({}), { state }), true, state);
  assert.equal(pure.polyforgeInstallRelevant(v({}), { state: "succeeded" }), false);
});

test("busy jobs follow install.sh's rule: every status but paused and end_pending, slug else id", () => {
  const status = { machine_id: "m", jobs: [
    { work_item_id: "wi_1", slug: "demo#1", status: "running" },
    { work_item_id: "wi_2", slug: "demo#2", status: "paused" },
    { work_item_id: "wi_3", slug: "demo#3", status: "end_pending" },
    { work_item_id: "wi_4", status: "recovering" },
  ] };
  assert.deepEqual(pure.polyforgeInstallBusyJobs(status), [{ slug: "demo#1", status: "running" }, { slug: "wi_4", status: "recovering" }]);
  assert.deepEqual(pure.polyforgeInstallBusyJobs({ machine_id: "m" }), []);
  assert.equal(pure.polyforgeInstallBusyJobs({ jobs: [] }), null, "not a daemon's status");
  assert.equal(pure.polyforgeInstallBusyJobs(null), null);
});

test("install.sh's refusal: its ERROR line and the indented jobs under it", () => {
  const lines = [
    "== 2/5 switch",
    "ERROR: the daemon is running jobs; upgrading to v0.2.6 needs a graceful shutdown. Upgrade refused; nothing on this machine was changed",
    "     demo#7 (running)",
    "     demo#10 (recovering)",
    "   proceed by one of: wait, pause them, or re-run with PF_INSTALL_FORCE=1",
  ];
  assert.deepEqual(pure.parseInstallRefusal(lines), {
    reason: "the daemon is running jobs; upgrading to v0.2.6 needs a graceful shutdown. Upgrade refused; nothing on this machine was changed",
    jobs: ["demo#7 (running)", "demo#10 (recovering)"],
  });
  assert.deepEqual(pure.parseInstallRefusal(["ERROR: the daemon's job list could not be read", "   check: pf ops status"]).jobs, []);
  assert.equal(pure.isInstallRefusal(lines), true, "the remedy names PF_INSTALL_FORCE=1");
  assert.equal(pure.isInstallRefusal(["ERROR: the daemon's job list could not be read", "   check: pf ops status. re-run with PF_INSTALL_FORCE=1."]), true);
  assert.equal(pure.isInstallRefusal(["ERROR: PF_INSTALL_FORCE=1 in the error itself is no remedy"]), false);
  assert.equal(pure.isInstallRefusal(["ERROR: something else broke"]), false);
  assert.equal(pure.isInstallRefusal(["no error at all"]), false);
  assert.equal(pure.lastInstallError(["ERROR: a", "x", "ERROR: b"]), "b");
  assert.equal(pure.lastInstallError(["ok"]), null);
});

test("host and key shapes: a host is never a flag; a key is one printable line", () => {
  for (const ok of ["10.0.0.1", "aihub.example", "h"]) assert.ok(pure.isInstallHost(ok), ok);
  for (const bad of ["", "-x", "a b", "http://h", "h:8080", "h;rm", 3, null]) assert.ok(!pure.isInstallHost(bad), String(bad));
  assert.ok(pure.isInstallApiKey("pf_k1_EXAMPLEKEY"));
  for (const bad of ["", "short", "has space key", "line\nbreak-key", "x".repeat(4097), 5]) assert.ok(!pure.isInstallApiKey(bad), String(bad));
  assert.equal(pure.sanitizeInstallVersion(" v0.2.5\n"), "v0.2.5");
  assert.equal(pure.sanitizeInstallVersion("dev"), "dev");
  assert.equal(pure.sanitizeInstallVersion("v1; rm"), null);
});

// ---- server, against stub gh / pf and a fake install.sh ----

const work = mkdtempSync(join(tmpdir(), "pf-install-"));
const bin = join(work, "bin");
const home = join(work, "home");
mkdirSync(bin);
mkdirSync(join(home, ".config", "polyforge"), { recursive: true });
const stateFile = join(work, "state.json");
const ghLog = join(work, "gh.log");
const installLog = join(work, "install.log");
const setState = (patch) => writeFileSync(stateFile, JSON.stringify({
  tag: "v0.2.6", pf: "v0.2.5", daemon: "v0.2.5", jobs: [], fetchFail: false, script: "ok", ...patch,
}));
setState({});

// The FAKE install.sh the stub gh serves: it records its argv, the two
// PF_INSTALL_* variables, whether the key is anywhere in its environment,
// and its stdin; then behaves as the state says. It echoes what it read on
// stdin, so the masking of the key in the output is proven, not assumed.
const fakeInstall = `#!/bin/sh
KEY=""
[ "$1" = - ] && IFS= read -r KEY
"${process.execPath}" -e 'const fs=require("fs");fs.appendFileSync(process.argv[1], JSON.stringify({argv: process.argv[2], force: process.env.PF_INSTALL_FORCE || "", repo: process.env.PF_INSTALL_REPO || "", host: process.env.PF_HOST || "", leaked: ["PI_WEB_PASSWORD", "PORT", "NODE_ENV", "NEXT_PUBLIC_APP_VERSION", "TMUX"].filter((k) => k in process.env), envHasKey: Object.values(process.env).some((v) => v.includes(process.argv[3] || "\\u0000")), stdin: process.argv[3]}) + "\\n")' ${JSON.stringify(installLog)} "$0 $*" "$KEY"
echo "== 1/5 prepare"
echo "   release channel: $PF_INSTALL_REPO"
[ -n "$KEY" ] && echo "   read a key: $KEY"
mode=$("${process.execPath}" -e 'process.stdout.write(JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).script)' ${JSON.stringify(stateFile)})
case "$mode" in
  exit3) echo "ERROR: something else broke"; exit 3 ;;
  refuse)
    if [ "\${PF_INSTALL_FORCE:-}" != 1 ]; then
      echo "ERROR: the daemon is running jobs; upgrading to v0.2.6 needs a graceful shutdown, which terminates their agent trees. Upgrade refused; nothing on this machine was changed"
      echo "     demo#7 (running)"
      echo "   proceed by one of: wait, pause them, or re-run with PF_INSTALL_FORCE=1"
      exit 3
    fi
    echo "   PF_INSTALL_FORCE=1: shutting down despite running jobs" ;;
  fail) echo "ERROR: SHA mismatch on pf-linux-amd64 of v0.2.6; nothing on this machine was changed" >&2; exit 1 ;;
esac
echo "== 5/5 verify"
echo "Done."
`;
writeFileSync(join(work, "install.sh.served"), fakeInstall);
writeFileSync(join(bin, "gh"), `#!${process.execPath}
const fs = require("fs"); const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(ghLog)}, JSON.stringify(args) + "\\n");
const s = JSON.parse(fs.readFileSync(${JSON.stringify(stateFile)}, "utf8"));
if (args[0] === "release" && args[1] === "list") { process.stdout.write(s.tag + "\\n"); process.exit(0); }
if (args[0] === "api") {
  if (s.fetchFail) { process.stderr.write("HTTP 404: Not Found\\n"); process.exit(1); }
  process.stdout.write(fs.readFileSync(${JSON.stringify(join(work, "install.sh.served"))}, "utf8")); process.exit(0);
}
process.exit(2);
`);
writeFileSync(join(bin, "pf"), `#!${process.execPath}
const fs = require("fs"); const args = process.argv.slice(2);
const s = JSON.parse(fs.readFileSync(${JSON.stringify(stateFile)}, "utf8"));
if (args.includes("--version")) { process.stdout.write(s.pf + "\\n"); process.exit(0); }
if (args.join(" ") === "--json ops status") {
  if (s.daemon === null) { process.stderr.write("pf: daemon not reachable\\n"); process.exit(1); }
  process.stdout.write(JSON.stringify({ machine_id: "m-test", version: s.daemon, jobs: s.jobs, queue: { running: 0 } })); process.exit(0);
}
process.exit(2);
`);
chmodSync(join(bin, "gh"), 0o755);
chmodSync(join(bin, "pf"), 0o755);

const saved = Object.fromEntries(["PATH", "HOME", "PF_BIN", "PF_INSTALL_FORCE", "PF_HOST", "NEXT_PUBLIC_APP_VERSION", "PI_WEB_PASSWORD", "PORT", "NODE_ENV", "TMUX"].map((name) => [name, process.env[name]]));
process.env.PATH = `${bin}${delimiter}${process.env.PATH}`;
process.env.HOME = home;
process.env.PF_BIN = join(bin, "pf");
process.env.NEXT_PUBLIC_APP_VERSION = "0.2.4";
// an inherited override must never force or redirect a run
process.env.PF_INSTALL_FORCE = "1";
process.env.PF_HOST = "inherited.example";
// Pi Web's own runtime variables must never reach the installer (nor a tmux
// server it may start): the project-command sanitizer drops them
process.env.PI_WEB_PASSWORD = "pwsentry";
process.env.PORT = "30141";
process.env.NODE_ENV = "production";
process.env.TMUX = "/tmp/pi-web-test-tmux,1,0";
after(() => {
  for (const [key, value] of Object.entries(saved)) {
    if (value === undefined) delete process.env[key]; else process.env[key] = value;
  }
  rmSync(work, { recursive: true, force: true });
});

const cliToml = join(home, ".config", "polyforge", "cli.toml");
const setConfigured = (configured) => {
  if (configured) writeFileSync(cliToml, 'server = "http://h:8080"\napi_key = "cfgsentry"\n');
  else rmSync(cliToml, { force: true });
};
const installCalls = () => existsSync(installLog) ? readFileSync(installLog, "utf8").trim().split("\n").filter(Boolean).map((l) => JSON.parse(l)) : [];
const ghCalls = () => existsSync(ghLog) ? readFileSync(ghLog, "utf8").trim().split("\n").filter(Boolean).map((l) => JSON.parse(l)) : [];
const reset = () => { rmSync(installLog, { force: true }); rmSync(ghLog, { force: true }); };

/** Collect the run's whole output and its final state through the same
 *  subscription the events route uses. */
function runToEnd() {
  return new Promise((resolve) => {
    const lines = [];
    let sub = null;
    sub = lib.subscribePolyforgeInstall(0, (event) => {
      if (event.type === "line") lines.push(event.text);
      else resolve({ lines, run: event.run });
    });
    lines.push(...sub.lines.map((line) => line.text));
    if (sub.run.state !== "running") resolve({ lines, run: sub.run });
  });
}

test("versions: installed, latest on the channel, the running daemon's own, busy jobs, Pi Web, first-install need", async () => {
  setState({ jobs: [{ work_item_id: "wi_7", slug: "demo#7", status: "running" }, { slug: "demo#8", status: "paused" }] });
  setConfigured(true);
  reset();
  const v = await lib.readPolyforgeInstallVersions(true);
  assert.equal(v.repo, pure.POLYFORGE_INSTALL_REPO);
  assert.equal(v.installed, "v0.2.5");
  assert.equal(v.latest, "v0.2.6");
  assert.equal(v.running, "v0.2.5");
  assert.equal(v.daemonReachable, true);
  assert.deepEqual(v.jobs, [{ slug: "demo#7", status: "running" }]);
  assert.equal(v.piWeb, "0.2.4");
  assert.equal(v.needsConfig, false);
  assert.deepEqual(ghCalls()[0], ["release", "list", "--repo", pure.POLYFORGE_INSTALL_REPO, "--limit", "1", "--json", "tagName", "--jq", ".[0].tagName"]);
  assert.ok(!JSON.stringify(v).includes("cfgsentry"), "the config's key never leaves");
  setState({ daemon: null });
  setConfigured(false);
  const down = await lib.readPolyforgeInstallVersions(true);
  assert.equal(down.daemonReachable, false);
  assert.match(down.daemonError, /not reachable/);
  assert.equal(down.running, null);
  assert.equal(down.needsConfig, true);
});

test("update: install.sh fetched from the channel, run with `-` and no key, output kept line by line, success concluded", async () => {
  setState({});
  setConfigured(true);
  reset();
  const started = await lib.startPolyforgeInstall({});
  assert.equal(started.state, "running");
  assert.equal(started.mode, "update");
  await assert.rejects(lib.startPolyforgeInstall({}), (error) => error.status === 409 && /already running/.test(error.message), "one run at a time");
  const { lines, run } = await runToEnd();
  assert.equal(run.state, "succeeded");
  assert.equal(run.exitCode, 0);
  assert.ok(lines.includes("== 1/5 prepare") && lines.includes("Done."), lines.join("\n"));
  assert.equal(run.lineCount, lines.length);
  const fetch = ghCalls().find((call) => call[0] === "api");
  assert.deepEqual(fetch, ["api", "-H", "Accept: application/vnd.github.raw", `repos/${pure.POLYFORGE_INSTALL_REPO}/contents/tools/install.sh?ref=main`]);
  const [call] = installCalls();
  assert.match(call.argv, / -$/, "an update passes `-` and no host");
  assert.equal(call.stdin, "", "an update writes no key");
  assert.equal(call.repo, pure.POLYFORGE_INSTALL_REPO, "the channel is pinned");
  assert.equal(call.force, "", "an inherited PF_INSTALL_FORCE never forces a run");
  assert.equal(call.host, "", "an inherited PF_HOST never redirects a run");
  assert.deepEqual(call.leaked, [], "PI_WEB_PASSWORD, PORT, NODE_ENV, NEXT_* and TMUX never reach install.sh");
  assert.deepEqual(lib.polyforgeInstallRun(), run, "the result stays readable after the run");
});

test("first install: the key reaches install.sh on stdin only — never argv, env, output or the run", async () => {
  // a fixture named without a credential keyword: no secret-scan shape
  const SENTINEL = "pf_k1_STDINONLYEXAMPLE";
  setState({});
  setConfigured(false);
  reset();
  await assert.rejects(lib.startPolyforgeInstall({ apiKey: SENTINEL }), (error) => error.status === 400 && /host/.test(error.message));
  await assert.rejects(lib.startPolyforgeInstall({ host: "10.0.0.9" }), (error) => error.status === 400 && /apiKey/.test(error.message));
  await assert.rejects(lib.startPolyforgeInstall({ host: "-x", apiKey: SENTINEL }), (error) => error.status === 400);
  assert.deepEqual(installCalls(), [], "a refused request runs nothing");
  assert.deepEqual(ghCalls(), [], "a refused request fetches nothing");
  await lib.startPolyforgeInstall({ host: "10.0.0.9", apiKey: SENTINEL });
  const { lines, run } = await runToEnd();
  assert.equal(run.state, "succeeded");
  const [call] = installCalls();
  assert.equal(call.stdin, SENTINEL, "the key arrives on stdin");
  assert.match(call.argv, / - 10\.0\.0\.9$/, "argv carries `-` and the host only");
  assert.ok(!call.argv.includes(SENTINEL), "never argv");
  assert.equal(call.envHasKey, false, "never the environment");
  for (const call of ghCalls()) assert.ok(!call.join(" ").includes(SENTINEL), "never gh's argv");
  assert.ok(lines.includes("   read a key: ***"), "a script that prints the key shows it masked");
  assert.ok(!lines.join("\n").includes(SENTINEL), "never the output");
  assert.ok(!JSON.stringify(run).includes(SENTINEL) && !JSON.stringify(lib.polyforgeInstallRun()).includes(SENTINEL), "never the run");
  const source = readFileSync(new URL("./polyforge-install.ts", import.meta.url), "utf8");
  assert.doesNotMatch(source, /console\./, "the module logs nothing");
});

test("jobs running: install.sh's refusal is kept with its reason and jobs; a force answers that refusal by its id and sets PF_INSTALL_FORCE=1", async () => {
  setState({ script: "refuse" });
  setConfigured(true);
  reset();
  await assert.rejects(lib.startPolyforgeInstall({ force: true, refusedRun: lib.polyforgeInstallRun()?.id }), (error) => error.status === 409 && /only right after/.test(error.message), "no force before a refusal");
  await assert.rejects(lib.startPolyforgeInstall({ force: "yes" }), (error) => error.status === 400);
  await lib.startPolyforgeInstall({});
  const refused = await runToEnd();
  assert.equal(refused.run.state, "refused");
  assert.equal(refused.run.exitCode, 3);
  assert.match(refused.run.refusal.reason, /^the daemon is running jobs;.*Upgrade refused/);
  assert.deepEqual(refused.run.refusal.jobs, ["demo#7 (running)"]);
  assert.equal(installCalls()[0].force, "");
  reset();
  await assert.rejects(lib.startPolyforgeInstall({ force: true }), (error) => error.status === 409 && /must name the refused run/.test(error.message), "a force names the refusal it answers");
  await assert.rejects(lib.startPolyforgeInstall({ force: true, refusedRun: "another-run" }), (error) => error.status === 409 && /must name the refused run/.test(error.message));
  assert.deepEqual(installCalls(), [], "a refused force runs nothing");
  const forced = await lib.startPolyforgeInstall({ force: true, refusedRun: refused.run.id });
  assert.equal(forced.force, true);
  const done = await runToEnd();
  assert.equal(done.run.state, "succeeded");
  assert.equal(installCalls()[0].force, "1", "the confirmed force reaches install.sh as PF_INSTALL_FORCE=1");
  assert.ok(done.lines.some((line) => line.includes("PF_INSTALL_FORCE=1")));
  await assert.rejects(lib.startPolyforgeInstall({ force: true, refusedRun: refused.run.id }), (error) => error.status === 409, "a force is not reusable after it ran");
});

test("a refusal is answerable for 10 minutes only, and the force is re-checked after the start's await", async () => {
  setState({ script: "refuse" });
  setConfigured(true);
  reset();
  await lib.startPolyforgeInstall({});
  const refused = await runToEnd();
  assert.equal(refused.run.state, "refused");
  // age the refusal past the window (the store is this process's own)
  const record = globalThis.__piWebPolyforgeInstall.current;
  const endedAt = record.run.endedAt;
  record.run.endedAt = new Date(Date.now() - pure.POLYFORGE_INSTALL_FORCE_WINDOW_MS - 1000).toISOString();
  await assert.rejects(lib.startPolyforgeInstall({ force: true, refusedRun: refused.run.id }), (error) => error.status === 409 && /older than 10 minutes/.test(error.message));
  record.run.endedAt = endedAt;
  // two forces answering the same refusal: both pass the first check, the
  // second is refused by the check after the await
  reset();
  const results = await Promise.allSettled([
    lib.startPolyforgeInstall({ force: true, refusedRun: refused.run.id }),
    lib.startPolyforgeInstall({ force: true, refusedRun: refused.run.id }),
  ]);
  assert.deepEqual(results.map((result) => result.status).sort(), ["fulfilled", "rejected"]);
  assert.equal(results.find((result) => result.status === "rejected").reason.status, 409);
  const done = await runToEnd();
  assert.equal(done.run.state, "succeeded");
  assert.equal(installCalls().length, 1, "one forced run, not two");
});

test("exit 3 without install.sh's refusal text is a failure, never offered a force", async () => {
  setState({ script: "exit3" });
  setConfigured(true);
  reset();
  await lib.startPolyforgeInstall({});
  const ended = await runToEnd();
  assert.equal(ended.run.state, "failed");
  assert.equal(ended.run.exitCode, 3);
  assert.equal(ended.run.error, "something else broke");
  await assert.rejects(lib.startPolyforgeInstall({ force: true, refusedRun: ended.run.id }), (error) => error.status === 409);
});

test("a failing install concludes failed with install.sh's ERROR line; a failed fetch runs nothing", async () => {
  setState({ script: "fail" });
  setConfigured(true);
  reset();
  await lib.startPolyforgeInstall({});
  const failed = await runToEnd();
  assert.equal(failed.run.state, "failed");
  assert.equal(failed.run.exitCode, 1);
  assert.match(failed.run.error, /^SHA mismatch/, "stderr is in the same ordered stream");
  setState({ fetchFail: true });
  reset();
  await lib.startPolyforgeInstall({});
  const unfetched = await runToEnd();
  assert.equal(unfetched.run.state, "failed");
  assert.match(unfetched.run.error, /could not fetch tools\/install\.sh from .*HTTP 404/);
  assert.deepEqual(installCalls(), [], "nothing ran");
});

test("a late subscriber replays the kept output after its cursor, then the end", async () => {
  setState({});
  setConfigured(true);
  reset();
  await lib.startPolyforgeInstall({});
  const whole = await runToEnd();
  const late = lib.subscribePolyforgeInstall(2, () => assert.fail("a finished run streams nothing new"));
  assert.deepEqual(late.lines.map((line) => line.n), Array.from({ length: whole.lines.length - 2 }, (_, i) => i + 3));
  assert.equal(late.run.state, "succeeded");
});
