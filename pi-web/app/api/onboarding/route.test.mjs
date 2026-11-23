// carries: app/api/onboarding/*, lib/onboarding-state.ts
// polyforge#167 (91-onboarding) AC1: the state API's image gate, its closed
// whitelist and its per-step writes. Expected bodies come from the ticket
// ("状态 API 不返回任何凭据值", "非镜像模式的 Pi Web 上不出现") and
// docs/workstation.md's first-boot field list — a credential sentinel in the
// record must not appear in any response or in the state file.
import "#test-env";
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { GET, POST } = await jiti.import("./route.ts");

const work = mkdtempSync(join(tmpdir(), "pf-onboarding-route-"));
const agentDir = join(work, "agent");
const dataRoot = join(work, "data");
const seedRoot = join(work, "seed");
const absentSeed = join(work, "host-mode");
mkdirSync(join(seedRoot, "releases", "v0.2.4"), { recursive: true });
writeFileSync(join(seedRoot, "releases", "v0.2.4", "manifest.json"), "{}\n");
mkdirSync(agentDir, { recursive: true });
mkdirSync(dataRoot, { recursive: true });

const saved = {
  agentDir: process.env.PI_CODING_AGENT_DIR,
  dataDir: process.env.POLYFORGE_DATA_DIR,
  seed: globalThis.__piWebTestSeedRoot,
};
function useEnv({ image = true } = {}) {
  process.env.PI_CODING_AGENT_DIR = agentDir;
  process.env.POLYFORGE_DATA_DIR = dataRoot;
  globalThis.__piWebTestSeedRoot = image ? seedRoot : absentSeed;
}
useEnv();
test.after(() => {
  process.env.PI_CODING_AGENT_DIR = saved.agentDir;
  process.env.POLYFORGE_DATA_DIR = saved.dataDir;
  globalThis.__piWebTestSeedRoot = saved.seed;
  if (saved.agentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  if (saved.dataDir === undefined) delete process.env.POLYFORGE_DATA_DIR;
  rmSync(work, { recursive: true, force: true });
});

const request = (path = "/api/onboarding", init = {}) => new Request(`http://127.0.0.1:30141${path}`, {
  ...init,
  headers: { host: "127.0.0.1:30141", ...(init.body ? { "content-type": "application/json" } : {}), ...(init.headers ?? {}) },
});
const post = (body, init = {}) => POST(request("/api/onboarding", { method: "POST", body: typeof body === "string" ? body : JSON.stringify(body), ...init }));
const statePath = join(agentDir, "pi-web-onboarding.json");
const stored = () => JSON.parse(readFileSync(statePath, "utf8"));

test("the empty image-mode state names the four steps, the first step and no connection", async () => {
  useEnv();
  rmSync(statePath, { force: true });
  const response = await GET(request());
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), {
    imageManaged: true,
    version: 1,
    steps: { aihub: "incomplete", account: "incomplete", workspace: "incomplete", ticket: "incomplete" },
    nextStep: "aihub",
    dismissed: false,
    connection: null,
  });
});

test("the AIHub connection is the first-boot record's whitelist, and no credential value rides any response or the file", async () => {
  useEnv();
  rmSync(statePath, { force: true });
  mkdirSync(join(dataRoot, "workstation"), { recursive: true });
  writeFileSync(join(dataRoot, "workstation", "first-boot.json"), JSON.stringify({
    phase: "done",
    mode: "external",
    defaultCell: "default",
    server: "https://aihub.example.test:8420",
    project: "polyforge",
    userReady: true,
    keyReady: true,
    error: "pf: token=SECRET-ERROR-SENTINEL",
    apiKey: "SECRET-API-KEY-SENTINEL",
    key: "SECRET-KEY-SENTINEL",
    access_token: "SECRET-ACCESS-TOKEN-SENTINEL",
  }));
  const response = await GET(request());
  const text = await response.text();
  const body = JSON.parse(text);
  assert.deepEqual(body.connection, {
    phase: "done",
    mode: "external",
    defaultCell: "default",
    server: "https://aihub.example.test:8420",
    project: "polyforge",
    userReady: true,
    keyReady: true,
  });
  for (const sentinel of ["SECRET-ERROR-SENTINEL", "SECRET-API-KEY-SENTINEL", "SECRET-KEY-SENTINEL", "SECRET-ACCESS-TOKEN-SENTINEL"]) {
    assert.equal(text.includes(sentinel), false, `the GET response leaked ${sentinel}`);
  }

  await post({ action: "complete", step: "aihub" });
  const afterText = readFileSync(statePath, "utf8");
  for (const sentinel of ["SECRET-ERROR-SENTINEL", "SECRET-API-KEY-SENTINEL", "SECRET-KEY-SENTINEL", "SECRET-ACCESS-TOKEN-SENTINEL"]) {
    assert.equal(afterText.includes(sentinel), false, `the state file leaked ${sentinel}`);
  }
  rmSync(join(dataRoot, "workstation", "first-boot.json"), { force: true });
});

test("each write merges by step: complete/skip/dismiss/reopen and the persisted file", async () => {
  useEnv();
  rmSync(statePath, { force: true });
  const complete = await post({ action: "complete", step: "aihub" });
  assert.equal(complete.status, 200);
  assert.equal((await complete.json()).steps.aihub, "complete");
  assert.equal(stored().steps.aihub, true);

  const second = await post({ action: "complete", step: "workspace" });
  assert.deepEqual((await second.json()).steps, { aihub: "complete", account: "incomplete", workspace: "complete", ticket: "incomplete" });
  assert.deepEqual(stored().steps, { aihub: true, account: false, workspace: true, ticket: false }, "the second write kept the first step");

  const skip = await post({ action: "skip", step: "account" });
  const skipped = await skip.json();
  assert.equal(skipped.steps.account, "incomplete", "a skipped step stays 「未完成」");
  assert.equal(skipped.nextStep, "account", "the skipped step is still the first unfinished one");

  const dismissed = await post({ action: "dismiss" });
  assert.equal((await dismissed.json()).dismissed, true);
  assert.equal(stored().dismissed, true);
  const reopened = await post({ action: "reopen" });
  const reopenedBody = await reopened.json();
  assert.equal(reopenedBody.dismissed, false);
  assert.equal(reopenedBody.nextStep, "account", "reopening starts at the first unfinished step (Q6)");
});

test("the request contract: gate, JSON-only, action and step validated before any write", async () => {
  useEnv();
  rmSync(statePath, { force: true });
  const untrusted = await GET(new Request("http://evil.example.test/api/onboarding", { headers: { host: "evil.example.test" } }));
  assert.equal(untrusted.status, 403);
  assert.deepEqual(await untrusted.json(), { error: "Forbidden" }, "the 403 names no machine fact");

  const notJson = await POST(request("/api/onboarding", { method: "POST", body: JSON.stringify({ action: "dismiss" }), headers: { "content-type": "text/plain" } }));
  assert.equal(notJson.status, 415);

  const torn = await post("{torn");
  assert.equal(torn.status, 400);
  assert.equal((await post({ action: "explode" })).status, 400);
  assert.equal((await post({ action: "complete" })).status, 400, "complete without a step is refused");
  assert.equal((await post({ action: "complete", step: "future" })).status, 400, "an unknown step is refused");
  assert.equal((await post({ action: "skip" })).status, 400, "skip names the step it advances past");
  assert.equal(existsSync(statePath), false, "no rejected request wrote the state file");
});

test("outside the image the API answers one boolean and never the wizard's state facts", async () => {
  useEnv({ image: false });
  rmSync(statePath, { force: true });
  const response = await GET(request());
  assert.equal(response.status, 200);
  const text = await response.text();
  assert.deepEqual(JSON.parse(text), { imageManaged: false });
  for (const fact of ["version", "steps", "nextStep", "dismissed", "connection", "aihub"]) {
    assert.equal(text.includes(fact), false, `host mode leaked the wizard fact ${fact}`);
  }
  const written = await post({ action: "complete", step: "aihub" });
  assert.equal(written.status, 403);
  assert.deepEqual(await written.json(), { error: "Forbidden" });
  assert.equal(existsSync(statePath), false, "host mode wrote no wizard state");
});
