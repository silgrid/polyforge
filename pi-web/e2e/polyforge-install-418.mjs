// carries: components/PolyforgeInstallCard*, lib/polyforge-install*, app/api/polyforge/install/*, app/api/polyforge/polyforge-install-routes*
// — Settings › Polyforge: install / update pf from the release
// channel, end to end in the browser. It NEVER runs a real installer: `gh`
// and `pf` are stubs first on PATH, and the tools/install.sh the stub `gh`
// serves is a fake that records its argv, its environment and its stdin,
// prints its steps a few hundred ms apart (so line-by-line output is
// observable) and then succeeds, refuses (exit 3, install.sh's refusal that
// PF_INSTALL_FORCE=1 lifts), fails, or holds until the suite releases it.
// Each scenario logs PASS/FAIL; the run exits 1 on any FAIL.
//
// What is covered here, per acceptance criterion: AC1 the versions and
// every disagreement on the page; AC2 one click, output line by line, the
// conclusions, a refusal with its jobs and the separately confirmed force;
// AC3 the first install's host + key, the key on stdin only and cleared on
// every submit; AC4 the copied
// command names the channel; AC5 the in-route gate, one run at a time, and a
// run that outlives its page and is seen again on reopening. The login half
// of AC5 is the proxy's (`/api/:path*`, pinned in
// app/api/polyforge/polyforge-install-routes.test.mjs): this suite's server
// runs without a password because its readiness probe is unauthenticated.
//
//   node e2e/polyforge-install-418.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createConnection } from "node:net";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, reportTitled, root, writeFileAtomic, writeHostSession } from "./harness.mjs";

assertNoDevServer();
// The channel the page must install from: Pi Web's own shipped marker (read
// here, so the suite holds in any projection of this tree).
const CHANNEL = JSON.parse(readFileSync(join(root, "bin", "pi-web-release-channel.json"), "utf8")).repo;
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-install-418-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-install-418";
for (const dir of [join(agentDir, "sessions", "e2e"), join(home, ".config", "polyforge"), project]) mkdirSync(dir, { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E pf install host session", timestamp: "2026-11-09T00:00:00.000Z" });
const cliToml = join(home, ".config", "polyforge", "cli.toml");

// ---- stubs: state.json drives pf, gh and the fake install.sh ----
// named without a credential keyword: the fixture has no secret-scan shape
const SENTINEL = "pf_k1_E2E418STDINONLYEXAMPLE";
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const recordLog = join(work, "install-calls.log");
const releaseFile = join(work, "release-hold");
const servedScript = join(work, "install.sh.served");
// A patch merges into the file as it is NOW, never into a copy held here:
// the fake install.sh writes state.json too (pf present at the channel's tag
// once it "installs"), and a stale copy would undo that write.
const setState = (patch) => {
  const current = existsSync(stateFile) ? JSON.parse(readFileSync(stateFile, "utf8")) : {};
  writeFileAtomic(stateFile, JSON.stringify({ ...current, ...patch }));
};
setState({ pfAbsent: true, pf: "v0.2.5", daemon: "v0.2.5", tag: "v0.2.6", jobs: [], script: "ok", fetchFail: false });
const records = () => existsSync(recordLog) ? readFileSync(recordLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)) : [];

writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename)); const args = process.argv.slice(2);
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
if (s.pfAbsent) { process.stderr.write("pf: not installed\\n"); process.exit(127); }
if (args.includes("--version")) { process.stdout.write(s.pf + "\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
if (rest[0] === "version") { process.stdout.write(JSON.stringify({ version: s.pf, capabilities: [] }) + "\\n"); process.exit(0); }
if (rest[0] === "ops" && rest[1] === "status") {
  process.stdout.write(JSON.stringify({ machine_id: "m-e2e-418", version: s.daemon, jobs: s.jobs, queue: { pending: 0, running: s.jobs.length, max_concurrent: 4 } }) + "\\n");
  process.exit(0);
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
writeFileSync(join(stubDir, "gh"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename)); const args = process.argv.slice(2);
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
if (args[0] === "release" && args[1] === "list") { process.stdout.write(s.tag + "\\n"); process.exit(0); }
if (args[0] === "api") {
  if (s.fetchFail) { process.stderr.write("HTTP 404: Not Found (e2e)\\n"); process.exit(1); }
  process.stdout.write(fs.readFileSync(${JSON.stringify(servedScript)}, "utf8")); process.exit(0);
}
process.exit(2);
`);
// The FAKE install.sh. It records {argv, stdin, force, repo, envHasKey},
// echoes the stdin key back (the page must show it masked), and on success
// "installs": pf becomes present at the channel's tag and cli.toml exists.
const node = JSON.stringify(process.execPath);
writeFileSync(servedScript, `#!/bin/sh
KEY=""
[ "$1" = - ] && IFS= read -r KEY
${node} -e 'const fs=require("fs");fs.appendFileSync(process.argv[1], JSON.stringify({argv: process.argv[2], stdin: process.argv[3], force: process.env.PF_INSTALL_FORCE || "", repo: process.env.PF_INSTALL_REPO || "", envHasKey: !!process.argv[3] && Object.values(process.env).some((v) => v.includes(process.argv[3]))}) + "\\n")' ${JSON.stringify(recordLog)} "$0 $*" "$KEY"
mode=$(${node} -e 'process.stdout.write(JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).script)' ${JSON.stringify(stateFile)})
echo "== 1/5 prepare (pf binary + release checkout; nothing is changed yet)"
echo "   release channel: $PF_INSTALL_REPO"
[ -n "$KEY" ] && echo "   key read from stdin: $KEY"
sleep 0.4
echo "   pf downloaded, SHA-verified, staged"
sleep 0.4
case "$mode" in
  refuse)
    if [ "\${PF_INSTALL_FORCE:-}" != 1 ]; then
      echo "== 2/5 switch"
      echo "ERROR: the daemon is running jobs; upgrading needs a graceful shutdown, which terminates their agent trees. Upgrade refused; nothing on this machine was changed (e2e)."
      echo "     demo#7 (running)"
      echo "   proceed by one of: wait until they finish (pf ops status), pause them (pf pause <wi>), or re-run with PF_INSTALL_FORCE=1"
      exit 3
    fi
    echo "   PF_INSTALL_FORCE=1: shutting down despite running jobs:"
    echo "     demo#7 (running)" ;;
  fail)
    echo "ERROR: SHA mismatch on pf-linux-amd64 (e2e); nothing on this machine was changed" >&2
    exit 1 ;;
  hold)
    n=0
    while [ ! -f ${JSON.stringify(releaseFile)} ] && [ $n -lt 600 ]; do sleep 0.2; n=$((n+1)); done ;;
esac
echo "== 2/5 switch"
sleep 0.4
echo "== 5/5 verify"
${node} -e 'const fs=require("fs");const f=process.argv[1];const s=JSON.parse(fs.readFileSync(f,"utf8"));s.pfAbsent=false;s.pf=s.tag;fs.writeFileSync(f+".t",JSON.stringify(s));fs.renameSync(f+".t",f)' ${JSON.stringify(stateFile)}
[ -f "$HOME/.config/polyforge/cli.toml" ] || printf 'server = "http://e2e:8080"\\napi_key = "fake"\\n' > "$HOME/.config/polyforge/cli.toml"
echo "Done."
`);
chmodSync(join(stubDir, "pf"), 0o755);
chmodSync(join(stubDir, "gh"), 0o755);

// ---- scenarios ----
const T = {
  E1: "E1 (AC3/AC6): a first install asks for the host and the key; the key reaches install.sh on stdin only, never argv, env, page or log",
  E2: "E2 (AC1): installed, latest, the running daemon, jobs and Pi Web are shown; a daemon on the old binary and Pi Web out of sync are named",
  E3: "E3 (AC2): one click updates from the channel, the output arrives line by line, and a success is concluded",
  E4: "E4 (AC5): closing the page does not stop a run; one run at a time; the reopened page shows its progress and result",
  E5: "E5 (AC2/AC6): with jobs running install.sh's refusal and jobs are shown; a force takes a separate confirmation and runs with PF_INSTALL_FORCE=1",
  E6: "E6 (AC2/AC6): a failing install, and a failed fetch of install.sh, conclude failed with the reason",
  E7: "E7 (AC4): the copyable command installs from the internal channel with PF_INSTALL_REPO",
  E8: "E8 (AC5): the in-route gate refuses a foreign Host; no 5xx or console error on the page",
};
const failures = new Map(Object.values(T).map((title) => [title, []]));
const ran = new Set();
const check = createCheck({ failures: ([title, label], text) => failures.get(title).push(`${label}: ${text}`), before: ([title]) => ran.add(title), pass: null, fail: null });

let base;
const serverLog = join(work, "server.log");
let coldSettings = true;
async function get(path) {
  const response = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(30_000) });
  const text = await response.text();
  let json = null; try { json = JSON.parse(text); } catch {}
  return { status: response.status, text, json };
}
function rawRequest(path, host) {
  return new Promise((resolve, reject) => {
    const socket = createConnection({ port: Number(new URL(base).port), host: "127.0.0.1" }, () => {
      socket.write(`GET ${path} HTTP/1.1\r\nHost: ${host}\r\nAccept: */*\r\nConnection: close\r\n\r\n`);
    });
    const chunks = [];
    socket.on("data", (chunk) => chunks.push(chunk));
    socket.on("error", reject);
    socket.on("close", () => resolve({ status: Number(/^HTTP\/[\d.]+ (\d{3})/.exec(Buffer.concat(chunks).toString("utf8"))?.[1] ?? 0) }));
  });
}
const h = createHarness({
  viewport: { width: 1280, height: 900 },
  permissions: ["clipboard-read", "clipboard-write"],
  timeout: 30_000,
  coldFirst: true,
  errors: ["pageerror", "console", "5xx"],
  session: SESSION,
  ready: "E2E pf install host session",
});
const { errors, openPage } = h;
/** Settings › Polyforge, Host pane; answers the install card. */
async function openCard(page) {
  const timeout = coldSettings ? COLD_MS : NAV_MS;
  coldSettings = false;
  const show = page.getByRole("button", { name: "Show sidebar", exact: true });
  if (await show.isVisible().catch(() => false)) await show.click();
  await page.waitForFunction(() => {
    const button = [...document.querySelectorAll("button")]
      .find((candidate) => candidate.getAttribute("aria-label") === "Settings" || candidate.textContent.trim() === "Settings");
    return !!button && Object.keys(button).some((key) => key.startsWith("__reactFiber$") || key.startsWith("__reactProps$"));
  }, null, { timeout });
  await page.getByRole("button", { name: "Settings", exact: true }).first().click({ timeout });
  const dialog = page.getByRole("dialog", { name: "Settings", exact: true });
  await dialog.waitFor({ timeout });
  await dialog.getByRole("button", { name: "Polyforge", exact: true }).click();
  const card = dialog.locator("[data-pf-install]");
  await card.waitFor({ timeout });
  await card.locator("[data-pf-install-start]").waitFor({ timeout });
  return { dialog, card };
}
const conclusion = (card, state) => card.locator(`[data-pf-install-conclusion="${state}"]`);
const outputText = async (card) => (await card.locator("[data-pf-install-output]").innerText().catch(() => "")) ?? "";
const releaseHold = () => writeFileSync(releaseFile, "go\n");

try {
  await h.launchBrowser(() => chromium.launch());
  ({ base } = await h.startServer({
    name: "polyforge-install-418",
    logFile: serverLog,
    env: {
      PATH: `${stubDir}${delimiter}${process.env.PATH}`,
      HOME: home,
      PI_CODING_AGENT_DIR: agentDir,
      PI_WEB_ALLOWED_ROOT_PREFIXES: work,
      PF_BIN: join(stubDir, "pf"),
      // inherited overrides a run must never pick up
      PF_INSTALL_FORCE: "1",
      PF_HOST: "inherited.invalid",
    },
  }));

  // ---- E1: first install (pf absent, no cli.toml) ----
  await check(T.E1, "first install", async () => {
    const page = await openPage();
    try {
      const { card } = await openCard(page);
      assert.ok(!existsSync(cliToml), "the machine starts without a config");
      const start = card.locator("[data-pf-install-start]");
      assert.equal((await start.innerText()).trim(), "Install pf");
      await card.locator("[data-pf-install-credentials]").waitFor();
      const keyInput = card.getByLabel("API key");
      assert.equal(await keyInput.getAttribute("type"), "password");
      await keyInput.fill(SENTINEL);
      await start.click();
      await card.getByText("Enter the aihub host.").waitFor();
      assert.deepEqual(records(), [], "an incomplete form runs nothing");
      assert.equal(await keyInput.inputValue(), "", "the key leaves the form on every submit, a refused check included");
      await card.getByLabel("aihub host").fill("e2e-aihub.example");
      await keyInput.fill(SENTINEL);
      await start.click();
      await conclusion(card, "succeeded").waitFor({ timeout: 60_000 });
      const [call] = records();
      assert.equal(call.stdin, SENTINEL, "the key reached install.sh on stdin");
      assert.match(call.argv, / - e2e-aihub\.example$/, `argv is \`- <host>\` only: ${call.argv}`);
      assert.ok(!call.argv.includes(SENTINEL), "the key is never on argv");
      assert.equal(call.envHasKey, false, "the key is never in install.sh's environment");
      assert.equal(call.repo, CHANNEL, "the run is pinned to the channel");
      assert.equal(call.force, "", "an inherited PF_INSTALL_FORCE never forces a run");
      const shown = await outputText(card);
      assert.ok(shown.includes("key read from stdin: ***"), "a script printing the key shows it masked");
      assert.ok(!(await page.content()).includes(SENTINEL), "the key is nowhere in the page");
      const api = await get("/api/polyforge/install?force=1");
      assert.ok(!api.text.includes(SENTINEL), "the key is never in a response");
      assert.ok(existsSync(cliToml), "the fake installer wrote the config");
      assert.equal(api.json.versions.needsConfig, false, "after the install the config exists");
      assert.equal(api.json.versions.installed, "v0.2.6");
    } finally { await page.context().close(); }
    assert.ok(!readFileSync(serverLog, "utf8").includes(SENTINEL), "the key is never in the server log");
  });

  // ---- E2: versions after the install (daemon still the old process) ----
  await check(T.E2, "versions", async () => {
    setState({ daemon: "v0.2.5", tag: "v0.2.7", jobs: [{ work_item_id: "wi_e2e7", slug: "demo#7", status: "running" }, { slug: "demo#8", status: "paused" }] });
    const page = await openPage();
    try {
      const { dialog, card } = await openCard(page);
      // 重新检测 re-reads past the latest-release cache
      await dialog.getByRole("button", { name: "Re-detect", exact: true }).click();
      await card.locator('[data-pf-install-version="latest"]', { hasText: "v0.2.7" }).waitFor();
      assert.equal((await card.locator('[data-pf-install-version="installed"]').innerText()).trim(), "v0.2.6");
      assert.equal((await card.locator('[data-pf-install-version="running"]').innerText()).trim(), "v0.2.5");
      assert.equal(await card.locator("[data-pf-install-jobs]").getAttribute("data-pf-install-jobs"), "1", "paused jobs do not block an upgrade");
      assert.ok((await card.innerText()).includes("demo#7 (running)"));
      const piWeb = (await card.locator('[data-pf-install-version="pi-web"]').innerText()).trim();
      assert.ok(piWeb.length > 0, "Pi Web's own version is shown");
      for (const flag of ["update", "daemon-stale", "jobs"]) await card.locator(`[data-pf-install-flag="${flag}"]`).waitFor();
      assert.match(await card.locator('[data-pf-install-flag="daemon-stale"]').innerText(), /still v0\.2\.5 while pf v0\.2\.6 is installed/);
      if (piWeb.replace(/^v/, "") !== "0.2.6") await card.locator('[data-pf-install-flag="pi-web"]').waitFor();
      assert.equal((await card.locator("[data-pf-install-start]").innerText()).trim(), "Update pf");
      assert.equal(await card.locator("[data-pf-install-credentials]").count(), 0, "an update asks for no key");
    } finally { await page.context().close(); }
    setState({ jobs: [], daemon: "v0.2.6", tag: "v0.2.6" });
  });

  // ---- E3: update, output line by line ----
  // The fake holds after its first lines until released, so "the early lines
  // are on the page while the run still runs" never depends on a sleep.
  await check(T.E3, "update", async () => {
    setState({ script: "hold" });
    rmSync(releaseFile, { force: true });
    rmSync(recordLog, { force: true });
    const page = await openPage();
    try {
      const { card } = await openCard(page);
      await card.locator("[data-pf-install-start]").click();
      await conclusion(card, "running").waitFor();
      await card.locator("[data-pf-install-output]", { hasText: "pf downloaded" }).waitFor();
      const early = (await outputText(card)).split("\n").length;
      assert.ok(!(await outputText(card)).includes("Done."), "the first lines arrive before the run ends");
      assert.equal(await conclusion(card, "running").count(), 1, "still running while held");
      releaseHold();
      await conclusion(card, "succeeded").waitFor({ timeout: 60_000 });
      const late = (await outputText(card)).split("\n").length;
      assert.ok(late > early, `the output grew line by line (${early} → ${late})`);
      assert.ok((await card.innerText()).includes("Succeeded: install.sh finished."));
      const [call] = records();
      assert.match(call.argv, / -$/, "an update passes `-` and no host");
      assert.equal(call.stdin, "", "an update writes no key");
    } finally { await page.context().close(); }
  });

  // ---- E4: the run outlives its page; one at a time; reopening shows it ----
  await check(T.E4, "close and reopen", async () => {
    setState({ script: "hold" });
    rmSync(releaseFile, { force: true });
    const first = await openPage();
    let card;
    try {
      ({ card } = await openCard(first));
      // FORCING (slow start answer): the start request leaves only once the
      // previous run's replayed output is on this page, and 1 s after that
      // (a slow server, never a wait an assertion depends on).
      await first.route("**/api/polyforge/install", async (route) => {
        if (route.request().method() !== "POST") return route.fallback();
        await first.locator("[data-pf-install-output]", { hasText: "pf downloaded" }).waitFor().catch(() => {});
        await delay(1000);
        await route.continue().catch(() => {});
      });
      await card.locator("[data-pf-install-start]").click();
      // the new run, not the previous run's replay: only the new run shows running
      await conclusion(card, "running").waitFor();
      await card.locator("[data-pf-install-output]", { hasText: "pf downloaded" }).waitFor();
    } finally { await first.context().close(); }
    await delay(1500);
    const during = await get("/api/polyforge/install");
    assert.equal(during.json.run.state, "running", "closing the page did not stop the run");
    const second = await openPage();
    try {
      ({ card } = await openCard(second));
      await conclusion(card, "running").waitFor();
      await card.locator("[data-pf-install-output]", { hasText: "pf downloaded" }).waitFor();
      assert.equal(await card.locator("[data-pf-install-start]").isDisabled(), true, "the button waits while a run runs");
      // from node, not the page: a browser logs a 4xx fetch as a console error
      const again = await fetch(`${base}/api/polyforge/install`, { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}", signal: AbortSignal.timeout(30_000) });
      assert.equal(again.status, 409, "a second install is refused while one runs");
      assert.match((await again.json()).error, /already running/);
      releaseHold();
      await conclusion(card, "succeeded").waitFor({ timeout: 60_000 });
      assert.ok((await outputText(card)).includes("Done."), "the reopened page followed the run to its end");
    } finally { await second.context().close(); }
    const third = await openPage();
    try {
      // FORCING (slow replay stream): the replay request leaves only once the
      // run's conclusion is on this page, and 1 s after that (a slow server,
      // never a wait an assertion depends on).
      await third.route("**/api/polyforge/install/events**", async (route) => {
        await third.locator('[data-pf-install-conclusion="succeeded"]').waitFor().catch(() => {});
        await delay(1000);
        await route.continue().catch(() => {});
      });
      ({ card } = await openCard(third));
      await conclusion(card, "succeeded").waitFor();
      // the conclusion comes from the run read, the lines from the replay stream
      await card.locator("[data-pf-install-output]", { hasText: "== 1/5 prepare" }).waitFor();
    } finally { await third.context().close(); }
  });

  // ---- E5: refused while jobs run; a separately confirmed force ----
  await check(T.E5, "refusal and force", async () => {
    setState({ script: "refuse", jobs: [{ work_item_id: "wi_e2e7", slug: "demo#7", status: "running" }] });
    rmSync(recordLog, { force: true });
    const page = await openPage();
    try {
      const { card } = await openCard(page);
      await card.locator("[data-pf-install-start]").click();
      await conclusion(card, "refused").waitFor({ timeout: 60_000 });
      const refusal = card.locator("[data-pf-install-refusal]");
      assert.match(await conclusion(card, "refused").innerText(), /Refused by install\.sh: the daemon is running jobs;.*Upgrade refused/);
      assert.ok((await refusal.innerText()).includes("demo#7 (running)"), "the refusal names the running jobs");
      assert.equal(records()[0].force, "", "the update was not forced");
      // the jobs change after the refusal: the confirmation shows the ones running now
      setState({ jobs: [{ work_item_id: "wi_e2e12", slug: "demo#12", status: "running" }] });
      await card.locator("[data-pf-install-force]").click();
      const confirm = card.getByRole("alertdialog", { name: "Force the update (PF_INSTALL_FORCE=1)?" });
      await confirm.waitFor();
      assert.match(await confirm.innerText(), /agent tree of every running job is terminated/, "the confirmation states the cost");
      await confirm.getByText("demo#12 (running)").waitFor();
      assert.ok(!(await confirm.innerText()).includes("demo#7"), "the confirmation lists the jobs running now, not the refusal's");
      await confirm.getByRole("button", { name: "Cancel", exact: true }).click();
      assert.equal(await card.locator("[data-pf-install-force-confirm]").count(), 0);
      assert.equal(records().length, 1, "cancel runs nothing");
      await card.locator("[data-pf-install-force]").click();
      await card.locator("[data-pf-install-force-go]").click();
      await conclusion(card, "succeeded").waitFor({ timeout: 60_000 });
      assert.equal(records()[1].force, "1", "the confirmed force reaches install.sh as PF_INSTALL_FORCE=1");
      assert.ok((await outputText(card)).includes("PF_INSTALL_FORCE=1: shutting down despite running jobs"));
    } finally { await page.context().close(); }
    setState({ script: "ok", jobs: [] });
  });

  // ---- E6: failures ----
  await check(T.E6, "failures", async () => {
    setState({ script: "fail" });
    const page = await openPage();
    try {
      const { card } = await openCard(page);
      await card.locator("[data-pf-install-start]").click();
      await conclusion(card, "failed").waitFor({ timeout: 60_000 });
      assert.match(await conclusion(card, "failed").innerText(), /Failed: SHA mismatch on pf-linux-amd64/);
      assert.equal(await card.locator("[data-pf-install-force]").count(), 0, "a failure offers no force");
      setState({ script: "ok", fetchFail: true });
      await card.locator("[data-pf-install-start]").click();
      await conclusion(card, "failed").waitFor({ timeout: 60_000 });
      await card.getByText(/could not fetch tools\/install\.sh from .*HTTP 404/).first().waitFor();
    } finally { await page.context().close(); }
    setState({ fetchFail: false });
  });

  // ---- E7: the copyable command names the internal channel ----
  await check(T.E7, "copy command", async () => {
    const page = await openPage();
    try {
      const { dialog } = await openCard(page);
      await dialog.getByRole("button", { name: "Copy install command", exact: true }).first().click();
      const copied = String(await page.evaluate(() => navigator.clipboard.readText()));
      assert.ok(copied.includes(`https://raw.githubusercontent.com/${CHANNEL}/main/tools/install.sh`), copied);
      assert.ok(copied.includes(`PF_INSTALL_REPO=${CHANNEL} bash -s --`), copied);
      assert.ok(copied.includes("<api-key>"), "the key stays a placeholder");
    } finally { await page.context().close(); }
  });

  // ---- E8: gate; no 5xx / console errors ----
  await check(T.E8, "gate", async () => {
    for (const path of ["/api/polyforge/install", "/api/polyforge/install/events"]) {
      const response = await rawRequest(path, "e2e-evil.example.com");
      assert.equal(response.status, 403, `${path} answered ${response.status} to a foreign Host`);
    }
    assert.deepEqual(errors.splice(0), []);
  });
} catch (error) {
  console.error(error);
  process.exitCode = 1;
} finally {
  releaseHold();
  await h.close().catch(() => {});
  reportTitled(failures, ran);
  if (process.exitCode) console.log(`Artifacts kept: ${work}`); else rmSync(work, { recursive: true, force: true });
}
