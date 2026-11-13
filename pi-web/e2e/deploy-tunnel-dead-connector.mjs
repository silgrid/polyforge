// carries: bin/pi-web-deploy.js
// pi#118 repro e2e: quick_tunnel_alive's bare `pgrep -f` identity
// false-positives on the session-group tmux server's own argv.
//
// Claim under test (2026-10-19 incident): the piweb session-group tmux
// server permanently carries `cloudflared tunnel --url http://127.0.0.1:
// <port>` in its own cmdline — the server is spawned by the deploy's
// tunnel-window creation (`tmux new-session -d -s <session> -n tunnel
// "cloudflared tunnel --url ..."`) whenever the deploy runs with no tmux
// server yet on its socket (e.g. after a machine/container restart).
// quick_tunnel_alive's identity is a bare
// `pgrep -f "cloudflared tunnel --url http://127.0.0.1:<port>([^0-9]|$)"`,
// so it matches the tmux server even when the connector process is dead.
// A redeploy into that state (connector died between deploys) then "reuses"
// the dead tunnel: it recovers the dead connector's URL from the stale log,
// starts no connector, and ships an unreachable quick URL with no tunnel
// window.
//
// Acceptance (per the work item): a deploy into the dead-connector state
// must not treat a non-connector cmdline match as a live connector — a NEW
// connector must be started (new PID, alive), a fresh URL shipped, and the
// tunnel window recreated.
//
// Like the other deploy e2e tests this runs fully offline — no real
// cloudflared, tailscale, npm global install, or network access. PATH stubs
// stand in for the external tools (cloudflared prints one quick-tunnel URL
// unique to its process and stays in the foreground holding its original
// argv; pi-web holds the restart-loop window, looping with its script path in
// its argv and its PID in one absolute file; npm's `install -g` lays a scratch
// global prefix from a template (e2e/lane-stubs.mjs writePiwebPrefix: an
// installed pi-web whose install scripts ran, its entry a copy of the pi-web
// stub) and `root -g` names it, so the deploy's install check and served ==
// installed check run against it).
//
// One addition over e2e/deploy-tunnel.mjs: every tmux call — the deploy's
// and this harness's — runs with TMUX_TMPDIR pointed at a scratch directory
// and without TMUX and TMUX_PANE,
// so the deploy's tunnel-window creation spawns its own tmux server on a
// scratch socket. That is exactly the incident precondition: the
// session-group server itself carries the tunnel cmdline in its argv. (On a
// machine whose tmux server predates the deploy, the session joins the
// existing server and the false positive does not appear — which is why
// e2e/deploy-tunnel.mjs, run against the shared server, cannot hit this.)
// The scratch socket also guarantees the live "piweb" session on this
// machine is never touched (lesson from the pi#38 verification report:
// deploy tests must not restart production; scratch session name, scratch
// HOME, and scratch port as in the other deploy e2e tests).
//
// This test FAILS at the HEAD it was authored against (b1f3761: deploy #2
// "reuses" the dead tunnel and ships its stale URL); it passes once
// quick_tunnel_alive verifies that a matched process's comm is cloudflared.
//
// Run: node e2e/deploy-tunnel-dead-connector.mjs   (from the repository root)
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { once } from "node:events";
import { fileURLToPath } from "node:url";
import { writePiwebPrefix } from "./lane-stubs.mjs";
import { eventually } from "./harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const deployScript = join(root, "deploy-piweb.sh");

assert.ok(process.platform === "linux" || process.platform === "darwin", "deploy-piweb.sh targets Linux/macOS");
{
  const tmux = spawnSync("tmux", ["-V"], { encoding: "utf8" });
  assert.equal(tmux.status, 0, "tmux must be installed for the deploy e2e");
}

const scratch = mkdtempSync(join(tmpdir(), "piweb-dead-conn-"));
const stubbin = join(scratch, "bin");
const sockDir = join(scratch, "tmux"); // scratch tmux socket dir (TMUX_TMPDIR)
mkdirSync(stubbin);
mkdirSync(sockDir);
const session = `piweb-dead-${process.pid}`;
const npmPrefix = join(scratch, "npm-prefix");
const npmPrefixTemplate = join(scratch, "npm-prefix-template");

// A scratch port so this repro never collides with the live deployment
// (real one serves 127.0.0.1:8142 on this machine) and so the pgrep patterns
// below only ever match this test's own processes.
const probe = createServer();
probe.listen(0, "127.0.0.1");
await once(probe, "listening");
const localPort = probe.address().port;
await new Promise((resolve, reject) => probe.close((error) => (error ? reject(error) : resolve())));

// The deploy's exact quick-tunnel identity pattern (quick_tunnel_alive):
// a bare pgrep -f against the full command line.
const tunnelPattern = `cloudflared tunnel --url http://127\\.0\\.0\\.1:${localPort}([^0-9]|$)`;

// ---------------------------------------------------------------------------
// Offline tool stubs (see header comment).
// ---------------------------------------------------------------------------
const cloudflaredStub = [
  "#!/bin/sh",
  "# Stub cloudflared quick-tunnel connector (pi#118 dead-connector e2e):",
  "# - records the connector PID next to this script;",
  "# - prints exactly one quick-tunnel URL unique to this process, embedding",
  "#   its PID (the real connector mints a fresh trycloudflare.com URL per",
  "#   run) — so a stale-log reuse is distinguishable from a fresh capture;",
  "# - stays in the foreground holding its original argv",
  "#   (cloudflared tunnel --url ...), like the real binary — the deploy",
  "#   script's pgrep -f identity must be able to see it.",
  'dir="$(cd "$(dirname "$0")" && pwd)"',
  `echo $$ >>"$dir/cloudflared.pid"`,
  'echo "INF stub connector: your quick tunnel is ready!"',
  'echo "INF stub connector: https://stub-$$-deadconn.trycloudflare.com"',
  "while :; do sleep 1; done",
  "",
].join("\n");

const piwebStub = [
  "#!/bin/sh",
  "# Stub pi-web server (pi#118 dead-connector e2e): records its PID and",
  "# holds the restart-loop tmux window open without binding any port. The PID",
  "# file is absolute and the stub loops instead of exec-ing sleep, so the",
  "# installed copy records into the same file and keeps its script path in",
  "# its argv.",
  `echo $$ >>'${join(stubbin, "piweb.pid")}'`,
  "while :; do sleep 1; done",
  "",
].join("\n");

const npmStub = [
  "#!/bin/sh",
  "# Stub npm (pi#118 dead-connector e2e): `ls -g <pkg>` -> not installed,",
  "# `root -g` -> the scratch prefix, `install -g <pkg>` -> the prefix template",
  "# copied there; the real global prefix is never touched.",
  'if [ "$1" = "ls" ]; then exit 1; fi',
  `if [ "$1" = "root" ]; then printf '%s\\n' '${npmPrefix}/lib/node_modules'; exit 0; fi`,
  `if [ "$1" = "install" ]; then rm -rf '${npmPrefix}' && mkdir -p '${npmPrefix}' && cp -R '${npmPrefixTemplate}/.' '${npmPrefix}/'; exit $?; fi`,
  "exit 0",
  "",
].join("\n");

for (const [name, body] of [
  ["cloudflared", cloudflaredStub],
  ["pi-web", piwebStub],
  ["npm", npmStub],
]) {
  writeFileSync(join(stubbin, name), body);
  chmodSync(join(stubbin, name), 0o755);
}
writePiwebPrefix(npmPrefixTemplate, { version: "0.1.1", entry: join(stubbin, "pi-web") });

const lastPid = (name) => {
  const file = join(stubbin, `${name}.pid`);
  const lines = readFileSync(file, "utf8").trim().split("\n");
  return Number(lines.at(-1));
};

const pidList = (name) => {
  const file = join(stubbin, `${name}.pid`);
  try {
    return readFileSync(file, "utf8").trim().split("\n").filter(Boolean).map(Number);
  } catch {
    return [];
  }
};

// tmux against the scratch socket (TMUX_TMPDIR): the harness must resolve
// the same server the scratch deploys do.
const tmuxSync = (args) => {
  const env = { ...process.env, TMUX_TMPDIR: sockDir };
  // TMUX overrides TMUX_TMPDIR. Inheriting an operator's parent session
  // made the cleanup below kill that server. Select our socket explicitly.
  delete env.TMUX;
  delete env.TMUX_PANE;
  const socket = join(sockDir, `tmux-${process.getuid()}`, "default");
  return spawnSync("tmux", ["-S", socket, ...args], { encoding: "utf8", env });
};

const windowsOf = (sess) =>
  (tmuxSync(["list-windows", "-t", sess, "-F", "#{window_name}"]).stdout || "")
    .split("\n")
    .filter(Boolean);

// PIDs whose full command line matches an ERE (pgrep -f; pgrep excludes
// itself, and no harness process carries these patterns).
const pgrepF = (pattern) =>
  spawnSync("pgrep", ["-f", pattern], { encoding: "utf8" }).stdout.split("\n").filter(Boolean).map(Number);

const commOf = (pid) => spawnSync("ps", ["-p", String(pid), "-o", "comm="], { encoding: "utf8" }).stdout.trim();
const argsOf = (pid) => spawnSync("ps", ["-p", String(pid), "-o", "args="], { encoding: "utf8" }).stdout.trim();

const alive = (pid) => spawnSync("kill", ["-0", String(pid)], { encoding: "utf8" }).status === 0;

// A killed connector is gone once kill -0 fails, it is an unreaped zombie
// (ps stat Z), or its PID now names a process that is not cloudflared (PID
// reuse): a bare kill -0 right after the kill can still succeed in the
// first two cases.
const statOf = (pid) => spawnSync("ps", ["-p", String(pid), "-o", "stat="], { encoding: "utf8" }).stdout.trim();
const connectorGone = (pid) => !alive(pid) || statOf(pid).startsWith("Z") || commOf(pid) !== "cloudflared";

// ---------------------------------------------------------------------------
// One deploy run. Returns { status, stdout, url } (url: the reported
// quick-tunnel URL — the dead connector's stale one under the buggy reuse).
// ---------------------------------------------------------------------------
const deploy = () => {
  const env = {
    ...process.env,
    PATH: `${stubbin}:${process.env.PATH}`,
    TMPDIR: scratch,
    HOME: scratch, // keep the stable ~/.piweb quick-tunnel log out of the real home
    TMUX_TMPDIR: sockDir, // scratch socket: the deploy spawns its own session-group server
    PIWEB_EXPOSE: "cloudflare",
    PIWEB_TUNNEL: "quick",
    PIWEB_TMUX_SESSION: session,
    PIWEB_PORT: String(localPort),
    PIWEB_HOST: "127.0.0.1",
    PIWEB_CF_CONFIG: "",
    PIWEB_CF_HOSTNAME: "",
  };
  delete env.TMUX;
  delete env.TMUX_PANE;
  const result = spawnSync("bash", [deployScript], { cwd: root, encoding: "utf8", timeout: 120_000, env });
  assert.equal(result.status, 0, `deploy-piweb.sh exited ${result.status}\nstdout:\n${result.stdout}\nstderr:\n${result.stderr}`);
  const url = result.stdout.match(/Quick tunnel up: (https:\/\/[a-z0-9-]+\.trycloudflare\.com)/)?.[1];
  assert.ok(url, `deploy output must report the quick-tunnel URL\nstdout:\n${result.stdout}`);
  return { stdout: result.stdout, url };
};

try {
  // --- deploy #1: fresh socket — the tunnel-window creation spawns the
  // session-group tmux server, exactly like the incident machine ----------
  const first = deploy();
  const connector1 = lastPid("cloudflared");
  assert.ok(alive(connector1), `tunnel connector PID ${connector1} must be alive after deploy #1`);
  assert.equal(commOf(connector1), "cloudflared", `tunnel connector PID ${connector1} must be the cloudflared stub after deploy #1`);
  assert.equal(
    first.url,
    `https://stub-${connector1}-deadconn.trycloudflare.com`,
    "deploy #1's URL must be its own connector's (the stub's URL embeds its PID)",
  );
  assert.ok(windowsOf(session).includes("tunnel"), "the scratch session must have a 'tunnel' window after deploy #1");

  // Incident precondition: the session-group tmux server was spawned by the
  // tunnel-window creation, so its own argv permanently carries the tunnel
  // cmdline (this is what a bare pgrep -f matches later).
  const servers1 = pgrepF(`tmux new-session -d -s ${session} -n tunnel`);
  assert.ok(servers1.length >= 1, "the scratch deploy must have spawned its own tmux server (TMUX_TMPDIR isolation)");
  assert.ok(
    servers1.some((pid) => argsOf(pid).includes(`cloudflared tunnel --url http://127.0.0.1:${localPort}`)),
    `the session-group tmux server's own argv must carry the tunnel cmdline; server argv: ${servers1.map(argsOf).join(" | ")}`,
  );

  // --- the connector dies between deploys (2026-10-19 incident state) ----
  spawnSync("kill", [String(connector1)], { encoding: "utf8" });
  const windowGone = await eventually(() => !windowsOf(session).includes("tunnel"), 10_000);
  assert.ok(windowGone, "the tunnel window must close when its connector process dies");
  const connectorDead = await eventually(() => connectorGone(connector1), 10_000);
  assert.ok(
    connectorDead,
    `the connector PID ${connector1} must be dead (stat '${statOf(connector1)}', comm '${commOf(connector1)}')`,
  );
  assert.ok(windowsOf(session).includes("piweb"), "the session must survive the connector death (piweb loop window)");

  // The reported mechanism, stated at the identity level: in this state the
  // deploy's exact quick_tunnel_alive pattern still matches — but no matched
  // process is a cloudflared connector; the match is the tmux server's own
  // argv. A bare -f match here is a false positive by construction.
  const matched = pgrepF(tunnelPattern);
  assert.ok(
    matched.length >= 1,
    "quick_tunnel_alive's bare pgrep -f pattern must still match something in the dead-connector state",
  );
  assert.ok(
    matched.every((pid) => commOf(pid) !== "cloudflared"),
    `no matched process may be a cloudflared connector here; matched: ${matched
      .map((pid) => `${pid} (comm '${commOf(pid)}')`)
      .join(", ")}`,
  );
  assert.ok(
    matched.some((pid) =>
      argsOf(pid).includes(`tmux new-session -d -s ${session} -n tunnel cloudflared tunnel --url http://127.0.0.1:${localPort}`),
    ),
    "the false-positive match must be the session-group tmux server carrying the tunnel cmdline in its own argv",
  );

  // --- deploy #2: same config; connector dead, tmux server alive ---------
  const pidsBefore = pidList("cloudflared");
  const second = deploy();

  // CORE ACCEPTANCE: the deploy must have detected the dead connector and
  // started a NEW one — the tmux server's argv is not a connector. (Fails at
  // the repro HEAD: the deploy "reuses" the dead tunnel — see its output in
  // the failure message.)
  const started = await eventually(() => pidList("cloudflared").some((pid) => !pidsBefore.includes(pid)), 10_000);
  const newConnector = pidList("cloudflared").filter((pid) => !pidsBefore.includes(pid)).at(-1);
  assert.ok(
    started && newConnector !== undefined && alive(newConnector),
    `pi#118: a deploy into the dead-connector state must start a NEW cloudflared connector (the tmux server's argv is not one); observed: no connector process was started — deploy #2 output:\n${second.stdout}`,
  );

  assert.ok(
    windowsOf(session).includes("tunnel"),
    `pi#118: the session must have a 'tunnel' window again after the dead connector was replaced; windows: ${windowsOf(session).join(", ")}`,
  );
  assert.notEqual(
    second.url,
    first.url,
    `pi#118: the deploy must ship the fresh connector's URL, not the dead connector's stale log URL; observed ${first.url} -> ${second.url}`,
  );
  assert.equal(
    second.url,
    `https://stub-${newConnector}-deadconn.trycloudflare.com`,
    "the shipped URL must be the new connector's (the stub's URL embeds its PID)",
  );

  console.log("PASS: a deploy into the dead-connector state replaced the dead tunnel (new connector, fresh URL, tunnel window)");
} finally {
  // Never leak the scratch session, its server, stub processes, or temp
  // files. All tmux calls stay on the scratch socket (TMUX_TMPDIR); the
  // live default-socket server is never touched.
  tmuxSync(["kill-session", "-t", session]);
  tmuxSync(["kill-server"]);
  for (const pid of [...pidList("cloudflared"), ...pidList("piweb")]) {
    spawnSync("kill", [String(pid)], { encoding: "utf8" });
  }
  rmSync(scratch, { recursive: true, force: true });
}