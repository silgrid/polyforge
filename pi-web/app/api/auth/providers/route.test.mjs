// carries: app/api/auth/providers/route.ts, lib/provider-listing.ts
// polyforge#142 (85a2, AC8, owner ruling Q10): the providers route never
// offers pi's Anthropic subscription (Claude Pro/Max) login — the real
// SDK's provider definitions answer the route, so the filter must drop the
// real anthropic OAuth entry; a stored subscription credential (planted
// through the same store pi's own login writes) keeps its status entry;
// and the API-key entry for Anthropic is unaffected. The sentinel planted
// as the fake credential must not survive in any answer.
import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const root = await mkdtemp(join(tmpdir(), "pi-web-providers-route-"));
const agentDir = join(root, "agent");
const home = join(root, "home");
await mkdir(join(agentDir, "extensions"), { recursive: true });
await mkdir(home, { recursive: true });
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.HOME = home;
process.env.USERPROFILE = home;
process.env.PI_OFFLINE = "1";
delete process.env.ANTHROPIC_API_KEY;

test.after(async () => {
  await rm(root, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
});
const { GET } = await jiti.import("./route.ts");
const { storeProviderCredential } = await jiti.import("../../../../lib/provider-credential-store.ts");
const { invalidateModelRuntimeWithExtensions } = await jiti.import("../../../../lib/model-runtime.ts");

const request = () => new Request("http://localhost/api/auth/providers", { headers: { host: "localhost" } });

test("the OAuth offering never lists the Anthropic subscription login; the API-key entry stays (AC8)", async () => {
  const response = await GET();
  assert.equal(response.status, 200);
  const body = await response.json();
  const oauthIds = body.oauthProviders.map((p) => p.id);
  assert.ok(!oauthIds.includes("anthropic"), `the unlogged Anthropic subscription login is offered: ${oauthIds.join(", ")}`);
  assert.ok(oauthIds.length > 0, "the offering is not empty — the SDK carries other OAuth providers");
  // pi's Anthropic API key entry is unaffected
  assert.ok(body.apiKeyProviders.some((p) => p.id === "anthropic"), "the Anthropic API-key entry is gone");
});

test("a stored Anthropic subscription credential keeps its status entry, its value never in the answer (AC8)", async () => {
  await storeProviderCredential("anthropic", { type: "oauth", refresh: "rt-SENTINEL-SUB", access: "at-SENTINEL-SUB", expires: 0 });
  invalidateModelRuntimeWithExtensions();
  const response = await GET();
  const text = await response.text();
  assert.equal(response.status, 200);
  assert.ok(!text.includes("SENTINEL"), "the stored credential's value appears in the answer");
  const body = JSON.parse(text);
  const anthropic = body.oauthProviders.find((p) => p.id === "anthropic");
  assert.ok(anthropic, "the stored subscription credential lost its status entry");
  assert.equal(anthropic.loggedIn, true);
  // the API-key list reports the provider as not configured (the credential is the subscription's)
  const keyRow = body.apiKeyProviders.find((p) => p.id === "anthropic");
  assert.ok(keyRow && keyRow.configured === false, "the subscription credential lit the API-key row");
});
