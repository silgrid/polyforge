// carries: lib/harness/diagnostics.ts, lib/harness/claude.ts, lib/harness/pi.ts, lib/harness/concepts.ts, app/api/harness/[harness]/diagnose/route.ts
// polyforge#149 (#85 85a1b, AC6): the native diagnostics a harness declares
// are executed only as the fixed safe command (`claude doctor`), with a
// deadline, without a terminal (stdin closed at once, so an interactive
// prompt cannot block), and through a filter that never echoes a credential
// or environment variable value. Expected masks come from the ticket (AC6)
// and lib/mcp-secrets' own rules; the planted credential shapes exist to be
// found absent.
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-harness-diagnose-"));
const binDir = path.join(scratch, "bin");
fs.mkdirSync(binDir);

/** A stub diagnostic command: a node script with a chosen behaviour. */
function stubBinary(name, body) {
  const file = path.join(binDir, name);
  fs.writeFileSync(file, `#!${process.execPath}\n${body}\n`);
  fs.chmodSync(file, 0o755);
  return file;
}

test.after(() => fs.rmSync(scratch, { recursive: true, force: true }));

const {
  CLAUDE_DIAGNOSTIC_ARGV,
  DIAGNOSTIC_DEADLINE_MS,
  maskDiagnosticLine,
  claudeDiagnose,
} = await jiti.import("./diagnostics.ts");
const { createClaudeAdapter } = await jiti.import("./claude.ts");
const { createPiAdapter } = await jiti.import("./pi.ts");
const diagnoseRoute = await jiti.import("@/app/api/harness/[harness]/diagnose/route.ts");
const { allowFileRoot } = await jiti.import("../file-access.ts");

// The credential-style outputs a stub command might print: every shape the
// filter must mask, planted verbatim so the assertions find the raw forms.
const RAW_SECRETS = [
  "sk-live-abcdef0123456789abcdef",
  "ghp_abcdefghijklmnopqrstuvwxyz1234567890",
  "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
  "E2E-DIAG-PLAIN-VALUE-123",
];

test("the fixed safe command is claude doctor with a bounded deadline", async () => {
  assert.deepEqual(CLAUDE_DIAGNOSTIC_ARGV, ["doctor"]);
  assert.ok(DIAGNOSTIC_DEADLINE_MS >= 10_000 && DIAGNOSTIC_DEADLINE_MS <= 30_000, "the default deadline is out of the settled range");
});

test("a hanging command is cut at its deadline, keeping the lines it printed", async () => {
  const stub = stubBinary("hangs", `console.log("doctor: first line"); setInterval(() => {}, 1000);`);
  const started = Date.now();
  const result = await claudeDiagnose({ claudeBinary: stub }, { timeoutMs: 400 });
  assert.ok(result.available === true, `the hanging run answered unavailable: ${JSON.stringify(result)}`);
  assert.equal(result.timedOut, true, "the deadline did not mark the run timed out");
  assert.deepEqual(result.lines, ["doctor: first line"]);
  assert.ok(Date.now() - started < 5_000, "the run outlived its deadline by seconds");
});

test("an interactive prompt cannot block: stdin is closed at once, and the deadline bounds the rest", async () => {
  // A prompt that reads stdin gets EOF at once (stdio ignore, never a TTY) and exits.
  const eof = stubBinary("prompt-exits", `process.stdin.resume(); process.stdin.once("end", () => { console.log("doctor: done after EOF"); process.exit(0); });`);
  const quick = await claudeDiagnose({ claudeBinary: eof }, { timeoutMs: 2_000 });
  assert.ok(quick.available === true);
  assert.equal(quick.timedOut, false, "an EOF-answered prompt was not left alone");
  assert.deepEqual(quick.lines, ["doctor: done after EOF"]);
  // A prompt that waits past EOF is bounded by the deadline all the same.
  const stubborn = stubBinary("prompt-hangs", `process.stdin.resume(); setInterval(() => {}, 1000);`);
  const bounded = await claudeDiagnose({ claudeBinary: stubborn }, { timeoutMs: 400 });
  assert.ok(bounded.available === true);
  assert.equal(bounded.timedOut, true, "a prompt waiting past EOF outlived the deadline");
});

test("the diagnostics output never echoes a credential value", async () => {
  for (const line of [
    `ANTHROPIC_API_KEY=${RAW_SECRETS[0]}`,
    `token: ${RAW_SECRETS[1]}`,
    `Authorization: Bearer ${RAW_SECRETS[2]}`,
    `GEMINI_API_KEY: ${RAW_SECRETS[3]}`,
  ]) {
    const masked = maskDiagnosticLine(line);
    for (const secret of RAW_SECRETS) {
      assert.ok(!masked.includes(secret), `the masked line leaks ${secret}: ${masked}`);
    }
    assert.ok(masked.includes("•••"), `nothing was masked in: ${masked}`);
  }
  // Ordinary output passes through untouched.
  assert.equal(maskDiagnosticLine("doctor: installation healthy, claude 2.1.322"), "doctor: installation healthy, claude 2.1.322");
  // A plain value under a credential-named variable is masked too (a value is a value).
  assert.match(maskDiagnosticLine("ANTHROPIC_API_KEY=a-plain-little-value"), /ANTHROPIC_API_KEY=•••/);
});

test("the filter masks every environment-variable-shaped value and URL userinfo (r1#2)", async () => {
  // AC6: 不回显凭据或环境变量值——变量的值与名字是否像凭据无关。
  const masked = (line) => maskDiagnosticLine(line);
  assert.equal(masked("ANTHROPIC_BASE_URL=https://proxy.corp/nonsecret"), "ANTHROPIC_BASE_URL=\u2022\u2022\u2022", "a non-credential variable's value echoed");
  assert.equal(masked("CLAUDE_CODE_USE_BEDROCK: 1"), "CLAUDE_CODE_USE_BEDROCK: \u2022\u2022\u2022", "a colon-spelled variable's value echoed");
  // A proxy URL's userinfo (user:pass@) is a credential wherever it sits.
  const proxy = masked("HTTPS_PROXY=http://user:p4ss@proxy.internal:8080");
  assert.ok(!proxy.includes("user:p4ss"), `the proxy URL's userinfo echoed: ${proxy}`);
  assert.ok(proxy.includes("\u2022\u2022\u2022@"), `the masked proxy URL lost its marker: ${proxy}`);
  assert.ok(proxy.includes("proxy.internal:8080"), `the masked proxy URL lost its host: ${proxy}`);
  // A bare occurrence of a set variable's value is masked through the run's env.
  const planted = "E2E-DIAG-ENV-VALUE-987654321";
  const env = { E2E_DIAG_SECRET_TOKEN: planted, E2E_DIAG_SHORT: "short" };
  const line = masked(`configuration notes: ${planted} and E2E_DIAG_SHORT stays`, env);
  assert.ok(!line.includes(planted), `a set variable's value echoed: ${line}`);
  assert.ok(line.includes("E2E_DIAG_SHORT stays"), `a short value was masked anyway: ${line}`);
  // Ordinary words, paths and versions still pass through untouched.
  assert.equal(masked("doctor: installation healthy, claude 2.1.322 at /usr/local/bin"), "doctor: installation healthy, claude 2.1.322 at /usr/local/bin");
});

test("a run's output masks the three shapes a stub might print (r1#2)", async () => {
  const planted = "E2E-DIAG-ENV-VALUE-987654321";
  const stub = stubBinary("prints-env", [
    `console.log("ANTHROPIC_BASE_URL=https://proxy.corp/nonsecret");`,
    `console.log("HTTPS_PROXY=http://user:p4ss@proxy.internal:8080");`,
    `console.log("note " + process.env.E2E_DIAG_SECRET_TOKEN);`,
    `console.log("doctor: ordinary line 2.1.322");`,
  ].join("\n"));
  const result = await claudeDiagnose({ claudeBinary: stub, env: { E2E_DIAG_SECRET_TOKEN: planted } }, { timeoutMs: 5_000 });
  assert.ok(result.available === true);
  const text = result.lines.join("\n");
  for (const raw of ["https://proxy.corp/nonsecret", "user:p4ss", planted]) {
    assert.ok(!text.includes(raw), `the run's output echoes ${raw}: ${text}`);
  }
  assert.ok(result.lines.includes("doctor: ordinary line 2.1.322"), "an ordinary line was dropped or masked");
});

test("a run's whole filtered output, the caps, and the unavailable answers", async () => {
  const printing = stubBinary("prints", [
    `console.log("ANTHROPIC_API_KEY=${RAW_SECRETS[0]}");`,
    `console.log("doctor: second line");`,
    `for (let i = 0; i < 600; i++) console.log("doctor: filler " + i);`,
  ].join("\n"));
  const result = await claudeDiagnose({ claudeBinary: printing }, { timeoutMs: 5_000 });
  assert.ok(result.available === true);
  assert.equal(result.truncated, true, "an oversized output was not marked truncated");
  assert.ok(result.lines.length < 600, "the line cap did not bound the output");
  for (const line of result.lines) {
    for (const secret of RAW_SECRETS) assert.ok(!line.includes(secret), `the filtered output leaks ${secret}`);
  }
  assert.ok(result.lines.includes("doctor: second line"), "an ordinary line was dropped by the filter");
  assert.match(result.lines[0], /ANTHROPIC_API_KEY=•••/);

  // A binary that is not there answers unavailable, with the reason.
  const missing = await claudeDiagnose({ claudeBinary: path.join(binDir, "not-there") }, { timeoutMs: 1_000 });
  assert.ok(missing.available === false, "a missing binary answered available");
  assert.ok(missing.reason.length > 0, "the unavailable answer carries no reason");

  // pi declares no fixed safe diagnostic command yet: the reason, never a guess.
  const pi = createPiAdapter();
  const piResult = await pi.diagnose?.({});
  assert.deepEqual(piResult, { available: false, reason: "pi has no fixed safe diagnostic command yet" });

  // The claude adapter runs the fixed command through the same runner.
  const adapter = createClaudeAdapter();
  assert.equal(adapter.capabilities.nativeDiagnostics.implemented, true, "claude's diagnostics are declared unimplemented");
  const throughAdapter = await adapter.diagnose?.({ claudeBinary: stubBinary("via-adapter", `console.log("doctor: via the adapter");`) }, undefined);
  assert.ok(throughAdapter?.available === true);
  assert.deepEqual(throughAdapter.command, ["claude", "doctor"]);
});

test("the diagnose route guards, and answers filtered diagnostics", async () => {
  const project = path.join(scratch, "project");
  fs.mkdirSync(project);
  globalThis.__piAllowedRootsCache = undefined;
  allowFileRoot(project);
  const request = (url, headers = {}) => new Request(`http://localhost${url}`, { headers: { host: "localhost", ...headers } });
  const params = (harness) => Promise.resolve({ harness });

  // The API guard and the unknown harness come before anything runs.
  const crossSite = await diagnoseRoute.GET(request("/api/harness/claude/diagnose", { "sec-fetch-site": "cross-site" }), { params: params("claude") });
  assert.equal(crossSite.status, 403);
  const unknown = await diagnoseRoute.GET(request("/api/harness/nope/diagnose"), { params: params("nope") });
  assert.equal(unknown.status, 404);

  // A cwd outside the allowed roots is refused.
  const denied = await diagnoseRoute.GET(request(`/api/harness/claude/diagnose?cwd=${encodeURIComponent(path.join(scratch, "outside"))}`), { params: params("claude") });
  assert.equal(denied.status, 403);

  // pi answers its unavailable reason through the route.
  const piResponse = await diagnoseRoute.GET(request("/api/harness/pi/diagnose"), { params: params("pi") });
  assert.equal(piResponse.status, 200);
  const piBody = await piResponse.json();
  assert.deepEqual(piBody, { harness: "pi", diagnostics: { available: false, reason: "pi has no fixed safe diagnostic command yet" } });
});
