// Shared `next dev`/`next start` lifecycle for the e2e suites.
//
// The suites used to copy the same probe/spawn/ready/stop code. Under the
// parallel runner that copy has a real race: the port is free when the probe
// closes, but another suite can take it before `next` binds it. This helper
// re-probes and retries a bounded number of times when the child exits before
// it is ready or reports EADDRINUSE.
import { spawn } from "node:child_process";
import { once } from "node:events";
import { cpSync, createWriteStream, existsSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const ATTEMPTS = 3;
const READY_TIMEOUT_MS = 120_000;

// build-once mode. E2E_SERVER_MODE is the one process-wide
// switch and the only one: `dev` (the default) runs `next dev` in the suite's
// own dist dir; `start` runs `next start` against a production build that
// already exists. No caller chooses its own mode, so every suite, a new one
// included, runs in whichever mode the run asked for.
const MODES = ["dev", "start"];
function serverMode() {
  const mode = process.env.E2E_SERVER_MODE || "dev";
  if (!MODES.includes(mode)) throw new Error(`next-server: E2E_SERVER_MODE must be dev or start, got ${JSON.stringify(mode)}`);
  return mode;
}

// The dist dir `next start` serves. With E2E_PREBUILT_DIST (set by e2e/all.mjs
// after its one `next build`, or by hand; relative to the package root) every
// server gets a private copy of that build in its own runner-owned dist dir
// (`.next/e2e/<suite>`, or the dir a multi-server suite nests below it):
// `next start` writes its route cache into the dir it serves
// (server/route-cache, the meta file with a plain non-atomic write), so two
// servers must not serve one dir. The copy leaves out webpack's cache/ (54 MB,
// about 50 ms) and the runner's per-suite cleanup removes it with the suite's
// dir; the prebuilt dir itself is only read. Any other dist dir is served in
// place: without E2E_PREBUILT_DIST that is the suite's PI_WEB_DIST_DIR, else
// `.next`, which keeps CI's `npm run build && E2E_SERVER_MODE=start npm run
// test:e2e` unchanged. The child gets the result as PI_WEB_DIST_DIR, so
// next.config.ts resolves the distDir the build wrote.
const runnerDistRoot = resolve(root, ".next/e2e");
function runnerOwned(distDir) {
  const rel = relative(runnerDistRoot, resolve(root, distDir));
  return rel !== "" && !rel.startsWith("..") && !isAbsolute(rel);
}
const hasBuild = (distDir) => existsSync(resolve(root, distDir, "BUILD_ID"));
function noBuild(name, distDir) {
  return new Error(`${name ?? "next"}: next start needs a production build in ${distDir} (no BUILD_ID): `
    + "let e2e/all.mjs build once (E2E_SERVER_MODE=start npm run test:e2e:all), "
    + `or build it (PI_WEB_DIST_DIR=${distDir} npm run build), or point E2E_PREBUILT_DIST at a build`);
}
function prepareStartDist({ name, env = process.env } = {}) {
  const own = env.PI_WEB_DIST_DIR || ".next";
  const prebuilt = process.env.E2E_PREBUILT_DIST;
  if (!prebuilt || !runnerOwned(own)) {
    const distDir = prebuilt || own;
    if (!hasBuild(distDir)) throw noBuild(name, distDir);
    return distDir;
  }
  if (!hasBuild(prebuilt)) throw noBuild(name, prebuilt);
  const source = resolve(root, prebuilt);
  const target = resolve(root, own);
  rmSync(target, { recursive: true, force: true });
  cpSync(source, target, { recursive: true, filter: (src) => src !== join(source, "cache") });
  return own;
}

function pidAlive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return error.code === "EPERM";
  }
}

async function freePort() {
  const probe = createServer();
  probe.listen(0, "127.0.0.1");
  await once(probe, "listening");
  const { port } = probe.address();
  await new Promise((resolve, reject) => probe.close((error) => (error ? reject(error) : resolve())));
  return port;
}

/**
 * Start a Next server for one suite and wait for the same readiness signal the
 * suites used (a successful `GET /api/sessions`). Returns `{ base, server, stop }`.
 *
 * `env` is passed to the child as-is, so each suite keeps its own process-global
 * server environment; in start mode only its PI_WEB_DIST_DIR may change, to the
 * dir `next start` serves (prepareStartDist). `logFile` receives stdout and stderr.
 */
export async function startNextServer({ name, env = process.env, logFile }) {
  const mode = serverMode();
  let distDir = env.PI_WEB_DIST_DIR || ".next";
  if (mode === "start") {
    distDir = prepareStartDist({ name, env });
    if ((env.PI_WEB_DIST_DIR || ".next") !== distDir) env = { ...env, PI_WEB_DIST_DIR: distDir };
  }
  const log = createWriteStream(logFile);
  let lastError;

  for (let attempt = 1; attempt <= ATTEMPTS; attempt += 1) {
    const port = await freePort();
    const base = `http://127.0.0.1:${port}`;
    const server = spawn(
      process.execPath,
      [join(root, "node_modules/next/dist/bin/next"), mode, "-H", "127.0.0.1", "-p", String(port)],
      { cwd: root, env, stdio: ["ignore", "pipe", "pipe"] },
    );
    let output = "";
    const exited = new Promise((resolve) => {
      server.once("exit", (code, signal) => resolve({ code, signal }));
      server.once("error", (error) => resolve({ code: null, signal: null, error }));
    });
    let exitInfo = null;
    void exited.then((info) => { exitInfo = info; });
    const collect = (chunk) => {
      output += chunk;
      log.write(chunk);
    };
    server.stdout.on("data", collect);
    server.stderr.on("data", collect);

    const stop = async ({ endLog = false } = {}) => {
      if (server.exitCode === null && server.signalCode === null) {
        const forceKill = setTimeout(() => { try { server.kill("SIGKILL"); } catch { /* already gone */ } }, 10_000);
        try { server.kill("SIGTERM"); } catch { /* already gone */ }
        await exited;
        clearTimeout(forceKill);
      }
      // A SIGTERM'd `next dev` can leave its lock behind (written by this server or by
      // a child of it, so the pid need not be server.pid); drop it when it is this
      // server's or its pid is no longer alive. A live other server's lock stays.
      const lock = join(root, distDir, "dev/lock");
      try {
        const { pid } = JSON.parse(readFileSync(lock, "utf8"));
        if (pid === server.pid || !pidAlive(pid)) rmSync(lock, { force: true });
      } catch { /* no lock, or unreadable */ }
      if (endLog) await new Promise((resolve) => log.end(resolve));
    };

    const deadline = Date.now() + READY_TIMEOUT_MS;
    let failure = null;
    while (true) {
      if (exitInfo || server.exitCode !== null || server.signalCode !== null) {
        failure = exitInfo?.error
          ? `failed to spawn: ${exitInfo.error.message}`
          : `exited before readiness (code ${server.exitCode}, signal ${server.signalCode})`;
        break;
      }
      if (output.includes("EADDRINUSE")) {
        failure = `port ${port} was taken before Next bound it`;
        break;
      }
      const response = await fetch(`${base}/api/sessions`, { signal: AbortSignal.timeout(5000) }).catch(() => null);
      if (response?.ok) {
        let stopped = false;
        return {
          base,
          server,
          stop: async () => {
            if (stopped) return;
            stopped = true;
            await stop({ endLog: true });
          },
        };
      }
      if (Date.now() >= deadline) {
        failure = "readiness timed out";
        break;
      }
      await delay(250);
    }

    await stop();
    const tail = output.trim().split("\n").slice(-20).join("\n");
    lastError = new Error(`${name ?? "next"}: ${failure} (attempt ${attempt}/${ATTEMPTS}); see ${logFile}${tail ? `\n${tail}` : ""}`);
  }

  await new Promise((resolve) => log.end(resolve));
  throw lastError;
}
