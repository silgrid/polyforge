// carries: lib/harness/concepts.ts, lib/harness/adapter.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

// The adapter framework's shared pieces: the neutral vocabulary, the write
// gate and the registry. Expected values come from the ticket (polyforge#141
// AC1 and the owner rulings 2026-11-19: Q1 A, Q2 C), never from the
// implementation's output.
const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true, moduleCache: false });
const concepts = await jiti.import("./concepts.ts");
const adapter = await jiti.import("./adapter.ts");

test("the neutral reasoning-effort levels are ordered off..max and the approval modes ask..full", () => {
  assert.deepEqual(concepts.REASONING_EFFORT_LEVELS, ["off", "minimal", "low", "medium", "high", "xhigh", "max"]);
  assert.deepEqual(concepts.APPROVAL_MODES, ["ask", "accept-edits", "plan", "auto", "deny-unprompted", "full"]);
});

test("neutral mappers accept the harness spellings and reject unknown ones", () => {
  // pi's defaultThinkingLevel and claude's effortLevel use the neutral spellings verbatim
  assert.equal(concepts.neutralReasoningEffort("high"), "high");
  assert.equal(concepts.neutralReasoningEffort("xhigh"), "xhigh");
  assert.equal(concepts.neutralReasoningEffort("off"), "off");
  assert.equal(concepts.neutralReasoningEffort("bogus"), null);
  assert.equal(concepts.neutralReasoningEffort(42), null);
  // claude's permissions.defaultMode spellings
  assert.equal(concepts.neutralApprovalMode("default"), "ask");
  assert.equal(concepts.neutralApprovalMode("acceptEdits"), "accept-edits");
  assert.equal(concepts.neutralApprovalMode("plan"), "plan");
  assert.equal(concepts.neutralApprovalMode("bypassPermissions"), "full");
  assert.equal(concepts.neutralApprovalMode("auto"), "auto");
  assert.equal(concepts.neutralApprovalMode("deny"), "deny-unprompted");
  assert.equal(concepts.neutralApprovalMode("weird"), null);
  // already-neutral spellings pass through
  assert.equal(concepts.neutralApprovalMode("accept-edits"), "accept-edits");
});

test("the known ranges are claude 2.x and pi 1.x, the first recorded versions kept as their floors (Q2 C; 85a3 Q4: the gate recognizes the same major)", () => {
  assert.equal(adapter.CLAUDE_KNOWN_RANGE.label, "claude 2.x");
  assert.equal(adapter.CLAUDE_KNOWN_RANGE.major, 2);
  assert.equal(adapter.CLAUDE_KNOWN_RANGE.minPatch, 312);
  assert.equal(adapter.PI_KNOWN_RANGE.label, "pi 1.x");
  assert.equal(adapter.PI_KNOWN_RANGE.major, 1);
  assert.equal(adapter.PI_KNOWN_RANGE.minor, 5);
});

test("versionInKnownRange: same major is recognized whatever the minor or patch, another major is not (85a3 Q4)", () => {
  const claude = adapter.CLAUDE_KNOWN_RANGE;
  assert.equal(adapter.versionInKnownRange("2.1.312", claude), true);
  assert.equal(adapter.versionInKnownRange("2.1.326", claude), true);
  assert.equal(adapter.versionInKnownRange("2.1.100", claude), true);
  assert.equal(adapter.versionInKnownRange("2.2.0", claude), true);
  assert.equal(adapter.versionInKnownRange("2.0.5", claude), true);
  assert.equal(adapter.versionInKnownRange("3.0.0", claude), false);
  assert.equal(adapter.versionInKnownRange("not-a-version", claude), false);
  const pi = adapter.PI_KNOWN_RANGE;
  assert.equal(adapter.versionInKnownRange("1.5.0", pi), true);
  assert.equal(adapter.versionInKnownRange("1.4.0", pi), true);
  assert.equal(adapter.versionInKnownRange("1.9.0", pi), true);
  assert.equal(adapter.versionInKnownRange("2.0.0", pi), false);
});

test("versionAtLeast: a model key's registered minimum floors a version (85a3 Q4)", () => {
  assert.equal(adapter.versionAtLeast("2.1.312", "2.1.312"), true);
  assert.equal(adapter.versionAtLeast("2.1.326", "2.1.312"), true);
  assert.equal(adapter.versionAtLeast("2.2.0", "2.1.312"), true);
  assert.equal(adapter.versionAtLeast("2.1.100", "2.1.312"), false);
  assert.equal(adapter.versionAtLeast("1.4.0", "1.5.0"), false);
  assert.equal(adapter.versionAtLeast("bogus", "1.5.0"), false);
  assert.equal(adapter.versionAtLeast("1.5.0", "bogus"), false);
});

test("writeGate refuses an out-of-range version with the probe's reason", () => {
  const gate = adapter.writeGate({
    version: "3.0.0",
    known: false,
    knownRange: "claude 2.x",
    reason: "version 3.0.0 is outside the known range claude 2.x",
    source: "claude initialize",
  });
  assert.equal(gate.allowed, false);
  assert.ok("reason" in gate && gate.reason.includes("3.0.0"));
});

test("writeGate refuses an unrecognized file structure with the structure reason (Q2 C)", () => {
  const gate = adapter.writeGate({
    version: "2.1.326",
    known: false,
    knownRange: "claude 2.x",
    reason: "settings.json has an unrecognized top-level structure",
    source: "claude initialize",
  });
  assert.equal(gate.allowed, false);
  assert.ok("reason" in gate && gate.reason.includes("unrecognized"));
});

test("writeGate refuses a version it could not detect", () => {
  const gate = adapter.writeGate({ version: null, known: false, reason: "claude not found", source: "claude initialize" });
  assert.equal(gate.allowed, false);
  assert.ok("reason" in gate && gate.reason.length > 0);
});

test("writeGate allows a known version", () => {
  const gate = adapter.writeGate({ version: "1.5.0", known: true, knownRange: "pi 1.x", source: "pi SDK package" });
  assert.deepEqual(gate, { allowed: true });
});

/** A fake adapter whose probe result is injected. */
function fakeAdapter(id, probeResult) {
  return {
    id,
    capabilities: {
      id,
      scopes: ["user"],
      concepts: ["model"],
      writableConcepts: [],
      readOnlyConcepts: ["env", "effective-config"],
      configRootEnv: null,
      credentialsFollowConfigRoot: true,
      nativeDiagnostics: { declared: true, implemented: false },
    },
    probeVersion: async () => probeResult,
  };
}

test("gatedWrite: an out-of-range version is refused with a reason, a known version answers not-implemented (Q1 A)", async () => {
  const unknown = fakeAdapter("pi", {
    version: "2.0.0",
    known: false,
    knownRange: "pi 1.x",
    reason: "version 2.0.0 is outside the known range pi 1.x",
    source: "pi SDK package",
  });
  const refused = await adapter.gatedWrite(unknown, "model", {}, {});
  assert.equal(refused.status, "refused");
  assert.ok(refused.reason.includes("2.0.0"));

  const known = fakeAdapter("pi", { version: "1.5.0", known: true, knownRange: "pi 1.x", source: "pi SDK package" });
  const notImplemented = await adapter.gatedWrite(known, "model", {}, {});
  assert.equal(notImplemented.status, "not-implemented");
  assert.ok(notImplemented.reason.includes("model"));
  assert.ok(notImplemented.reason.includes("pi"));
});

test("gatedWrite probes before deciding: the probe runs even for a read-only concept", async () => {
  let probed = 0;
  const counting = fakeAdapter("claude", { version: null, known: false, reason: "claude not found", source: "claude initialize" });
  counting.probeVersion = async () => {
    probed += 1;
    return { version: null, known: false, reason: "claude not found", source: "claude initialize" };
  };
  const refused = await adapter.gatedWrite(counting, "env", {}, {});
  assert.equal(probed, 1);
  assert.equal(refused.status, "refused");
});

test("the registry registers, lists and looks adapters up by id", () => {
  const pi = fakeAdapter("pi", { version: "1.5.0", known: true, knownRange: "pi 1.x", source: "pi SDK package" });
  const claude = fakeAdapter("claude", { version: "2.1.326", known: true, knownRange: "claude 2.x", source: "claude initialize" });
  adapter.registerHarness(pi);
  adapter.registerHarness(claude);
  assert.equal(adapter.getHarnessAdapter("pi"), pi);
  assert.equal(adapter.getHarnessAdapter("claude"), claude);
  assert.equal(adapter.getHarnessAdapter("pi").id, "pi");
  assert.deepEqual(adapter.listHarnessAdapters().map((a) => a.id), ["pi", "claude"]);
});
