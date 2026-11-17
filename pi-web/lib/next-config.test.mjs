// Pins the agent-build escape: Next.js 16.4.x defaults experimental.agentUpgrade
// to 'security' (next/dist/server/config-shared.js), and with that policy a
// build whose environment looks like an agent stops with SecurityFatalError as
// soon as the advisory bulk check finds the installed version affected
// (next/dist/lib/upgrade/nudge.js) — both v0.2.2 release attempts stopped there
// on next 16.4.4 (advisory 1247486, moderate, >=16.3.0 <=16.4.4). The owner
// ruling of 2026-11-17 (polyforge#104 r2) keeps pi-web on 16.4.4 — the 16.4.6
// bump ships a compression regression (double-gzipped route-handler responses,
// follow-up ticket) — so the abort is switched off here instead: pi-web serves
// behind an authenticated edge proxy and the release gate builds offline, where
// the advisory check never ran anyway. A change that re-enables the policy
// (or bumps next past it) updates this pin in the same commit.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { interopDefault: true, moduleCache: false });
const config = await jiti.import("../next.config.ts");

test("switches off Next's agent security-upgrade abort (experimental.agentUpgrade is false)", () => {
  assert.equal(
    config.experimental?.agentUpgrade,
    false,
    "next.config.ts must set experimental.agentUpgrade to false: with the 16.4.x default 'security' an agent-run build stops with SecurityFatalError on any advisory affecting the installed next",
  );
});
