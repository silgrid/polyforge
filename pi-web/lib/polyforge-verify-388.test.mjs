// carries: lib/polyforge-cards-pure.ts, lib/polyforge-cards.ts, components/PolyforgePanel.tsx
// — a heavy dispatch's wait in the daemon's admission queue
// in the job detail's execution progress: the step declaring `admission:
// heavy` (develop's verification) queues, is admitted and runs; its
// `verify_queued` / `verify_admitted` lines open its block. Read through
// the real progress reader (`readProgressSnapshot`) and cut by
// `progressBlocks`, as the panel does. (The daemon-run verification's end
// line, its check lines and the verify-judge's blocks left with the daemon
// actions, workflow contract v2 change 16.)
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-cards.ts");
const pure = await jiti.import("./polyforge-cards-pure.ts");

const WI = "wi_W388Stream01";
const T0 = Date.parse("2026-11-08T01:54:25.000Z");
const at = (ms) => new Date(T0 + ms).toISOString();
const jsonl = (records) => records.map((r) => JSON.stringify(r)).join("\n") + "\n";

const QUEUED = 60_010;
const ADMITTED = 120_010;
const RUN = { spawn: 4, start: 120_020, end: 300_020 };

function events() {
  const out = [];
  const ev = (ms, kind, body) => out.push({ at: at(ms), kind, work_item: WI, body, seq: out.length + 1 });
  ev(-60_000, "step_started", { step: "build", spawn_n: 3, model: "claude-e2e", resume: true });
  ev(-1_000, "step_done", { step: "build", spawn_n: 3 });
  ev(QUEUED, "verify_queued", { step: "verification", reason: "heavy-run slots full (2/2)", position: 2, since: at(QUEUED) });
  ev(ADMITTED, "verify_admitted", { step: "verification", queued_ms: ADMITTED - QUEUED });
  ev(RUN.start, "step_started", { step: "verification", spawn_n: RUN.spawn, model: "pi:e2e/cheap", resume: false });
  ev(RUN.end, "step_done", { step: "verification", spawn_n: RUN.spawn });
  ev(RUN.end + 6, "step_started", { step: "publish", spawn_n: 5, model: "pi:e2e/model" });
  return out;
}

async function snapshot(t) {
  const work = mkdtempSync(join(tmpdir(), "pf-388-"));
  t.after(() => rmSync(work, { recursive: true, force: true }));
  const root = join(work, "data");
  const capsule = join(work, "capsule");
  mkdirSync(join(root, "events"), { recursive: true });
  mkdirSync(join(capsule, "runtime-state"), { recursive: true });
  writeFileSync(join(root, "events", `wi-${WI}.jsonl`), jsonl(events()));
  writeFileSync(join(capsule, "runtime-state", "work-item.json"), JSON.stringify({ id: WI }));
  writeFileSync(join(capsule, "runtime-state", "spawn-journal.jsonl"), jsonl([]));
  const snap = await lib.readProgressSnapshot(root, capsule, WI);
  assert.equal(snap.available, true, "the stream is readable");
  return snap;
}

test("a queued dispatch's queued and admitted lines open its block", async (t) => {
  const { lines } = await snapshot(t);
  const layout = pure.progressBlocks(lines);
  assert.equal(layout.leading + layout.blocks.reduce((sum, b) => sum + b.count, 0), lines.length, "no line is dropped");
  const green = layout.blocks.find((b) => b.step === "verification" && b.spawn === "4");
  assert.ok(green.first !== undefined && green.first < green.start, "the block starts at its lead-in lines");
  const lead = lines.slice(green.first, green.start);
  assert.deepEqual(lead.map((line) => line.event), ["verify_queued", "verify_admitted"]);
  assert.ok(lead[0].text.includes("#2") && lead[0].text.includes("heavy-run slots full (2/2)") && lead[0].text.includes(`since ${pure.formatProgressClock(at(QUEUED))}`),
    `the queued line shows position, reason and since: ${lead[0].text}`);
  assert.ok(lead[1].text.includes("+1m 00s"), `the admitted line shows how long it queued: ${lead[1].text}`);
  assert.equal(green.status, "done", "the dispatch's own step_done ends it");
  assert.match(lines[green.start].text, /▶ verification #4 · pi:e2e\/cheap/, "an agent dispatch's line names its step and model");
  const build = layout.blocks.find((b) => b.spawn === "3");
  assert.ok(build.start + build.count <= green.first, "the previous block no longer holds them");
  assert.equal(build.first, undefined, "a block with no lead-in lines has no first");
});
