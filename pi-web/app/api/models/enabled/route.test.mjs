// carries: app/api/models/enabled/*
// polyforge#170 (85a3-2, AC1, review r1#2): the enabledModels route writes
// through the pi adapter's version gate — the minimal edit lands in the native
// settings.json with the file's unrelated keys kept, and an adapter refusal
// (a settings.json whose top level is not an object) answers 409 with the
// file's bytes untouched. Fixtures are generated here; the expected patterns
// are hand-derived from the minimal-edit rule (a glob covering the switched-off
// model is expanded in place), never from the implementation's output.
import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after } from "node:test";
import { createJiti } from "jiti";

const root = await mkdtemp(join(tmpdir(), "pi-web-enabled-route-"));
const agentDir = join(root, "agent");
const originalAgentDir = process.env.PI_CODING_AGENT_DIR;
const originalOffline = process.env.PI_OFFLINE;
for (const key of ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY", "OPENAI_API_KEY"]) delete process.env[key];
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.PI_OFFLINE = "1";
await mkdir(join(agentDir, "extensions"), { recursive: true });
const settingsPath = join(agentDir, "settings.json");
const modelsPath = join(agentDir, "models.json");
await writeFile(modelsPath, JSON.stringify({
  providers: {
    gateway: {
      baseUrl: "https://gw.example/v1",
      apiKey: "fixture-key",
      api: "openai-completions",
      models: [{ id: "m1", name: "Gateway One" }, { id: "m2", name: "Gateway Two" }],
    },
  },
}, null, 2));

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { PUT } = await jiti.import("./route.ts");

after(async () => {
  if (originalAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
  else process.env.PI_CODING_AGENT_DIR = originalAgentDir;
  if (originalOffline === undefined) delete process.env.PI_OFFLINE;
  else process.env.PI_OFFLINE = originalOffline;
  await rm(root, { recursive: true, force: true });
});

function put(body) {
  return new Request("http://localhost/api/models/enabled", {
    method: "PUT",
    headers: { "Content-Type": "application/json", Host: "localhost" },
    body: JSON.stringify(body),
  });
}

test("the minimal edit lands in native settings.json through the adapter gate, unrelated keys kept", async () => {
  await writeFile(settingsPath, JSON.stringify({
    defaultProvider: "gateway", defaultModel: "m1", enabledModels: ["gateway/*"],
  }, null, 2));
  const response = await PUT(put({ op: "models", refs: ["gateway/m1"], enabled: false }));
  assert.equal(response.status, 200, JSON.stringify(await response.clone().json()));
  const saved = JSON.parse(await readFile(settingsPath, "utf8"));
  // gateway/* covered both models; switching m1 off expands the glob in place.
  assert.deepEqual(saved, { defaultProvider: "gateway", defaultModel: "m1", enabledModels: ["gateway/m2"] });
  // models.json is not an enabledModels write: its bytes are untouched.
  assert.deepEqual(JSON.parse(await readFile(modelsPath, "utf8")).providers.gateway.models.length, 2);
});

test("an adapter refusal answers 409 and keeps the file bytes", async () => {
  const original = "[1, 2, 3]\n";
  await writeFile(settingsPath, original);
  const response = await PUT(put({ op: "models", refs: ["gateway/m1"], enabled: false }));
  assert.equal(response.status, 409);
  const body = await response.json();
  assert.match(body.error, /settings\.json/, JSON.stringify(body));
  assert.equal(await readFile(settingsPath, "utf8"), original);
});
