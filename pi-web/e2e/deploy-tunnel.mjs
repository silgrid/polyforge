// carries: deploy-piweb.sh
// pi#107 repro e2e: deploy-piweb.sh must be tunnel-idempotent (pi#38 followup).
//
// Claim under test: `deploy-piweb.sh` with PIWEB_EXPOSE=cloudflare /
// PIWEB_TUNNEL=quick tears the whole tmux session down and rebuilds the
// quick tunnel on every run, so the tunnel process (PID) and the
// trycloudflare.com quick URL drift between two consecutive deploys with
// identical configuration.
//
// This pass runs fully offline — no real cloudflared, tailscale, npm global
// install, or network access. PATH stubs stand in for the external tools:
//
//   cloudflared  prints one quick-tunnel URL unique to its process (like the
//                real connector, which mints a fresh trycloudflare.com URL on
//                every run) and stays in the foreground, holding the tmux
//                tunnel window open.
//   pi-web       stays in the foreground (the restart-loop window's server).
//   npm          `install -g` lays a scratch global prefix from a template
//                (e2e/lane-stubs.mjs writePiwebPrefix: an installed pi-web
//                whose install scripts ran, its entry a copy of the pi-web
//                stub) and `root -g` names it, so the deploy's install check
//                and served == installed check run against it.
//
// Every child (deploy and harness tmux calls) runs without TMUX and TMUX_PANE.
//
// The deployment targets a scratch tmux session name and a scratch port so
// the live "piweb" session on this machine is never touched (lesson from the
// pi#38 verification report: deploy tests must not restart production).
//
// Acceptance (per the work item): after two consecutive deploys with the
// same PIWEB_EXPOSE, the tunnel connector PID and the quick-tunnel URL must
// be unchanged.
//
// Run: node e2e/deploy-tunnel.mjs   (from the repository root or anywhere)
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

const scratch = mkdtempSync(join(tmpdir(), "piweb-deploy-repro-"));
const stubbin = join(scratch, "bin");
mkdirSync(stubbin);
const session = `piweb-repro-${process.pid}`;
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

// A scratch port so this repro never collides with the live deployment
// (real one serves 127.0.0.1:8142 on this machine) and so the pkill/pgrep
// patterns below only ever match this test's own stub processes.
const probe = createServer();
probe.listen(0, "127.0.0.1");
await once(probe, "listening");
const localPort = probe.address().port;
await new Promise((resolve, reject) => probe.close((error) => (error ? reject(error) : resolve())));

// ---------------------------------------------------------------------------
// Offline tool stubs (see header comment).
// ---------------------------------------------------------------------------
const cloudflaredStub = [
  "#!/bin/sh",
  "# Stub cloudflared quick-tunnel connector (pi#107 deploy e2e):",
  "# - records the connector PID next to this script;",
  "# - prints exactly one quick-tunnel URL unique to this process, mimicking",
  "#   the real connector which mints a fresh trycloudflare.com URL per run;",
  "# - stays in the foreground like a healthy connector, holding the tmux",
  "#   tunnel window open.",
  'dir="$(cd "$(dirname "$0")" && pwd)"',
  'echo $$ >>"$dir/cloudflared.pid"',
  'echo "INF stub connector: your quick tunnel is ready!"',
  'echo "INF stub connector: https://stub-$$-repro.trycloudflare.com"',
  "# Loop instead of exec-ing sleep so the process keeps its original argv",
  "# (cloudflared tunnel --url ...) like the real binary — the deploy",
  "# script's pgrep -f identity must be able to see it.",
  "while :; do sleep 1; done",
  "",
].join("\n");

const piwebStub = [
  "#!/bin/sh",
  "# Stub pi-web server (pi#107 deploy e2e): records its PID and holds the",
  "# restart-loop tmux window open without binding any port. The PID file is",
  "# absolute and the stub loops instead of exec-ing sleep, so the installed",
  "# copy records into the same file and keeps its script path in its argv.",
  `echo $$ >>'${join(stubbin, "piweb.pid")}'`,
  "while :; do sleep 1; done",
  "",
].join("\n");

const npmStub = [
  "#!/bin/sh",
  "# Stub npm (pi#107 deploy e2e): `ls -g <pkg>` -> not installed, `root -g`",
  "# -> the scratch prefix, `install -g <pkg>` -> the prefix template copied",
  "# there; the real global prefix is never touched.",
  'if [ "$1" = "ls" ]; then exit 1; fi',
  `if [ "$1" = "root" ]; then printf '%s\\n' '${npmPrefix}/lib/node_modules'; exit 0; fi`,
  `if [ "$1" = "install" ]; then rm -rf '${npmPrefix}' && mkdir -p '${npmPrefix}' && cp -R '${npmPrefixTemplate}/.' '${npmPrefix}/'; exit $?; fi`,
  "exit 0",
  "",
].join("\n");

for (const [name, body] of [["cloudflared", cloudflaredStub], ["pi-web", piwebStub], ["npm", npmStub]]) {
  writeFileSync(join(stubbin, name), body);
  chmodSync(join(stubbin, name), 0o755);
}
writePiwebPrefix(npmPrefixTemplate, { version: "0.1.1", entry: join(stubbin, "pi-web") });
writeFileSync(join(stubbin, "gh"), ghStubBody);
chmodSync(join(stubbin, "gh"), 0o755);

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

// ---------------------------------------------------------------------------
// One deploy run. Returns { status, stdout, url, pid }.
// ---------------------------------------------------------------------------
const deploy = () => {
  const result = spawnSync("bash", [deployScript], {
    cwd: root,
    encoding: "utf8",
    timeout: 120_000,
    env: withoutTmux({
      ...process.env,
      ...releaseStubEnv(join(scratch, "gh.argv")),
      PATH: `${stubbin}:${process.env.PATH}`,
      TMPDIR: scratch,
      HOME: scratch, // keep the stable ~/.piweb quick-tunnel log out of the real home
      PIWEB_EXPOSE: "cloudflare",
      PIWEB_TUNNEL: "quick",
      PIWEB_TMUX_SESSION: session,
      PIWEB_PORT: String(localPort),
      PIWEB_HOST: "127.0.0.1",
      PIWEB_CF_CONFIG: "",
      PIWEB_CF_HOSTNAME: "",
    }),
  });
  assert.equal(result.status, 0, `deploy-piweb.sh exited ${result.status}\nstdout:\n${result.stdout}\nstderr:\n${result.stderr}`);
  const url = result.stdout.match(/Quick tunnel up: (https:\/\/[a-z0-9-]+\.trycloudflare\.com)/)?.[1];
  assert.ok(url, `deploy output must report the quick-tunnel URL\nstdout:\n${result.stdout}`);
  return { stdout: result.stdout, url, pid: lastPid("cloudflared") };
};

try {
  // --- deploy #1 -----------------------------------------------------------
  const first = deploy();

  // The connector must actually be alive inside the tunnel window of the
  // scratch session (sanity: the stubbed tunnel really is running there).
  const kill0 = spawnSync("kill", ["-0", String(first.pid)], { encoding: "utf8" });
  assert.equal(kill0.status, 0, `tunnel connector PID ${first.pid} must be alive after deploy #1`);
  const windows = spawnSync("tmux", ["list-windows", "-t", session, "-F", "#{window_name}"], { encoding: "utf8", env: tmuxEnv() });
  assert.ok(windows.stdout.split("\n").includes("tunnel"), "the scratch session must have a 'tunnel' window");

  // --- deploy #2: same PIWEB_EXPOSE / PIWEB_TUNNEL / port ------------------
  const second = deploy();

  // --- pi#107 acceptance: the tunnel must have survived the redeploy -------
  assert.equal(
    second.pid,
    first.pid,
    `pi#107: two consecutive deploys (same PIWEB_EXPOSE) must keep the tunnel connector PID unchanged; observed ${first.pid} -> ${second.pid}`,
  );
  assert.equal(
    second.url,
    first.url,
    `pi#107: two consecutive deploys (same PIWEB_EXPOSE) must keep the quick-tunnel URL unchanged; observed ${first.url} -> ${second.url}`,
  );

  console.log("PASS: deploy #1 and #2 kept tunnel PID and quick URL stable");
} finally {
  // Never leak the scratch session, stub processes, or temp files.
  spawnSync("tmux", ["kill-session", "-t", session], { encoding: "utf8", env: tmuxEnv() });
  for (const pid of [...pidList("cloudflared"), ...pidList("piweb")]) {
    spawnSync("kill", [String(pid)], { encoding: "utf8" });
  }
  rmSync(scratch, { recursive: true, force: true });
}
