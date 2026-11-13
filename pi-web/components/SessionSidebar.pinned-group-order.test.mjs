import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// Parse smoke test, same convention as SessionSidebar.test.mjs.
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
await jiti.import("./SessionSidebar.tsx");

const source = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");

// The order overlay is pure logic; import it directly so the reorder
// semantics, running guard inputs and persistence round-trip are tested as
// behavior, not just source shape.
const {
  applyGroupOrder,
  listGroupOrder,
  setGroupOrder,
  PINNED_GROUP_ORDER_STORAGE_KEY,
} = await jiti.import("../lib/pinned-group-order.ts");

function projects(keys) {
  return keys.map((key) => ({ key, root: `/root/${key}` }));
}

test("the rendered pinned sequence applies the persisted order overlay", () => {
  const memoBlock = source.slice(
    source.indexOf("const pinnedProjects = useMemo("),
    source.indexOf("const [pinnedLabelsByKey"),
  );
  assert.match(memoBlock, /applyGroupOrder\(/);
  assert.match(memoBlock, /listGroupOrder\(\)/);
  assert.match(memoBlock, /pinnedEntries\.map\(\(entry\) => \(\{ key: customDirectoryIdentity\(entry\.path\), root: entry\.path, displayName: entry\.displayName \}\)\)/);
});

test("a drop moves the dragged group to the target's index (adjacent-swap semantics) and persists the full order", () => {
  const moveBlock = source.slice(
    source.indexOf("const movePinnedGroup = useCallback"),
    source.indexOf("}, [pinnedProjects]);") + "}, [pinnedProjects]);".length,
  );
  // Remove + reinsert at the target index (a plain swap for neighbors) ...
  assert.match(moveBlock, /keys\.splice\(draggedIndex, 1\);/);
  assert.match(moveBlock, /keys\.splice\(targetIndex, 0, draggedKey\);/);
  // ... unknown keys refused, self-drops ignored ...
  assert.match(moveBlock, /if \(draggedKey === targetKey\) return;/);
  assert.match(moveBlock, /if \(draggedIndex < 0 \|\| targetIndex < 0\) return;/);
  // ... and the FULL sequence persisted through the lib, then applied
  // immediately by bumping the pinned revision.
  assert.match(moveBlock, /setGroupOrder\(keys\);/);
  assert.match(moveBlock, /setPinnedRevision\(\(revision\) => revision \+ 1\);/);
});

test("only non-running groups are draggable; any group may be the drop target", () => {
  const guardBlock = source.slice(
    source.indexOf("const pinnedGroupDragProps ="),
    source.indexOf("const groupVisibleCounts = new Map<string, number>();"),
  );
  // The guard reads the running count from the groupActivity map.
  assert.match(guardBlock, /const running = \(groupActivity\.get\(project\.key\)\?\.running \?\? 0\) > 0;/);
  assert.match(guardBlock, /draggable: !running,/);
  // A running group's dragstart is refused even if draggable snuck through.
  assert.match(guardBlock, /if \(running\) \{\s*event\.preventDefault\(\);\s*return;\s*\}/);
  // The target guard rejects only SELF-drops and unknown drags — a running
  // group remains a valid target (only the dragged item must be idle).
  assert.match(guardBlock, /if \(dragGroupKey == null \|\| dragGroupKey === project\.key\) return;/);
  assert.doesNotMatch(guardBlock, /targetRunning/, "the drop target is never guarded by running state");
});

test("one dragged group at a time; dragend and drop clear the live drag state", () => {
  const stateBlock = source.slice(
    source.indexOf("const [dragGroupKey, setDragGroupKey]"),
    source.indexOf("const [customPathOpen, setCustomPathOpen]"),
  );
  assert.match(stateBlock, /const \[dragGroupKey, setDragGroupKey\] = useState<string \| null>\(null\);/);
  assert.match(stateBlock, /const \[dropTargetGroupKey, setDropTargetGroupKey\] = useState<string \| null>\(null\);/);
  const dragBlock = source.slice(
    source.indexOf("const pinnedGroupDragProps ="),
    source.indexOf("const groupVisibleCounts = new Map<string, number>();"),
  );
  assert.match(dragBlock, /onDragEnd: \(\) => \{\s*setDragGroupKey\(null\);\s*setDropTargetGroupKey\(null\);\s*\},/);
  assert.match(dragBlock, /setDragGroupKey\(project\.key\);/);
});

test("the header row carries the drag affordance and the drop-target highlight", () => {
  const headerBlock = source.slice(
    source.indexOf("function PinnedGroupHeader({"),
    source.indexOf("const SCRAMBLE_CHARS ="),
  );
  // Draggable root with the reorder handlers, suspended while renaming so
  // the rename input keeps focus/selection inside a draggable container.
  assert.match(headerBlock, /draggable=\{reorder != null && reorder\.draggable && !renaming\}/);
  assert.match(headerBlock, /onDragStart=\{reorder\?\.onDragStart\}/);
  assert.match(headerBlock, /onDragOver=\{reorder\?\.onDragOver\}/);
  assert.match(headerBlock, /onDrop=\{reorder\?\.onDrop\}/);
  assert.match(headerBlock, /onDragEnd=\{reorder\?\.onDragEnd\}/);
  // Drop-target highlight: selected background + dashed accent outline.
  assert.match(headerBlock, /background: reorder\?\.isDropTarget \? "var\(--bg-selected\)" : "var\(--bg\)"/);
  assert.match(headerBlock, /outline: "2px dashed var\(--accent\)"/);
  // The rows map wires the per-project drag props into the header.
  const rowsBlock = source.slice(
    source.indexOf("visibleSidebarRows.map((row) => {"),
    source.indexOf("const family = row.family;"),
  );
  assert.match(rowsBlock, /reorder=\{pinnedGroupDragProps\(row\.project\)\}/);
});

test("keyboard reorder is deferred to the backlog (recorded in code)", () => {
  assert.match(source, /[Kk]eyboard[\s\S]*?reorder[\s\S]*?deferred to the backlog/);
});

test("reordering survives a reload: persist then re-apply over the default list (mocked storage)", () => {
  const store = new Map();
  globalThis.window = {
    localStorage: {
      getItem: (key) => (store.has(key) ? store.get(key) : null),
      setItem: (key, value) => store.set(key, String(value)),
      removeItem: (key) => store.delete(key),
    },
  };
  try {
    // The sidebar's move: remove "gamma" and reinsert at "alpha"'s index.
    const keys = ["alpha", "beta", "gamma", "delta"];
    const draggedIndex = keys.indexOf("gamma");
    const targetIndex = keys.indexOf("alpha");
    keys.splice(draggedIndex, 1);
    keys.splice(targetIndex, 0, "gamma");
    setGroupOrder(keys);
    assert.equal(store.get(PINNED_GROUP_ORDER_STORAGE_KEY), '["gamma","alpha","beta","delta"]');
    // Re-applying the stored order over the DEFAULT (storage-order) list
    // restores the dragged layout exactly — the reload contract.
    assert.deepEqual(
      applyGroupOrder(projects(["alpha", "beta", "gamma", "delta"]), listGroupOrder()).map((p) => p.key),
      ["gamma", "alpha", "beta", "delta"],
    );
  } finally {
    delete globalThis.window;
  }
});

test("running-group guard input: groupActivity carries the per-group running counts", () => {
  const activityBlock = source.slice(
    source.indexOf("const groupActivity = new Map<string, { running: number; unread: number }>();"),
    source.indexOf("const pinnedGroupDragProps ="),
  );
  assert.match(activityBlock, /if \(runningSessionIds\.has\(session\.id\)\) running \+= 1;/);
  assert.match(activityBlock, /groupActivity\.set\(project\.key, \{ running, unread \}\);/);
});
