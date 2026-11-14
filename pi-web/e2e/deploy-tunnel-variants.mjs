// carries: deploy-piweb.sh
// pi#107 variants e2e: the tunnel-idempotence acceptance cases of the work
// item that the main repro (e2e/deploy-tunnel.mjs, quick mode) does not
// cover:
//
//   1. quick tunnel, argv-preserving connector: the work item's pgrep -f
//      identity ("cloudflared tunnel --url http://127.0.0.1:<port>") must
//      recognize a running connector through its command line and reuse it;
//   2. quick tunnel, port change: changing PIWEB_PORT between deploys is a
//      stale-forwarding identity miss — the old connector is replaced (new
//      PID, new URL, old process dead);
//   3. named tunnel, consecutive deploys: the `cloudflared tunnel --config`
//      process is reused (PID unchanged);
//   4. legacy session recycle (code review): a session pre-seeded the way
//      pre-pi#107 deploys and the README autostart recipe create it — an
//      unnamed loop window running pi-web — must converge on one deploy:
//      the old loop process dead, exactly one (named) loop window left;
//   5. tailscale serve (code review): two deploys with PIWEB_EXPOSE=tailscale
//      keep the serve pane PID unchanged; a PIWEB_PORT change replaces the
//      serve holder and leaves exactly one 'serve' window.
//
// Like the main repro this runs fully offline: PATH stubs stand in for
// cloudflared/pi-web/npm/tailscale, and every deploy targets a scratch tmux
// session name and scratch ports, so the live "piweb" session on this
// machine is never touched (lesson from the pi#38 verification report). Every
// child (deploy and harness tmux calls) runs without TMUX and TMUX_PANE. The
// npm stub's `install -g` lays a scratch global prefix from a template
// (e2e/lane-stubs.mjs writePiwebPrefix: an installed pi-web whose install
// scripts ran, its entry a copy of the pi-web stub) and `root -g` names it,
// so the deploy's install check and served == installed check run against it.
//
// The cloudflared stub keeps its original argv (`tunnel --url ...` /
// `tunnel --config ...`) by looping instead of exec-ing sleep, the way the
// real cloudflared binary holds its command line — that is what lets the
// pgrep identity see it. Every deploy also runs with HOME pointed at the
// scratch dir, so the stable quick-tunnel log (~/.piweb/...) never touches
// the real home directory (code review). The pi-web stub loops the same way, so
// the installed copy keeps its script path in its argv, and records its PID
// in one absolute file whichever copy runs.
//
// Run: node e2e/deploy-tunnel-variants.mjs   (from the repository root)
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { once } from "node:events";
import { fileURLToPath } from "node:url";
import { ghStubBody, releaseStubEnv, writePiwebPrefix } from "./lane-stubs.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const deployScript = join(root, "deploy-piweb.sh");

assert.ok(process.platform === "linux" || process.platform === "darwin", "deploy-piweb.sh targets Linux/macOS");
{
  const tmux = spawnSync("tmux", ["-V"], { encoding: "utf8" });
  assert.equal(tmux.status, 0, "tmux must be installed for the deploy e2e");
}

const scratch = mkdtempSync(join(tmpdir(), "piweb-deploy-variants-"));
const stubbin = join(scratch, "bin");
mkdirSync(stubbin);
const session = `piweb-var-${process.pid}`;
const legacySession = `${session}-legacy`; // case 4
const tsSession = `${session}-ts`; // case 5
const scratchSessions = [session, legacySession, tsSession];
const npmPrefix = join(scratch, "npm-prefix");
const npmPrefixTemplate = join(scratch, "npm-prefix-template");

// An inherited TMUX/TMUX_PANE selects the operator's tmux server; no child of
// this suite carries them.
const withoutTmux = (env) => {
  delete env.TMUX;
  delete env.TMUX_PANE;
  return env;
};
const tmuxEnv = () => withoutTmux({ ...process.env });

const freePort = async () => {
  const probe = createServer();
  probe.listen(0, "127.0.0.1");
  await once(probe, "listening");
  const port = probe.address().port;
  await new Promise((resolve, reject) => probe.close((error) => (error ? reject(error) : resolve())));
  return port;
};
const portA = await freePort(); // quick mode, stable across deploys
const portB = await freePort(); // quick mode, port change
const portC = await freePort(); // named mode (must equal the config's ingress port)
const portT1 = await freePort(); // tailscale mode, stable across deploys
const portT2 = await freePort(); // tailscale mode, port change

// ---------------------------------------------------------------------------
// Offline tool stubs (see header comment).
// ---------------------------------------------------------------------------
const cloudflaredStub = [
  "#!/bin/sh",
  "# Stub cloudflared connector (pi#107 variants e2e): records the connector",
  "# PID, prints one quick-tunnel URL unique to this process, and stays in the",
  "# foreground holding its original argv (tunnel --url/--config ...), the way",
  "# the real binary does — so the deploy script's pgrep identity sees it.",
  'dir="$(cd "$(dirname "$0")" && pwd)"',
  'echo $$ >>"$dir/cloudflared.pid"',
  'echo "INF stub connector: your quick tunnel is ready!"',
  'echo "INF stub connector: https://stub-$$-variants.trycloudflare.com"',
  "while :; do sleep 1; done",
  "",
].join("\n");

const piwebStub = [
  "#!/bin/sh",
  "# Stub pi-web server (pi#107 variants e2e): records its PID and holds the",
  "# restart-loop tmux window open without binding any port. The PID file is",
  "# absolute and the stub loops instead of exec-ing sleep, so the installed",
  "# copy records into the same file and keeps its script path in its argv.",
  `echo $$ >>'${join(stubbin, "piweb.pid")}'`,
  "while :; do sleep 1; done",
  "",
].join("\n");

const npmStub = [
  "#!/bin/sh",
  "# Stub npm (pi#107 variants e2e): `ls -g <pkg>` -> not installed, `root -g`",
  "# -> the scratch prefix, `install -g <pkg>` -> the prefix template copied",
  "# there; the real global prefix is never touched.",
  'if [ "$1" = "ls" ]; then exit 1; fi',
  `if [ "$1" = "root" ]; then printf '%s\\n' '${npmPrefix}/lib/node_modules'; exit 0; fi`,
  `if [ "$1" = "install" ]; then rm -rf '${npmPrefix}' && mkdir -p '${npmPrefix}' && cp -R '${npmPrefixTemplate}/.' '${npmPrefix}/'; exit $?; fi`,
  "exit 0",
  "",
].join("\n");

const tailscaleStub = [
  "#!/bin/sh",
  "# Stub tailscale (pi#107 variants e2e): offline stand-in covering exactly",
  "# what deploy-piweb.sh calls:",
  "# - status / status --json / dns status --json: healthy tailnet member",
  "#   whose Self.DNSName is stub-tailnet.ts.net;",
  "# - up: no-op success;",
  "# - serve status: prints the mapping the current foreground holder set",
  "#   (nothing before the first holder runs);",
  "# - serve --reset: forgets the mapping (the config, not the holder);",
  "# - serve --https=443 http://127.0.0.1:<port>: records the forwarded port",
  "#   and the holder PID, then stays in the foreground like the real serve.",
  'dir="$(cd "$(dirname "$0")" && pwd)"',
  'json=\'{"Self":{"DNSName":"stub-tailnet.ts.net."}}\'',
  'case "$1" in',
  "  status)",
  '    [ "$2" = "--json" ] && echo "$json"',
  "    exit 0",
  "    ;;",
  "  dns)",
  '    [ "$2" = "status" ] && echo "$json"',
  "    exit 0",
  "    ;;",
  "  up)",
  "    exit 0",
  "    ;;",
  "  serve)",
  '    case "$2" in',
  "      status)",
  '        if [ -s "$dir/serve.state" ]; then',
  '          port="$(cat "$dir/serve.state")"',
  '          echo "/: https 443 -> http://127.0.0.1:${port}"',
  "        fi",
  "        exit 0",
  "        ;;",
  "      --reset)",
  '        rm -f "$dir/serve.state"',
  "        exit 0",
  "        ;;",
  "      --https=443)",
  '        echo "${3##*:}" >"$dir/serve.state"',
  '        echo $$ >>"$dir/serve.pid"',
  "        while :; do sleep 1; done",
  "        ;;",
  "    esac",
  "    exit 1",
  "    ;;",
  "esac",
  "exit 1",
  "",
].join("\n");

for (const [name, body] of [
  ["cloudflared", cloudflaredStub],
  ["pi-web", piwebStub],
  ["npm", npmStub],
  ["tailscale", tailscaleStub],
]) {
  writeFileSync(join(stubbin, name), body);
  chmodSync(join(stubbin, name), 0o755);
}
writePiwebPrefix(npmPrefixTemplate, { version: "0.1.1", entry: join(stubbin, "pi-web") });
writeFileSync(join(stubbin, "gh"), ghStubBody);
chmodSync(join(stubbin, "gh"), 0o755);

// Named-mode tunnel config: its ingress service port must match portC so the
// deploy script's named-tunnel identity (config port == local port) holds.
const cfConfig = join(scratch, "config.yml");
writeFileSync(
  cfConfig,
  [
    "tunnel: 00000000-0000-0000-0000-000000000000",
    "credentials-file: /nonexistent-credentials.json",
    "ingress:",
    "  - hostname: stub-variants.example.com",
    `    service: http://127.0.0.1:${portC}`,
    "  - service: http_status:404",
    "",
  ].join("\n"),
);

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

// Names of the windows a scratch session currently has.
const windowsOf = (sess) =>
  spawnSync("tmux", ["list-windows", "-t", sess, "-F", "#{window_name}"], { encoding: "utf8", env: tmuxEnv() })
    .stdout.split("\n")
    .filter(Boolean);

// PID of the process running in the session's serve/tunnel window.
const panePid = (sess, window) =>
  Number(spawnSync("tmux", ["list-panes", "-t", `${sess}:${window}`, "-F", "#{pane_pid}"], { encoding: "utf8", env: tmuxEnv() }).stdout.trim());

// ---------------------------------------------------------------------------
// One deploy run. Returns { status, stdout, pid } (pid: last cloudflared stub).
// Async: the loop window's stub records its PID only after tmux new-window
// returns, so the deploy waits for that entry before resolving (callers
// assert on the pi-web PID drift between deploys).
// ---------------------------------------------------------------------------
const deploy = async ({ expose = "cloudflare", tunnel, port, target = session }) => {
  const piwebCountBefore = pidList("piweb").length;
  const env = withoutTmux({
    ...process.env,
    ...releaseStubEnv(join(scratch, "gh.argv")),
    PATH: `${stubbin}:${process.env.PATH}`,
    TMPDIR: scratch,
    HOME: scratch, // keep ~/.piweb residue inside the scratch dir (code review)
    PIWEB_EXPOSE: expose,
    PIWEB_TMUX_SESSION: target,
    PIWEB_PORT: String(port),
    PIWEB_HOST: "127.0.0.1",
    PIWEB_CF_CONFIG: cfConfig,
    PIWEB_CF_HOSTNAME: "stub-variants.example.com",
  });
  if (tunnel !== undefined) {
    env.PIWEB_TUNNEL = tunnel;
  }
  const result = spawnSync("bash", [deployScript], { cwd: root, encoding: "utf8", timeout: 120_000, env });
  assert.equal(result.status, 0, `deploy-piweb.sh exited ${result.status}\nstdout:\n${result.stdout}\nstderr:\n${result.stderr}`);
  for (let i = 0; i < 50 && pidList("piweb").length <= piwebCountBefore; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  return { stdout: result.stdout, pid: lastPid("cloudflared") };
};

try {
  // --- 1. quick, argv-preserving connector: pgrep identity reuses it -------
  const quick1 = await deploy({ tunnel: "quick", port: portA });
  const quick2 = await deploy({ tunnel: "quick", port: portA });
  assert.equal(
    quick2.pid,
    quick1.pid,
    `pi#107 (quick, pgrep identity): consecutive deploys must keep the connector PID unchanged; observed ${quick1.pid} -> ${quick2.pid}`,
  );
  assert.match(quick2.stdout, /Reusing healthy cloudflared quick tunnel/);
  const url1 = quick1.stdout.match(/Quick tunnel up: (https:\/\/[a-z0-9-]+\.trycloudflare\.com)/)?.[1];
  const url2 = quick2.stdout.match(/Quick tunnel up: (https:\/\/[a-z0-9-]+\.trycloudflare\.com)/)?.[1];
  assert.ok(url1 && url2, "deploy output must report the quick-tunnel URL");
  assert.equal(url2, url1, `pi#107 (quick): URL must be reused; observed ${url1} -> ${url2}`);
  // The pi-web loop window is recycled even while the tunnel is reused —
  // its PID must change on every deploy (code review).
  assert.ok(pidList("piweb").length >= 2, "each deploy must start its own pi-web loop process");
  assert.notEqual(
    pidList("piweb")[1],
    pidList("piweb")[0],
    "pi#107 (quick): the pi-web loop PID must change across deploys (window recycled)",
  );

  // --- 2. port change: stale forwarding is an identity miss -> replace ----
  const quick3 = await deploy({ tunnel: "quick", port: portB });
  assert.notEqual(quick3.pid, quick1.pid, "pi#107 (port change): the stale connector must be replaced (new PID)");
  const url3 = quick3.stdout.match(/Quick tunnel up: (https:\/\/[a-z0-9-]+\.trycloudflare\.com)/)?.[1];
  assert.ok(url3, "deploy output must report the new quick-tunnel URL");
  assert.notEqual(url3, url1, "pi#107 (port change): the quick URL must change with the connector");
  const oldAlive = spawnSync("kill", ["-0", String(quick1.pid)], { encoding: "utf8" });
  assert.notEqual(oldAlive.status, 0, `pi#107 (port change): old connector PID ${quick1.pid} must be gone after replacement`);

  // --- 3. named: consecutive deploys keep the --config connector ----------
  const named1 = await deploy({ tunnel: "named", port: portC });
  const piwebBeforeNamed = lastPid("piweb");
  const named2 = await deploy({ tunnel: "named", port: portC });
  assert.equal(
    named2.pid,
    named1.pid,
    `pi#107 (named): consecutive deploys must keep the cloudflared tunnel --config process unchanged; observed ${named1.pid} -> ${named2.pid}`,
  );
  assert.match(named2.stdout, /Reusing cloudflared named tunnel/);
  assert.notEqual(
    lastPid("piweb"),
    piwebBeforeNamed,
    "pi#107 (named): the pi-web loop PID must change across deploys (window recycled)",
  );

  // --- 4. legacy session: unnamed loop window is recycled (code review) -----
  // Pre-seed the scratch session the way pre-pi#107 deploys and the README
  // autostart recipe do: one unnamed window running the pi-web loop.
  spawnSync("tmux", ["new-session", "-d", "-s", legacySession, join(stubbin, "pi-web")], { encoding: "utf8", env: tmuxEnv() });
  await new Promise((resolve) => setTimeout(resolve, 500)); // let the stub record its PID
  const legacyOldPid = lastPid("piweb");
  const legacyOldAlive = spawnSync("kill", ["-0", String(legacyOldPid)], { encoding: "utf8" });
  assert.equal(legacyOldAlive.status, 0, "the pre-seeded legacy loop process must be alive before the deploy");
  const legacy = await deploy({ expose: "", port: portA, target: legacySession });
  const legacyNewPid = lastPid("piweb");
  const legacyOldDead = spawnSync("kill", ["-0", String(legacyOldPid)], { encoding: "utf8" });
  assert.notEqual(legacyOldDead.status, 0, `pi#107 (legacy): the old unnamed loop process PID ${legacyOldPid} must be dead after the deploy`);
  const legacyNewAlive = spawnSync("kill", ["-0", String(legacyNewPid)], { encoding: "utf8" });
  assert.equal(legacyNewAlive.status, 0, "the deployed pi-web loop process must be alive");
  assert.notEqual(legacyNewPid, legacyOldPid, "pi#107 (legacy): the deploy must start a new pi-web loop process");
  assert.deepEqual(
    windowsOf(legacySession),
    ["piweb"],
    "pi#107 (legacy): after one deploy the session must have exactly one loop window, named 'piweb'",
  );

  // --- 5. tailscale serve: holder reuse and port-change replacement ------
  // (code review: two deploys keep the serve pane PID; a port change replaces
  // the holder and leaves exactly one 'serve' window.)
  const ts1 = await deploy({ expose: "tailscale", port: portT1, target: tsSession });
  assert.match(ts1.stdout, /Starting 'tailscale serve'/, "the first tailscale deploy must start the serve holder");
  assert.match(ts1.stdout, /pi-web served at https:\/\/stub-tailnet\.ts\.net/, "the tailscale hostname must be resolved from the stub");
  const servePid1 = panePid(tsSession, "serve");
  const piwebBeforeTs2 = lastPid("piweb");
  const ts2 = await deploy({ expose: "tailscale", port: portT1, target: tsSession });
  assert.match(ts2.stdout, /'tailscale serve' already forwards to 127\.0\.0\.1:\d+; keeping it/, "the second deploy must keep the healthy serve mapping");
  assert.equal(
    panePid(tsSession, "serve"),
    servePid1,
    `pi#107 (tailscale): consecutive deploys must keep the serve pane PID unchanged; observed ${servePid1} -> ${panePid(tsSession, "serve")}`,
  );
  assert.equal(windowsOf(tsSession).filter((w) => w === "serve").length, 1, "exactly one 'serve' window must exist after the reuse deploy");
  assert.notEqual(
    lastPid("piweb"),
    piwebBeforeTs2,
    "pi#107 (tailscale): the pi-web loop PID must change across deploys (window recycled)",
  );
  const ts3 = await deploy({ expose: "tailscale", port: portT2, target: tsSession });
  assert.match(ts3.stdout, /Starting 'tailscale serve'/, "a port change must replace the serve holder");
  const servePid3 = panePid(tsSession, "serve");
  assert.notEqual(servePid3, servePid1, "pi#107 (tailscale): a port change must replace the serve holder (new pane PID)");
  const oldServeAlive = spawnSync("kill", ["-0", String(servePid1)], { encoding: "utf8" });
  assert.notEqual(oldServeAlive.status, 0, `pi#107 (tailscale): the old serve holder PID ${servePid1} must be dead after replacement`);
  assert.equal(windowsOf(tsSession).filter((w) => w === "serve").length, 1, "exactly one 'serve' window must exist after the port change");

  console.log("PASS: quick (pgrep identity) reuse, port-change replacement, named-tunnel reuse, legacy-window recycle, and tailscale serve reuse all held");
} finally {
  // Never leak the scratch sessions, stub processes, or temp files. HOME was
  // pointed at the scratch dir for every deploy, so the stable quick-tunnel
  // log lives inside scratch and goes with it.
  for (const sess of scratchSessions) {
    spawnSync("tmux", ["kill-session", "-t", sess], { encoding: "utf8", env: tmuxEnv() });
  }
  for (const pid of [...pidList("cloudflared"), ...pidList("piweb"), ...pidList("serve")]) {
    spawnSync("kill", [String(pid)], { encoding: "utf8" });
  }
  rmSync(scratch, { recursive: true, force: true });
}
