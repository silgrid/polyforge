// carries: bin/pi-web-deploy.js, bin/pi-web.js, lib/web-auth*, app/api/web-auth/*
// repro e2e: a deploy must keep the operator service shape.
//
// Reported failure (2026-10-30): the release lane ran
// `.polyforge/deploy.sh` from the daemon's empty environment, so the tmux
// `piweb` window started `pi-web -H 0.0.0.0` with none of PIWEB_PORT,
// PI_WEB_PASSWORD or PI_WEB_ALLOWED_HOSTS. The service fell back to port
// 30141 (colliding with another service) and, if that port had been free,
// would have listened without authentication.
//
// Covered criteria:
//   - AC1: from a daemon-like empty environment, a deploy reads the operator's
//     ~/.config/piweb/deploy.env (0600) and starts the service on that port,
//     with that password and those allowed hosts; explicit environment
//     variables still win over the file.
//   - AC2: with no deploy.env, or one missing the port or the password, a
//     non-loopback deploy is refused before installing or recycling the old
//     service.
//   - AC3: the deploy lane's ending fails when the configured service is not
//     reachable, or when its unauthenticated API is not refused (401); a
//     healthy service (GET /login 200, unauthenticated API 401) succeeds.
//   - an authenticated GET / that answers 5xx fails the deploy
//     even though /login stays 200 and the anonymous API stays 401.
//   - deploy.env values may carry an unquoted trailing
//     ` # comment`; a deploy.env that is not 0600 deploys with one named
//     `warn:` line; a probe failure after the swap reports state=changed; a
//     host without curl names the missing dependency in both probes.
//   - TLS edge probe (warn-only): with allowed hosts and a
//     reachable edge, only the first entry is probed once (https, bounded
//     --max-time) and no warning is printed; with the edge refusing, exactly
//     one `warn: TLS edge unreachable (<host>)` line is printed while the lane
//     still exits 0 and reports state=deployed; with no PI_WEB_ALLOWED_HOSTS
//     the edge probe is skipped with exactly one `warn: TLS edge probe
//     skipped: PI_WEB_ALLOWED_HOSTS is not set (no edge host to probe)` line
//     naming the missing dependency and no https request is
//     made.
//
// Fully offline: PATH stubs stand in for gh/npm/pi-web, a curl stub answers
// every https:// request (plain http goes to the real curl), HOME is a scratch
// directory (so the real ~/.config/piweb/deploy.env and tmux `piweb` session
// are never touched), and every deploy uses a scratch tmux session and port;
// every child (deploy and harness tmux calls) runs without TMUX and TMUX_PANE.
// The npm stub's `install -g` lays a scratch global prefix from a template
// (e2e/lane-stubs.mjs writePiwebPrefix: an installed pi-web whose install
// scripts ran, its entry a byte copy of the pi-web stub, which reads its
// config by absolute path) and `root -g` names it, so the deploy's install
// check and served == installed check run against it.
//
// Run: node e2e/deploy-service-shape.mjs   (from the repository root)
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { once } from "node:events";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, symlinkSync, writeFileSync } from "node:fs";
import { createServer as createNetServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { ghStubBody, laneRemoteEnv, readLog, writePiwebPrefix, writeStub } from "./lane-stubs.mjs";
import { blockFail, blockPass, createCheck } from "./harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const deployScript = join(root, "deploy-piweb.sh");
// The deploy lane is the repository's one deploy entry, `.polyforge/deploy.sh`
// at the repository root, with `--component pi-web`.
const laneDeploy = join(root, "..", ".polyforge", "deploy.sh");
const LANE_COMPONENT = ["--component", "pi-web"];

assert.ok(process.platform === "linux" || process.platform === "darwin", "deploy-piweb.sh targets Linux/macOS");
{
  const tmux = spawnSync("tmux", ["-V"], { encoding: "utf8" });
  assert.equal(tmux.status, 0, "tmux must be installed for the deploy e2e");
}

const PASSWORD = "fixture-password-task-160";
const LATEST_TAG = "v0.1.1";
const PAYLOAD = "stub pi-web release tarball payload for the service-shape repro\n";
const HASH = createHash("sha256").update(PAYLOAD).digest("hex");

const scratch = mkdtempSync(join(tmpdir(), "piweb-service-shape-"));
const stubbin = join(scratch, "bin");
mkdirSync(stubbin);
const npmLog = join(scratch, "npm.argv");
const npmPrefix = join(scratch, "npm-prefix");
const npmPrefixTemplate = join(scratch, "npm-prefix-template");
const ghLog = join(scratch, "gh.argv");
const deployEnvPath = join(scratch, ".config", "piweb", "deploy.env");
const stubConfigPath = join(scratch, "stub-config.json");
const curlLog = join(scratch, "curl-https.argv");
const realCurl = spawnSync("sh", ["-c", "command -v curl"], { encoding: "utf8" }).stdout.trim();
assert.ok(realCurl, "curl must be installed for the deploy e2e (the local service probe uses it)");
const sessions = [
  `piweb-shape-ac1-${process.pid}`,
  `piweb-shape-priority-${process.pid}`,
  `piweb-shape-ac2-nofile-${process.pid}`,
  `piweb-shape-ac2-noport-${process.pid}`,
  `piweb-shape-ac2-nopass-${process.pid}`,
  `piweb-shape-ac3-healthy-${process.pid}`,
  `piweb-shape-ac3-unreachable-${process.pid}`,
  `piweb-shape-ac3-open-${process.pid}`,
  `piweb-shape-edge-reach-${process.pid}`,
  `piweb-shape-edge-refuse-${process.pid}`,
  `piweb-shape-edge-none-${process.pid}`,
  `piweb-shape-inline-${process.pid}`,
  `piweb-shape-mode644-${process.pid}`,
  `piweb-shape-nocurl-probe-${process.pid}`,
  `piweb-shape-nocurl-edge-${process.pid}`,
  `piweb-shape-ac3-login500-${process.pid}`,
];

// ---------------------------------------------------------------------------
// Offline stubs.
// ---------------------------------------------------------------------------
const npmStub = [
  "#!/bin/sh",
  "# Stub npm: logs argv; `ls -g @silgrid/pi-web` reads as a",
  "# live 0.1.1 install (so deploy.sh records previous_version), the old",
  "# @agegr package reads as absent, `root -g` names the scratch prefix, and",
  "# `install -g` copies the prefix template there (never the real prefix).",
  'log="${NPMSTUB_LOG:?NPMSTUB_LOG must point at the invocation log}"',
  'printf \'%s\\n\' "$*" >>"$log"',
  `if [ "$1" = "root" ]; then printf '%s\\n' '${npmPrefix}/lib/node_modules'; exit 0; fi`,
  `if [ "$1" = "install" ]; then rm -rf '${npmPrefix}' && mkdir -p '${npmPrefix}' && cp -R '${npmPrefixTemplate}/.' '${npmPrefix}/'; exit $?; fi`,
  'if [ "$1" = "ls" ]; then',
  '  case "$*" in',
  '    *"@agegr/pi-web"*) exit 1 ;;',
  '    *"@silgrid/pi-web"*) printf \'%s\\n\' "@silgrid/pi-web@0.1.1"; exit 0 ;;',
  "  esac",
  "fi",
  "exit 0",
  "",
].join("\n");

const piwebStub = [
  "#!/usr/bin/env node",
  "// Stub pi-web server: records the service's argv and the",
  "// service-shape environment, then either serves a password-protected",
  "// pi-web-like HTTP surface (serve), serves it with authentication disabled",
  "// (open), or holds the window without binding anything (hold/dead).",
  'const http = require("node:http");',
  'const { appendFileSync, readFileSync } = require("node:fs");',
  'const config = JSON.parse(readFileSync("' + stubConfigPath + '", "utf8"));',
  "",
  'const arg = (names) => {',
  "  for (let i = 2; i < process.argv.length; i += 1) {",
  "    if (names.includes(process.argv[i])) return process.argv[i + 1];",
  "  }",
  "  return undefined;",
  "};",
  'const portValue = arg(["--port", "-p"]) ?? process.env.PORT ?? process.env.PIWEB_PORT;',
  "const port = Number(portValue);",
  'const mode = config.mode || "serve";',
  "const record = {",
  "  pid: process.pid,",
  "  argv: process.argv.slice(2),",
  "  port: Number.isInteger(port) ? port : null,",
  "  mode,",
  "  env: {",
  "    PI_WEB_PASSWORD: process.env.PI_WEB_PASSWORD ?? null,",
  "    PI_WEB_ALLOWED_HOSTS: process.env.PI_WEB_ALLOWED_HOSTS ?? null,",
  "    PI_WEB_IDLE_TIMEOUT_MS: process.env.PI_WEB_IDLE_TIMEOUT_MS ?? null,",
  "    PI_WEB_HOSTNAME: process.env.PI_WEB_HOSTNAME ?? null,",
  "    PIWEB_PORT: process.env.PIWEB_PORT ?? null,",
  "    PORT: process.env.PORT ?? null,",
  "  },",
  "};",
  'appendFileSync(config.record, `${JSON.stringify(record)}\\n`);',
  "",
  'if (!Number.isInteger(port) || mode === "hold" || mode === "dead" || mode === "unreachable") {',
  "  // No configured port, or deliberately unreachable: hold the tmux",
  "  // window without binding anything.",
  "  setInterval(() => {}, 60_000);",
  "} else {",
  "  const authenticated = mode !== \"open\" && Boolean(process.env.PI_WEB_PASSWORD);",
  "  const host = arg([\"-H\", \"--hostname\"]) ?? process.env.PI_WEB_HOSTNAME ?? process.env.PIWEB_HOST ?? \"127.0.0.1\";",
  "  const server = http.createServer((request, response) => {",
  '    if (!authenticated) {',
  '      if (request.url === "/login") {',
  '        response.writeHead(302, { Location: "/" });',
  "        response.end();",
  "        return;",
  "      }",
  '      if (request.url.startsWith("/api/")) {',
  '        response.writeHead(200, { "Content-Type": "application/json" });',
  '        response.end("{}");',
  "        return;",
  "      }",
  "      response.writeHead(200);",
  '      response.end("open");',
  "      return;",
  "    }",
  '    if (request.url === "/login") {',
  '      response.writeHead(200, { "Content-Type": "text/html" });',
  '      response.end("login");',
  "      return;",
  "    }",
  '    const cookie = request.headers.cookie ?? "";',
  '    const authorization = request.headers.authorization ?? "";',
  '    const basic = authorization.startsWith("Basic ") ? Buffer.from(authorization.slice(6), "base64").toString("utf8") : "";',
  '    const authenticatedRequest = cookie.includes("pi_web_session=") || (basic.startsWith("pi:") && basic.slice(3) === (process.env.PI_WEB_PASSWORD ?? ""));',
  '    if (request.url === "/api/web-auth") {',
  '      if (request.method === "POST") {',
  '        response.writeHead(200, { "Content-Type": "application/json", "Set-Cookie": "pi_web_session=stub; Path=/; HttpOnly" });',
  '        response.end(JSON.stringify({ ok: true }));',
  "        return;",
  "      }",
  '      response.writeHead(200, { "Content-Type": "application/json" });',
  '      response.end(JSON.stringify({ enabled: true, authenticated: authenticatedRequest }));',
  "      return;",
  "    }",
  '    if (request.url.startsWith("/api/")) {',
  '      response.writeHead(401, { "WWW-Authenticate": \'Basic realm="Pi Web"\' });',
  '      response.end("Authentication required");',
  "      return;",
  "    }",
  '    if (!authenticatedRequest) {',
  '      response.writeHead(307, { Location: "/login" });',
  "      response.end();",
  "      return;",
  "    }",
  '    if (mode === "authenticated-500") {',
  '      response.writeHead(500, { "Content-Type": "text/html" });',
  '      response.end("broken authenticated page");',
  "      return;",
  "    }",
  '    response.writeHead(307, { Location: "/login" });',
  "    response.end();",
  "  });",
  "  server.on(\"error\", () => {",
  "    // Keep the window alive even when the bind fails; a failed bind is an",
  "    // unreachable service, exactly the state the deploy must detect.",
  "    setInterval(() => {}, 60_000);",
  "  });",
  "  server.listen(port, host);",
  "}",
  "",
].join("\n");

const curlStub = [
  "#!/bin/sh",
  "# Stub curl: plain-http requests (the local service probe)",
  "# go to the real curl; every https:// request (the TLS edge probe) is",
  "# logged and answered from the stub config: httpsMode reach -> 307, exit 0;",
  "# refuse -> 000 (curl's -w output on a failed connect), exit 7.",
  "https=",
  'for a in "$@"; do case "$a" in https://*) https=1 ;; esac; done',
  `if [ -z "$https" ]; then exec '${realCurl}' "$@"; fi`,
  `printf '%s\\n' "$*" >>'${curlLog}'`,
  `if grep -q '"httpsMode":"refuse"' '${stubConfigPath}'; then`,
  '  echo "curl: (7) Failed to connect (stub)" >&2',
  "  printf '000'",
  "  exit 7",
  "fi",
  "printf '307'",
  "exit 0",
  "",
].join("\n");

for (const [name, body] of [
  ["gh", ghStubBody],
  ["npm", npmStub],
  ["pi-web", piwebStub],
  ["curl", curlStub],
]) {
  writeStub(stubbin, name, body);
}
writePiwebPrefix(npmPrefixTemplate, { version: "0.1.1", entry: join(stubbin, "pi-web") });

// An inherited TMUX/TMUX_PANE selects the operator's tmux server; no child of
// this suite carries them.
const tmuxEnv = (env = { ...process.env }) => {
  delete env.TMUX;
  delete env.TMUX_PANE;
  return env;
};

// ---------------------------------------------------------------------------
// Fixtures and helpers.
// ---------------------------------------------------------------------------
const records = (file) => {
  try {
    return readFileSync(file, "utf8")
      .split("\n")
      .filter(Boolean)
      .map((line) => JSON.parse(line));
  } catch {
    return [];
  }
};

const alive = (pid) => spawnSync("kill", ["-0", String(pid)]).status === 0;

const freePort = async () => {
  const probe = createNetServer();
  probe.listen(0, "127.0.0.1");
  await once(probe, "listening");
  const port = probe.address().port;
  await new Promise((resolve, reject) => probe.close((error) => (error ? reject(error) : resolve())));
  return port;
};

const httpStatus = async (url, timeoutMs = 2_000) => {
  const response = await fetch(url, { redirect: "manual", signal: AbortSignal.timeout(timeoutMs) });
  await response.arrayBuffer();
  return response.status;
};

const waitForStatus = async (url, expected, timeoutMs = 10_000) => {
  const deadline = Date.now() + timeoutMs;
  let last = "unreachable";
  while (Date.now() < deadline) {
    try {
      last = await httpStatus(url, 1_000);
      if (last === expected) return last;
    } catch {
      last = "unreachable";
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`${url} did not answer ${expected} within ${timeoutMs}ms (last: ${last})`);
};

const waitForRecords = async (file, count, timeoutMs = 10_000) => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const found = records(file);
    if (found.length >= count) return found;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  return records(file);
};

const writeDeployEnv = (content, mode = 0o600) => {
  if (content === null) {
    rmSync(deployEnvPath, { force: true });
    return;
  }
  mkdirSync(dirname(deployEnvPath), { recursive: true, mode: 0o700 });
  writeFileSync(deployEnvPath, content);
  chmodSync(deployEnvPath, mode);
};

// a PATH without curl. The stubs minus curl come first, then
// one symlink per executable on the real PATH except any `curl`, so every
// other tool the deploy needs resolves exactly as before.
const curllessPath = (() => {
  const stubs = join(scratch, "bin-nocurl-stubs");
  const tools = join(scratch, "bin-nocurl-tools");
  mkdirSync(stubs);
  mkdirSync(tools);
  for (const name of ["gh", "npm", "pi-web"]) symlinkSync(join(stubbin, name), join(stubs, name));
  for (const dir of (process.env.PATH ?? "").split(":")) {
    if (!dir || !existsSync(dir)) continue;
    let names = [];
    try { names = readdirSync(dir); } catch { continue; }
    for (const name of names) {
      if (name === "curl" || existsSync(join(tools, name))) continue;
      const target = join(dir, name);
      try {
        const info = statSync(target);
        if (!info.isFile() || (info.mode & 0o111) === 0) continue;
      } catch { continue; }
      symlinkSync(target, join(tools, name));
    }
  }
  return `${stubs}:${tools}`;
})();

const writeStubConfig = (record, mode, httpsMode = "reach") => {
  writeFileSync(stubConfigPath, `${JSON.stringify({ record, mode, httpsMode })}\n`);
};

const httpsRequests = () => readLog(curlLog).filter((line) => line.includes("https://"));

// A daemon-like environment: every operator service-shape variable removed,
// only the harness's own scratch routing left in place.
const baseEnv = (session, overrides = {}) => {
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (key.startsWith("PI_WEB_")) delete env[key];
    if (key.startsWith("PIWEB_") && key !== "PIWEB_TMUX_SESSION") delete env[key];
  }
  // the default install channel (the latest release of silgrid/polyforge)
  // is served by the gh stub
  Object.assign(env, {
    GHSTUB_LOG: ghLog,
    GHSTUB_LATEST_TAG: LATEST_TAG,
    GHSTUB_PAYLOAD: PAYLOAD,
    GHSTUB_HASH: HASH,
    GHSTUB_CHECKSUM_MODE: "match",
    PATH: `${stubbin}:${process.env.PATH}`,
    TMPDIR: scratch,
    HOME: scratch,
    PIWEB_TMUX_SESSION: session,
    NPMSTUB_LOG: npmLog,
  });
  Object.assign(env, laneRemoteEnv(laneDeploy, env));
  Object.assign(env, overrides);
  return tmuxEnv(env);
};

const runDeploy = (args, env) =>
  spawnSync("bash", args.lane ? [laneDeploy, ...(args.args ?? []), ...LANE_COMPONENT] : [deployScript, ...(args.args ?? [])], {
    cwd: root,
    encoding: "utf8",
    timeout: 120_000,
    env,
  });

const output = (result) => `${result.stdout}\n${result.stderr}`;

const startOldService = async (session, record, port) => {
  writeStubConfig(record, "hold");
  const env = baseEnv(session, {
    PIWEB_PORT: String(port),
    PI_WEB_PASSWORD: "old-service-password",
  });
  const started = spawnSync("tmux", ["new-session", "-d", "-s", session, `exec ${join(stubbin, "pi-web")}`], {
    encoding: "utf8",
    env,
  });
  assert.equal(started.status, 0, `old-service fixture: tmux must start the scratch session ${session}`);
  const found = await waitForRecords(record, 1, 5_000);
  assert.equal(found.length, 1, "old-service fixture: the stub must record its PID");
  return found[0];
};

const recordFiles = [
  "inline.jsonl",
  "mode644.jsonl",
  "nocurl-probe.jsonl",
  "nocurl-edge.jsonl",
  "ac1.jsonl",
  "priority.jsonl",
  "ac2-0.jsonl",
  "ac2-1.jsonl",
  "ac2-2.jsonl",
  "ac3-healthy.jsonl",
  "ac3-unreachable.jsonl",
  "ac3-open.jsonl",
  "edge-reach.jsonl",
  "edge-refuse.jsonl",
  "edge-none.jsonl",
  "ac3-authenticated-500.jsonl",
];

// Each scenario starts from no scratch service at all, so a late process
// from the previous (deliberately failing) scenario can never write into
// this scenario's record file.
const resetScratchServices = () => {
  for (const session of sessions) {
    spawnSync("tmux", ["kill-session", "-t", session], { encoding: "utf8", env: tmuxEnv() });
  }
  for (const file of recordFiles) {
    for (const item of records(join(scratch, file))) {
      spawnSync("kill", ["-9", String(item.pid)], { encoding: "utf8" });
    }
  }
};

const failures = [];
const scenario = createCheck({ failures, message: (error) => String(error.message), pass: blockPass, fail: blockFail, before: () => resetScratchServices() });

try {
  // --- AC1: daemon-empty deploy reads the operator's deploy.env -------------
  await scenario("AC1: deploy.env supplies port, password and allowed hosts", async () => {
    writeDeployEnv(null);
    const port = await freePort();
    const record = join(scratch, "ac1.jsonl");
    rmSync(record, { force: true });
    writeDeployEnv(
      [
        `PIWEB_PORT=${port}`,
        `PI_WEB_PASSWORD=${PASSWORD}`,
        "PI_WEB_ALLOWED_HOSTS=pi.example.test,edge.example.test",
        "PI_WEB_IDLE_TIMEOUT_MS=0",
        "",
      ].join("\n"),
    );
    writeStubConfig(record, "serve");
    const result = runDeploy({ args: [] }, baseEnv(sessions[0]));
    assert.equal(result.status, 0, `deploy must succeed from a daemon-empty environment with deploy.env present\n${output(result)}`);
    assert.doesNotMatch(output(result), /^warn: .*deploy\.env/m, `a 0600 deploy.env must not warn\n${output(result)}`);

    const found = await waitForRecords(record, 1);
    assert.ok(found.length > 0, "the deployed service must have started");
    const service = found.at(-1);
    assert.equal(service.mode, "serve", "fixture sanity: the deployed service must be the serving stub");
    assert.equal(
      service.port,
      port,
      `the service must listen on deploy.env's PIWEB_PORT (${port}); observed argv ${JSON.stringify(service.argv)} env ${JSON.stringify(service.env)}`,
    );
    assert.equal(
      service.env.PI_WEB_PASSWORD,
      PASSWORD,
      `the service must receive deploy.env's PI_WEB_PASSWORD; observed environment ${JSON.stringify(service.env)}`,
    );
    assert.match(
      service.env.PI_WEB_ALLOWED_HOSTS ?? "",
      /(^|,)\s*pi\.example\.test\s*(,|$)/,
      `the service must receive deploy.env's PI_WEB_ALLOWED_HOSTS; observed ${JSON.stringify(service.env.PI_WEB_ALLOWED_HOSTS)}`,
    );
    await waitForStatus(`http://127.0.0.1:${port}/login`, 200);
    assert.equal(
      await httpStatus(`http://127.0.0.1:${port}/api/sessions`),
      401,
      "the deployed service's unauthenticated API must refuse with 401",
    );
  });

  // --- AC1: explicit environment still wins over deploy.env -----------------
  await scenario("the default install channel is the latest GitHub Release of silgrid/polyforge, never npm", async () => {
    writeDeployEnv(null);
    const port = await freePort();
    const record = join(scratch, "default-channel.jsonl");
    rmSync(record, { force: true });
    rmSync(ghLog, { force: true });
    rmSync(npmLog, { force: true });
    writeStubConfig(record, "serve");
    const result = runDeploy({ args: [] }, baseEnv(sessions[0], { PIWEB_PORT: String(port), PI_WEB_PASSWORD: PASSWORD }));
    assert.equal(result.status, 0, `a deploy with PIWEB_INSTALL_REPO unset must install from the default release channel\n${output(result)}`);
    const gh = readLog(ghLog);
    assert.ok(gh.some((line) => /^release download -R silgrid\/polyforge\b/.test(line)), `the tarball must come from silgrid/polyforge's release; observed gh argv: ${JSON.stringify(gh)}`);
    const installs = readLog(npmLog).filter((line) => /^install\b/.test(line));
    assert.equal(installs.length, 1, `exactly one npm install; observed: ${JSON.stringify(installs)}`);
    assert.match(installs[0], new RegExp(`^install -g \\S*/silgrid-pi-web-${LATEST_TAG.slice(1).replaceAll(".", "\\.")}\\.tgz$`), "npm installs the verified release tarball");
    assert.doesNotMatch(installs[0], /@latest|@silgrid\/pi-web/, "nothing is installed from the npm registry");
    await waitForRecords(record, 1);
  });

  await scenario("AC1: explicit environment beats deploy.env", async () => {
    const filePort = await freePort();
    const explicitPort = await freePort();
    const record = join(scratch, "priority.jsonl");
    rmSync(record, { force: true });
    writeDeployEnv(
      [
        `PIWEB_PORT=${filePort}`,
        "PI_WEB_PASSWORD=file-password",
        "PI_WEB_ALLOWED_HOSTS=file.example.test",
        "",
      ].join("\n"),
    );
    writeStubConfig(record, "serve");
    const env = baseEnv(sessions[1], {
      PIWEB_PORT: String(explicitPort),
      PI_WEB_PASSWORD: PASSWORD,
      PI_WEB_ALLOWED_HOSTS: "explicit.example.test",
    });
    const result = runDeploy({ args: [] }, env);
    assert.equal(result.status, 0, `deploy must succeed with explicit service-shape variables\n${output(result)}`);

    const found = await waitForRecords(record, 1);
    assert.ok(found.length > 0, "the deployed service must have started");
    const service = found.at(-1);
    assert.equal(
      service.port,
      explicitPort,
      `an explicitly set PIWEB_PORT must beat deploy.env's value (${filePort}); observed ${service.port}`,
    );
    assert.equal(service.env.PI_WEB_PASSWORD, PASSWORD, "an explicitly set PI_WEB_PASSWORD must beat deploy.env's value");
    assert.equal(
      service.env.PI_WEB_ALLOWED_HOSTS,
      "explicit.example.test",
      "an explicitly set PI_WEB_ALLOWED_HOSTS must beat deploy.env's value",
    );
    await waitForStatus(`http://127.0.0.1:${explicitPort}/login`, 200);
  });

  // --- AC2: missing deploy.env or missing port/password fails closed ---------
  const failClosedCases = [
    { title: "AC2: missing deploy.env refuses a non-loopback deploy", env: null, missing: ["PIWEB_PORT", "PI_WEB_PASSWORD"] },
    {
      title: "AC2: deploy.env without PIWEB_PORT refuses a non-loopback deploy",
      env: `PI_WEB_PASSWORD=${PASSWORD}\n`,
      missing: ["PIWEB_PORT"],
    },
    {
      title: "AC2: deploy.env without PI_WEB_PASSWORD refuses a non-loopback deploy",
      env: null,
      missing: ["PI_WEB_PASSWORD"],
    },
  ];
  for (let i = 0; i < failClosedCases.length; i += 1) {
    const testCase = failClosedCases[i];
    await scenario(testCase.title, async () => {
      const port = await freePort();
      if (i === 2) testCase.env = `PIWEB_PORT=${port}\n`;
      writeDeployEnv(testCase.env);
      rmSync(npmLog, { force: true });
      const session = sessions[2 + i];
      const record = join(scratch, `ac2-${i}.jsonl`);
      rmSync(record, { force: true });
      const oldService = await startOldService(session, record, port);

      const result = runDeploy({ args: [] }, baseEnv(session));
      const report = output(result);
      assert.notEqual(
        result.status,
        0,
        `${testCase.title}: a non-loopback deploy without the complete service shape must be refused\n${report}`,
      );
      assert.match(
        report,
        /deploy\.env|PIWEB_PORT|PI_WEB_PASSWORD|port|password|refus/i,
        `the refusal must name what is missing (deploy.env, port or password); observed:\n${report}`,
      );
      // exactly the missing keys, never a present one.
      const named = /Missing:([^\n]*)/.exec(report)?.[1]?.match(/\b(PIWEB_PORT|PI_WEB_PASSWORD)\b/g) ?? [];
      assert.deepEqual(
        [...new Set(named)].sort(),
        [...testCase.missing].sort(),
        `the refusal must name exactly the missing key(s) ${testCase.missing.join(", ")}; observed:\n${report}`,
      );
      assert.ok(
        readLog(npmLog).every((line) => !/^install\b/.test(line)),
        `the refusal must happen before any install; observed npm argv: ${JSON.stringify(readLog(npmLog))}`,
      );
      const after = records(record);
      assert.equal(
        after.length,
        1,
        `the refused deploy must not start a replacement service; observed records: ${JSON.stringify(after)}`,
      );
      assert.equal(after[0].pid, oldService.pid, "the old service must be the only service after the refusal");
      assert.ok(alive(oldService.pid), `the old service PID ${oldService.pid} must still be running`);
      assert.equal(
        spawnSync("tmux", ["has-session", "-t", session], { encoding: "utf8", env: tmuxEnv() }).status,
        0,
        "the old service's tmux session must survive the refused deploy",
      );
    });
  }

  // --- AC3: the deploy lane's ending checks the running service -------------
  const ac3 = async ({ title, mode, check }) => {
    await scenario(title, async () => {
      writeDeployEnv(null);
      const port = await freePort();
      const record = join(scratch, `ac3-${mode}.jsonl`);
      rmSync(record, { force: true });
      rmSync(npmLog, { force: true });
      rmSync(ghLog, { force: true });
      const session = {
        healthy: sessions[5],
        unreachable: sessions[6],
        open: sessions[7],
        "authenticated-500": sessions[15],
      }[mode];
      assert.ok(session, `fixture sanity: no scratch session for mode ${mode}`);
      writeStubConfig(record, mode === "healthy" ? "serve" : mode);
      const env = baseEnv(session, {
        PIWEB_HOST: "127.0.0.1",
        PIWEB_PORT: String(port),
        PI_WEB_PASSWORD: PASSWORD,
        PI_WEB_ALLOWED_HOSTS: "pi.example.test",
        GHSTUB_LOG: ghLog,
        GHSTUB_LATEST_TAG: LATEST_TAG,
        GHSTUB_PAYLOAD: PAYLOAD,
        GHSTUB_HASH: HASH,
        GHSTUB_CHECKSUM_MODE: "match",
      });
      const result = runDeploy({ lane: true, args: [LATEST_TAG, "--confirm", LATEST_TAG] }, env);
      await check({ result, port, record });
    });
  };

  await ac3({
    title: "AC3: a healthy service (login 200, anonymous API 401) deploys",
    mode: "healthy",
    check: async ({ result, port }) => {
      assert.equal(result.status, 0, `a deploy whose service answers /login 200 and denies the anonymous API must succeed\n${output(result)}`);
      assert.match(result.stdout, /^state=deployed$/m, "a verified healthy deploy must report state=deployed");
      await waitForStatus(`http://127.0.0.1:${port}/login`, 200);
      assert.equal(await httpStatus(`http://127.0.0.1:${port}/api/sessions`), 401);
    },
  });

  await ac3({
    title: "AC3: an unreachable configured service fails the deploy",
    mode: "unreachable",
    check: async ({ result, port }) => {
      assert.notEqual(
        result.status,
        0,
        `a deploy whose service never answers on the configured port ${port} must fail\n${output(result)}`,
      );
      assert.doesNotMatch(output(result), /^state=deployed$/m, "an unreachable service must not be reported as deployed");
      // (#160 s5#9): a probe failure after the swap is a change.
      assert.match(result.stdout, /^state=changed$/m, `a failed post-swap probe must report state=changed\n${output(result)}`);
      assert.match(result.stdout, /^installed_version=/m, `state=changed must carry installed_version\n${output(result)}`);
      assert.doesNotMatch(result.stdout, /^state=not_changed$/m, "a swapped service is never reported as not_changed");
    },
  });

  await ac3({
    title: "AC3: an unauthenticated-accessible service fails the deploy",
    mode: "open",
    check: async ({ result, port }) => {
      assert.notEqual(
        result.status,
        0,
        `a deploy whose API answers without authentication must fail (configured password; observed at ${port})\n${output(result)}`,
      );
      assert.doesNotMatch(output(result), /^state=deployed$/m, "an unauthenticated service must not be reported as deployed");
    },
  });

  await ac3({
    title: "AC3/a login-page 200 hides an authenticated / that answers 500; the deploy fails",
    mode: "authenticated-500",
    check: async ({ result, port }) => {
      // /login stays 200 and the anonymous API stays 401, exactly the shape
      // that let the v0.1.7 incident pass a deploy while every logged-in page
      // was a 500. Only an authenticated GET / exposes it.
      await waitForStatus(`http://127.0.0.1:${port}/login`, 200);
      assert.equal(await httpStatus(`http://127.0.0.1:${port}/api/sessions`), 401);
      assert.notEqual(
        result.status,
        0,
        `a deploy whose authenticated / answers 5xx must fail (observed at ${port})\n${output(result)}`,
      );
      assert.doesNotMatch(output(result), /^state=deployed$/m, "a service with a broken authenticated page must not be reported as deployed");
    },
  });

  // --- deploy.env inline comments and its mode --------------
  await scenario("an unquoted trailing # comment in deploy.env is not part of the value", async () => {
    const port = await freePort();
    const record = join(scratch, "inline.jsonl");
    rmSync(record, { force: true });
    writeDeployEnv(
      [
        `PIWEB_PORT=${port}  # the service port`,
        `PI_WEB_PASSWORD="${PASSWORD}#kept" # a quoted value keeps its #`,
        "PI_WEB_ALLOWED_HOSTS=pi.example.test\t# tab before the comment",
        "",
      ].join("\n"),
    );
    writeStubConfig(record, "serve");
    const result = runDeploy({ args: [] }, baseEnv(sessions[11]));
    assert.equal(result.status, 0, `a deploy.env with inline comments must deploy\n${output(result)}`);
    const found = await waitForRecords(record, 1);
    assert.ok(found.length > 0, "the deployed service must have started");
    const service = found.at(-1);
    assert.equal(service.port, port, `the port must be read without its comment; observed argv ${JSON.stringify(service.argv)}`);
    assert.equal(service.env.PI_WEB_PASSWORD, `${PASSWORD}#kept`, "a quoted value keeps its content, # included");
    assert.equal(service.env.PI_WEB_ALLOWED_HOSTS, "pi.example.test", "the comment after a tab is stripped too");
    await waitForStatus(`http://127.0.0.1:${port}/login`, 200);
  });

  await scenario("a deploy.env that is not 0600 warns once and still deploys", async () => {
    const port = await freePort();
    const record = join(scratch, "mode644.jsonl");
    rmSync(record, { force: true });
    rmSync(npmLog, { force: true });
    rmSync(ghLog, { force: true });
    writeDeployEnv([`PIWEB_PORT=${port}`, `PI_WEB_PASSWORD=${PASSWORD}`, ""].join("\n"), 0o644);
    writeStubConfig(record, "serve");
    const env = baseEnv(sessions[12], {
      PIWEB_HOST: "127.0.0.1",
      GHSTUB_LOG: ghLog,
      GHSTUB_LATEST_TAG: LATEST_TAG,
      GHSTUB_PAYLOAD: PAYLOAD,
      GHSTUB_HASH: HASH,
      GHSTUB_CHECKSUM_MODE: "match",
    });
    const result = runDeploy({ lane: true, args: [LATEST_TAG, "--confirm", LATEST_TAG] }, env);
    const report = output(result);
    assert.equal(result.status, 0, `a 0644 deploy.env must not fail the deploy\n${report}`);
    assert.match(result.stdout, /^state=deployed$/m, "a 0644 deploy.env still reports state=deployed");
    const warnings = report.match(/^warn: .*deploy\.env.*$/gm) ?? [];
    assert.equal(warnings.length, 1, `exactly one deploy.env warning line\n${report}`);
    assert.match(warnings[0], /\b0?644\b/, `the warning names the file's mode: ${warnings[0]}`);
    assert.match(warnings[0], /\b0600\b/, `the warning names the expected mode: ${warnings[0]}`);
    assert.ok(!report.includes(PASSWORD), "no deploy.env value is ever printed");
  });

  // --- a host without curl names the missing dependency -----
  await scenario("without curl the service probe fails naming curl, not the service", async () => {
    writeDeployEnv(null);
    const port = await freePort();
    const record = join(scratch, "nocurl-probe.jsonl");
    rmSync(record, { force: true });
    writeStubConfig(record, "serve");
    const env = baseEnv(sessions[13], {
      PATH: curllessPath,
      PIWEB_HOST: "127.0.0.1",
      PIWEB_PORT: String(port),
      PI_WEB_PASSWORD: PASSWORD,
      PI_WEB_ALLOWED_HOSTS: "edge.example.test",
    });
    const started = Date.now();
    const result = runDeploy({ args: [] }, env);
    const report = output(result);
    assert.equal(result.status, 3, `an unverifiable swap exits 3 (the lane reports state=changed)\n${report}`);
    assert.match(result.stderr, /curl is not installed/, `the probe failure names curl\n${report}`);
    assert.doesNotMatch(report, /did not answer GET \/login/, `the service is not blamed for a missing curl\n${report}`);
    assert.ok(Date.now() - started < 15_000, "a missing curl fails at once, not after the probe's 20 s wait");
  });

  await scenario("without curl the TLS edge probe is skipped with a line naming curl", async () => {
    writeDeployEnv(null);
    const port = await freePort();
    const record = join(scratch, "nocurl-edge.jsonl");
    rmSync(record, { force: true });
    writeStubConfig(record, "serve");
    const env = baseEnv(sessions[14], {
      PATH: curllessPath,
      PIWEB_HOST: "127.0.0.1",
      PIWEB_PORT: String(port),
      PI_WEB_ALLOWED_HOSTS: "edge.example.test,second.example.test",
    });
    const result = runDeploy({ args: [] }, env);
    const report = output(result);
    assert.equal(result.status, 0, `a skipped edge probe never fails the deploy\n${report}`);
    assert.equal(
      (result.stdout.match(/^warn: TLS edge probe skipped: curl is not installed \(edge\.example\.test\)$/gm) ?? []).length,
      1,
      `exactly one line naming the missing curl\n${report}`,
    );
    assert.doesNotMatch(report, /warn: TLS edge unreachable/, `the edge is not blamed for a missing curl\n${report}`);
  });

  // --- warn-only TLS edge probe ------------------------------
  const EDGE_WARN = /^warn: TLS edge unreachable \(edge\.example\.test\)$/gm;
  const edge = async ({ title, kind, httpsMode, allowedHosts, check }) => {
    await scenario(title, async () => {
      writeDeployEnv(null);
      const port = await freePort();
      const record = join(scratch, `edge-${kind}.jsonl`);
      rmSync(record, { force: true });
      rmSync(curlLog, { force: true });
      rmSync(npmLog, { force: true });
      rmSync(ghLog, { force: true });
      const session = sessions[8 + ["reach", "refuse", "none"].indexOf(kind)];
      writeStubConfig(record, "serve", httpsMode);
      const env = baseEnv(session, {
        PIWEB_HOST: "127.0.0.1",
        PIWEB_PORT: String(port),
        PI_WEB_PASSWORD: PASSWORD,
        ...(allowedHosts === undefined ? {} : { PI_WEB_ALLOWED_HOSTS: allowedHosts }),
        GHSTUB_LOG: ghLog,
        GHSTUB_LATEST_TAG: LATEST_TAG,
        GHSTUB_PAYLOAD: PAYLOAD,
        GHSTUB_HASH: HASH,
        GHSTUB_CHECKSUM_MODE: "match",
      });
      const result = runDeploy({ lane: true, args: [LATEST_TAG, "--confirm", LATEST_TAG] }, env);
      await check({ result });
    });
  };

  await edge({
    title: "a reachable TLS edge (first allowed host) prints no warning",
    kind: "reach",
    httpsMode: "reach",
    allowedHosts: "edge.example.test,second.example.test",
    check: async ({ result }) => {
      const report = output(result);
      assert.equal(result.status, 0, `a deploy with a reachable edge must succeed\n${report}`);
      assert.match(result.stdout, /^state=deployed$/m, "a deploy with a reachable edge must report state=deployed");
      assert.doesNotMatch(report, /warn: TLS edge unreachable/, `a reachable edge must not warn\n${report}`);
      const requests = httpsRequests();
      assert.equal(requests.length, 1, `the edge must be probed exactly once; observed ${JSON.stringify(requests)}`);
      assert.match(requests[0], /(^| )https:\/\/edge\.example\.test\/( |$)/, `the first allowed host must be probed; observed ${requests[0]}`);
      const maxTime = Number(/(?:^| )--max-time ([0-9.]+)(?: |$)/.exec(requests[0])?.[1]);
      assert.ok(maxTime > 0 && maxTime <= 6, `the edge probe must carry a short --max-time; observed ${requests[0]}`);
      assert.ok(
        readLog(curlLog).every((line) => !line.includes("second.example.test")),
        `only the first allowed host may be probed; observed ${JSON.stringify(readLog(curlLog))}`,
      );
    },
  });

  await edge({
    title: "an unreachable TLS edge warns but the deploy still succeeds",
    kind: "refuse",
    httpsMode: "refuse",
    allowedHosts: "edge.example.test,second.example.test",
    check: async ({ result }) => {
      const report = output(result);
      assert.equal(result.status, 0, `an unreachable edge must not fail the deploy\n${report}`);
      assert.match(result.stdout, /^state=deployed$/m, "an unreachable edge must still report state=deployed");
      assert.equal(
        (result.stdout.match(EDGE_WARN) ?? []).length,
        1,
        `exactly one 'warn: TLS edge unreachable (edge.example.test)' line must be printed on stdout\n${report}`,
      );
      assert.match(result.stdout, /^==> Service probe OK: /m, "the local service probe must still have run and passed");
      assert.equal(httpsRequests().length, 1, `the edge must be probed exactly once; observed ${JSON.stringify(httpsRequests())}`);
    },
  });

  await edge({
    title: "no PI_WEB_ALLOWED_HOSTS skips the TLS edge probe with one line naming it",
    kind: "none",
    httpsMode: "reach",
    allowedHosts: undefined,
    check: async ({ result }) => {
      const report = output(result);
      assert.equal(result.status, 0, `a deploy without allowed hosts must succeed\n${report}`);
      assert.match(result.stdout, /^state=deployed$/m, "a deploy without allowed hosts must report state=deployed");
      assert.equal(
        (result.stdout.match(/^warn: TLS edge probe skipped: PI_WEB_ALLOWED_HOSTS is not set \(no edge host to probe\)$/gm) ?? []).length,
        1,
        `the skip must be announced with exactly one warning naming PI_WEB_ALLOWED_HOSTS\n${report}`,
      );
      assert.doesNotMatch(report, /warn: TLS edge unreachable/, `a skipped edge probe must not warn\n${report}`);
      assert.equal(httpsRequests().length, 0, `no https request may be made; observed ${JSON.stringify(httpsRequests())}`);
    },
  });

  if (failures.length > 0) {
    console.error(`${failures.length} scenario(s) failed: ${failures.join("; ")}`);
    process.exitCode = 1;
  } else {
    console.log("PASS: all deploy service-shape scenarios held");
  }
} finally {
  // Never leak the scratch sessions, stub processes, or temp files.
  for (const session of sessions) {
    spawnSync("tmux", ["kill-session", "-t", session], { encoding: "utf8", env: tmuxEnv() });
  }
  for (const file of recordFiles) {
    for (const record of records(join(scratch, file))) {
      spawnSync("kill", ["-9", String(record.pid)], { encoding: "utf8" });
    }
  }
  rmSync(scratch, { recursive: true, force: true });
}