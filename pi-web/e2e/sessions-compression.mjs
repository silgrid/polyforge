// carries: app/api/sessions/*, lib/json-response*, next.config.ts
// Online compression checks for the session routes (polyforge#105): since
// Next 16.4.5 the server strips an application-set Content-Encoding and, from
// 16.4.6 on, compresses large route-handler responses itself. pi-web therefore
// hands `next start` plain JSON and lets the server do the one and only gzip
// layer. These scenarios pin that contract over the wire, against a real
// server (no browser needed: raw `node:http` requests never decompress on
// their own, so the exact bytes and headers are observable):
//
//   S105-1  `Accept-Encoding: identity` — no content-encoding, the raw bytes
//           are JSON, and the body is large enough (>= 1024 bytes) that the
//           server's compression threshold is not what skips compression.
//   S105-2  `Accept-Encoding: gzip` — the body parses as JSON after at most
//           the one decompression the content-encoding header names, and a
//           response that does carry content-encoding varies on
//           `Accept-Encoding`. This is the anti-double-gzip guard: a tree
//           that self-compresses (the app-layer gzip this ticket removes)
//           answers with a body that is still gzip after one decompression,
//           or with no header while the body stays encoded, and both shapes
//           fail here.
//   S105-3  start mode only (`E2E_SERVER_MODE=start`): the server itself
//           compressed the response — content-encoding is exactly `gzip`,
//           one gunzip yields valid JSON, and `Vary` carries
//           `Accept-Encoding`. Under `next dev` the compression middleware
//           makes no promise, so dev runs judge S105-1/S105-2 only.
//
//   node e2e/sessions-compression.mjs
//   npm run test:e2e:all -- sessions-compression.mjs
//
// The suite needs no browser, but `next start` needs a production build:
// e2e/all.mjs builds once for a run that selects a browser suite, so a
// targeted start-mode run of this suite alone builds into its own dist dir
// first (the path next-server.mjs's noBuild error names) unless the runner
// already handed every suite a prebuilt build.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import http from "node:http";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { gunzipSync } from "node:zlib";
import { startNextServer } from "./next-server.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const mode = process.env.E2E_SERVER_MODE || "dev";
assert.ok(mode === "dev" || mode === "start", "E2E_SERVER_MODE must be dev or start");
process.env.PI_WEB_DIST_DIR ||= ".next/e2e/sessions-compression";
if (mode === "dev") {
  assert.ok(
    !existsSync(join(root, process.env.PI_WEB_DIST_DIR, "dev/lock")),
    "Run in a checkout without an active dev server",
  );
}

const SESSIONS = 6;
const FIRST_MESSAGE = "compressible session content for the online compression check ".repeat(8);

const failures = [];
const ran = new Set();
async function scenario(id, body) {
  ran.add(id);
  try {
    await body();
    console.log(`PASS ${id}`);
  } catch (error) {
    failures.push(`${id}: ${error?.message ?? error}`);
    console.error(`FAIL ${id}: ${error?.message ?? error}`);
  }
}

const varyHasAcceptEncoding = (headers) =>
  /(?:^|,\s*)accept-encoding(?:\s*,|$)/i.test(headers.vary ?? "");

/** Seeds throwaway sessions whose summary list comfortably clears 1 KB. */
function seedSessions(agentDir) {
  const sessionDir = join(agentDir, "sessions", "sessions-compression");
  mkdirSync(sessionDir, { recursive: true });
  const stamp = "2026-11-17T00:00:00.000Z";
  for (let i = 0; i < SESSIONS; i += 1) {
    const id = `compression-${i}`;
    const lines = [
      { type: "session", version: 3, id, timestamp: stamp, cwd: join(agentDir, "project") },
      { type: "message", id: "u1", parentId: null, timestamp: stamp, message: { role: "user", content: `${FIRST_MESSAGE} #${i}` } },
    ];
    writeFileSync(join(sessionDir, `${stamp.replaceAll(":", "-")}_${id}.jsonl`), `${lines.map((line) => JSON.stringify(line)).join("\n")}\n`);
  }
}

const agentDir = mkdtempSync(join(tmpdir(), "pi-web-sessions-compression-"));
seedSessions(agentDir);
const artifacts = join(root, "test-results/e2e");
mkdirSync(artifacts, { recursive: true });

/** In start mode with no prebuilt build handed down, build this suite's own. */
async function ensureStartBuild() {
  if (process.env.E2E_PREBUILT_DIST
      || existsSync(join(root, process.env.PI_WEB_DIST_DIR, "BUILD_ID"))) return;
  const scratchHome = mkdtempSync(join(tmpdir(), "pi-web-sessions-compression-build-"));
  const build = spawn(
    process.execPath,
    [join(root, "node_modules/next/dist/bin/next"), "build", "--webpack"],
    {
      cwd: root,
      env: { ...process.env, HOME: scratchHome, NEXT_TELEMETRY_DISABLED: "1" },
      stdio: "inherit",
    },
  );
  const code = await new Promise((resolve) => {
    build.once("error", () => resolve(1));
    build.once("close", resolve);
  });
  rmSync(scratchHome, { recursive: true, force: true });
  assert.equal(code, 0, `next build --webpack into ${process.env.PI_WEB_DIST_DIR} failed`);
}

let stop = () => {};
try {
  if (mode === "start") await ensureStartBuild();

  const started = await startNextServer({
    name: "sessions-compression",
    logFile: join(artifacts, "sessions-compression-server.log"),
    env: {
      ...process.env,
      PI_CODING_AGENT_DIR: agentDir,
      PI_WEB_PASSWORD: "",
      NEXT_TELEMETRY_DISABLED: "1",
    },
  });
  stop = started.stop;
  const { hostname, port } = new URL(started.base);

  /** A raw request that never decompresses: the exact bytes arrive as sent. */
  const request = (path, acceptEncoding) => new Promise((resolve, reject) => {
    const req = http.request(
      { host: hostname, port, path, headers: { "Accept-Encoding": acceptEncoding }, method: "GET" },
      (res) => {
        const chunks = [];
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("end", () => resolve({
          status: res.statusCode,
          headers: res.headers,
          body: Buffer.concat(chunks),
        }));
      },
    );
    req.on("error", reject);
    req.end();
  });

  await scenario("S105-1", async () => {
    const response = await request("/api/sessions?summary=1", "identity");
    assert.equal(response.status, 200);
    assert.equal(response.headers["content-encoding"], undefined,
      `identity request must not carry content-encoding (got ${JSON.stringify(response.headers["content-encoding"])})`);
    const payload = JSON.parse(response.body.toString("utf8"));
    assert.equal(payload.sessions.length, SESSIONS);
    // The compression threshold is 1024 bytes: below it the server legitimately
    // skips compression, and no later scenario may judge the compression.
    assert.ok(response.body.length >= 1024,
      `the summary list body is ${response.body.length} bytes, under the 1024-byte compression threshold`);
  });

  await scenario("S105-2", async () => {
    const response = await request("/api/sessions?summary=1", "gzip");
    assert.equal(response.status, 200);
    const encoding = response.headers["content-encoding"];
    let payload;
    if (encoding === "gzip") {
      assert.ok(varyHasAcceptEncoding(response.headers),
        "a compressed response must vary on Accept-Encoding");
      payload = JSON.parse(gunzipSync(response.body).toString("utf8"));
    } else {
      assert.equal(encoding, undefined,
        `unexpected content-encoding ${JSON.stringify(encoding)}: the body must parse after at most the one decompression it names`);
      payload = JSON.parse(response.body.toString("utf8"));
    }
    assert.equal(payload.sessions.length, SESSIONS);
  });

  if (mode === "start") {
    await scenario("S105-3", async () => {
      const response = await request("/api/sessions?summary=1", "gzip");
      assert.equal(response.status, 200);
      assert.equal(response.headers["content-encoding"], "gzip",
        `next start must itself compress the >=1KB list (got ${JSON.stringify(response.headers["content-encoding"])})`);
      const payload = JSON.parse(gunzipSync(response.body).toString("utf8"));
      assert.equal(payload.sessions.length, SESSIONS);
      assert.ok(varyHasAcceptEncoding(response.headers),
        "the compressed response must vary on Accept-Encoding");
    });
  } else {
    console.log("SKIP S105-3 (dev mode: the compression middleware makes no promise under next dev)");
  }

  if (failures.length === 0 && ran.size > 0) {
    console.log("sessions-compression: all scenarios passed");
  } else {
    process.exitCode = 1;
  }
} finally {
  await stop();
  rmSync(agentDir, { recursive: true, force: true });
}
