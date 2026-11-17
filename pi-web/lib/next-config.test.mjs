// Pins the agent-build escape: Next.js defaults experimental.agentUpgrade
// to 'security' (next/dist/server/config-shared.js), and with that policy a
// build whose environment looks like an agent stops with SecurityFatalError as
// soon as the advisory bulk check finds the installed version affected
// (next/dist/lib/upgrade/nudge.js) — both v0.2.2 release attempts stopped there
// on advisory 1247486 (moderate, >=16.3.0 <=16.4.4). This pin holds regardless
// of the installed next version (owner rulings 2026-11-17, polyforge#104 r2 and
// polyforge#105, which bumped next to 16.4.6 and dropped the app-layer gzip):
// dependency advisories are judged by pi-web's own audit before a release (the
// integrate gate, polyforge#113), never by a third-party check aborting an
// agent-run build — and the release gate builds offline, where this check
// never runs anyway. A change that re-enables the policy updates this pin in
// the same commit.
import "#test-env";
import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createJiti } from "jiti";

const projectRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

function sizeLimitToBytes(value) {
  if (typeof value === "number") return value;
  const match = /^(\d+(?:\.\d+)?)\s*(b|kb|mb|gb)$/i.exec(value.trim());
  assert.ok(match, `unparseable SizeLimit: ${value}`);
  const units = { b: 1, kb: 1024, mb: 1024 ** 2, gb: 1024 ** 3 };
  return Number(match[1]) * units[match[2].toLowerCase()];
}

test("scopes Next.js output file tracing to the pi-web package", async () => {
  const config = await createJiti(import.meta.url).import("../next.config.ts", { default: true });

  assert.equal(config.outputFileTracingRoot, projectRoot);
});

test("raises the proxy body buffer above the upload route's 100 MB request cap", async () => {
  const config = await createJiti(import.meta.url).import("../next.config.ts", { default: true });

  assert.ok(sizeLimitToBytes(config.experimental.proxyClientMaxBodySize) > 100 * 1024 * 1024);
});

test("switches off Next's agent security-upgrade abort (experimental.agentUpgrade is false)", async () => {
  const config = await createJiti(import.meta.url).import("../next.config.ts", { default: true });

  assert.equal(
    config.experimental?.agentUpgrade,
    false,
    "next.config.ts must set experimental.agentUpgrade to false: with the default 'security' an agent-run build stops with SecurityFatalError on any advisory affecting the installed next",
  );
});
