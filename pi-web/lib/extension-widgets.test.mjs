import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

import { mergeExtensionSnapshot, updateExtensionWidgets } from "./extension-widgets.ts";

const agents = { key: "agents", lines: ["Agents: idle"], placement: "belowEditor" };
const fleet = { key: "fleet", lines: ["Fleet: idle"], placement: "belowEditor" };

test("updates competing widgets without changing their order", () => {
  const afterAgentsUpdate = updateExtensionWidgets(
    [agents, fleet],
    "agents",
    ["Agents: running"],
    "belowEditor",
  );
  const afterFleetUpdate = updateExtensionWidgets(
    afterAgentsUpdate,
    "fleet",
    ["Fleet: running"],
    "belowEditor",
  );

  assert.deepEqual(afterFleetUpdate.map((widget) => widget.key), ["agents", "fleet"]);
  assert.deepEqual(afterFleetUpdate.map((widget) => widget.lines), [
    ["Agents: running"],
    ["Fleet: running"],
  ]);
});

test("appends new widgets and removes cleared widgets", () => {
  const withFleet = updateExtensionWidgets([agents], "fleet", fleet.lines, fleet.placement);
  assert.deepEqual(withFleet.map((widget) => widget.key), ["agents", "fleet"]);

  const withoutAgents = updateExtensionWidgets(withFleet, "agents", undefined);
  assert.deepEqual(withoutAgents, [fleet]);
});

test("a snapshot keeps the live value of keys an event touched during its read", () => {
  // The page set `usage` and `polyforge` from the event stream while a state read
  // that the server answered before the command was still in flight (wb#159).
  const usage = { key: "usage", lines: ["E2E control widget line"], placement: "aboveEditor" };
  const polyforge = { key: "polyforge", lines: ["pf running"], placement: "aboveEditor" };
  const staleSnapshot = [polyforge, fleet];
  const live = [polyforge, usage];

  const merged = mergeExtensionSnapshot(staleSnapshot, live, new Set(["usage", "polyforge"]));
  assert.deepEqual(merged, [polyforge, fleet, usage]);
});

test("a snapshot drops a key an event cleared during its read and replaces untouched keys", () => {
  const agentsRunning = { ...agents, lines: ["Agents: running"] };
  const merged = mergeExtensionSnapshot([agentsRunning, fleet], [agents], new Set(["fleet"]));
  assert.deepEqual(merged, [agentsRunning]);
});

test("a snapshot with no events during its read replaces the live list", () => {
  const snapshot = [fleet];
  assert.equal(mergeExtensionSnapshot(snapshot, [agents], new Set()), snapshot);
});
