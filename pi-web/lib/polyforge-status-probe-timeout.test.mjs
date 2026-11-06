// carries: lib/polyforge-status.ts
// the presence probe's 3 s bound honours a child that has
// already exited. A blocked event loop (a cold page load's heavy work) lets
// the bound expire while the child's exit is still unprocessed; Node runs the
// expired timer before it handles the exit, so the probe must give that
// pending exit a chance to land before it judges pf absent. Kept in its own
// file: the 3.5 s synchronous block runs in its own `node --test` child
// process and stalls no other test.
import "#test-env";
import assert from "node:assert/strict";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);

async function loadSubject() {
  return jiti.import("./polyforge-status.ts");
}

async function fakePf(t, body) {
  const dir = await mkdtemp(join(tmpdir(), "pi-web-pf-probe-timeout-"));
  const bin = join(dir, "pf");
  await writeFile(bin, `#!/bin/sh\n${body}\n`);
  await chmod(bin, 0o755);
  const previous = process.env.PF_BIN;
  process.env.PF_BIN = bin;
  t.after(async () => {
    if (previous === undefined) delete process.env.PF_BIN;
    else process.env.PF_BIN = previous;
    await rm(dir, { recursive: true, force: true });
  });
}

function blockEventLoop(ms) {
  const until = Date.now() + ms;
  while (Date.now() < until) {
    // busy-wait: the event loop processes nothing, not even the child's exit
  }
}

test("a probe whose child exited during a blocked event loop past the 3 s bound reads present", async (t) => {
  await fakePf(t, "echo v9.9.9");
  const { probePolyforgeVersion } = await loadSubject();
  // The spawn and the 3 s timer are armed synchronously here.
  const pending = probePolyforgeVersion(true);
  // The child exits within milliseconds; its exit is only processed after
  // the block, by which time the bound has expired.
  blockEventLoop(3500);
  assert.deepEqual(await pending, { present: true, version: "v9.9.9" });
});

test("a child genuinely still running at the 3 s bound is killed and reads absent", async (t) => {
  await fakePf(t, "exec sleep 10");
  const { probePolyforgeVersion } = await loadSubject();
  const started = Date.now();
  assert.deepEqual(await probePolyforgeVersion(true), { present: false, version: null });
  const elapsed = Date.now() - started;
  assert.ok(elapsed >= 2900 && elapsed < 9000, `the bound killed the child after about 3 s (took ${elapsed} ms)`);
});
