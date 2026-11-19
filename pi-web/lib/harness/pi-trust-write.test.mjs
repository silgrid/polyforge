// carries: lib/project-trust.ts, lib/harness/pi.ts, app/api/project-trust/*
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";
import { ProjectTrustStore } from "@earendil-works/pi-coding-agent";

// polyforge#150 (85a4, review r1#2): a project trust decision from the
// permission page goes through the same gate as POST /api/project-trust —
// a busy session refuses before anything is written, and a landed decision
// rebuilds the cwd's sessions (the model cache drops, the wrappers end) so
// the next start reads the new trust state. Expected values are
// hand-derived from the route's own refusal words (r2#4: the busy line is
// direction-neutral, so it reads true for deny too) and the SDK trust
// store's decision shape; the collaborators are injected so the busy and
// rebuild facts are observed, not supposed.
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-pi-trust-write-"));
const agentDir = path.join(scratch, "agent");
const project = path.join(scratch, "project");
const plain = path.join(scratch, "plain");
fs.mkdirSync(agentDir, { recursive: true });
fs.mkdirSync(path.join(project, ".pi"), { recursive: true });
fs.mkdirSync(plain, { recursive: true });
// Trust-requiring: the .pi/mcp.json alone makes the folder require trust.
fs.writeFileSync(path.join(project, ".pi", "mcp.json"), JSON.stringify({ mcpServers: {} }));
process.env.PI_CODING_AGENT_DIR = agentDir;

test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const trust = await jiti.import("../project-trust.ts");
const pi = await jiti.import("./pi.ts");
const adapter = pi.createPiAdapter();

test("a busy session refuses the decision before anything is written", async () => {
  const rebuilds = [];
  const outcome = await trust.writeProjectTrustDecision(project, true, agentDir, {
    isBusy: () => true,
    rebuild: async (cwd) => { rebuilds.push(cwd); },
  });
  assert.deepEqual(outcome, { ok: false, reason: "session-busy", message: "Wait for the active session to finish before changing this project's trust" });
  assert.equal(fs.existsSync(path.join(agentDir, "trust.json")), false, "a refused write created trust.json");
  assert.deepEqual(rebuilds, [], "a refused write rebuilt the cwd's sessions");
});

test("a landed decision records it and rebuilds the cwd's sessions; denying records the explicit false", async () => {
  const rebuilds = [];
  const outcome = await trust.writeProjectTrustDecision(project, true, agentDir, {
    isBusy: () => false,
    rebuild: async (cwd) => { rebuilds.push(cwd); },
  });
  assert.equal(outcome.ok, true);
  assert.equal(outcome.status.trusted, true);
  assert.deepEqual(rebuilds, [project], "a landed decision did not rebuild the cwd");
  assert.equal(new ProjectTrustStore(agentDir).getEntry(project).decision, true);

  const denied = await trust.writeProjectTrustDecision(project, false, agentDir, {
    isBusy: () => false,
    rebuild: async (cwd) => { rebuilds.push(cwd); },
  });
  assert.equal(denied.ok, true);
  assert.equal(denied.status.trusted, false);
  assert.deepEqual(rebuilds, [project, project], "the deny did not rebuild the cwd");
  assert.equal(new ProjectTrustStore(agentDir).getEntry(project).decision, false);
});

test("a folder that requires no trust records no decision", async () => {
  const outcome = await trust.writeProjectTrustDecision(plain, true, agentDir, {
    isBusy: () => false,
    rebuild: async () => {},
  });
  assert.deepEqual(outcome, { ok: false, reason: "not-required", message: "This project has no resources that require trust" });
  assert.equal(new ProjectTrustStore(agentDir).getEntry(plain), null);
});

test("the pi adapter's permission write goes through the shared gate: the not-required refusal is the route's own message", async () => {
  const refused = await adapter.write("permission", { projectTrust: { trusted: true } }, { agentDir, cwd: plain, piVersion: "1.5.0" });
  assert.equal(refused.status, "refused");
  assert.ok(refused.reason.includes("no resources that require trust"), refused.reason);
  const written = await adapter.write("permission", { projectTrust: { trusted: true } }, { agentDir, cwd: project, piVersion: "1.5.0" });
  assert.equal(written.status, "written");
  assert.equal(new ProjectTrustStore(agentDir).getEntry(project).decision, true);
});
