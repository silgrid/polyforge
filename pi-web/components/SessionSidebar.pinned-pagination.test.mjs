import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// Parse smoke test, same convention as SessionSidebar.test.mjs.
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
await jiti.import("./SessionSidebar.tsx");

const source = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");

// The pure row model carries the pagination geometry; import it directly so
// the limit/step contract is tested as behavior, not just source shape.
const {
  buildSidebarRows,
  GROUP_MORE_HEIGHT,
  SESSION_LIST_ITEM_HEIGHT,
  GROUP_HEADER_HEIGHT,
} = await jiti.import("../lib/sidebar-rows.ts");

const families = (ids) => ids.map((id) => ({
  root: { id, name: `s-${id}` },
  subagents: [],
}));

const project = { key: "proj", root: "/proj" };

function rowsFor(ids, shownCount) {
  return buildSidebarRows({
    pinnedProjects: [project],
    familiesByProject: new Map([[project.key, families(ids)]]),
    expandedKeys: new Set([project.key]),
    mainFamilies: [],
    ...(shownCount !== undefined ? { groupVisibleCounts: new Map([[project.key, shownCount]]) } : {}),
  });
}

test("the row model cuts a group's rows at its shown count and emits a footer with the hidden count", () => {
  const ids = Array.from({ length: 30 }, (_, i) => `id-${i}`);
  // Default (no entry): every row, no footer — the pre-feature layout.
  const unlimited = rowsFor(ids, undefined);
  assert.equal(unlimited.filter((row) => row.kind === "session").length, 30);
  assert.equal(unlimited.filter((row) => row.kind === "groupMore").length, 0);

  // Limit: 8 rows plus the footer, offsets stay cumulative and contiguous.
  const limited = rowsFor(ids, 8);
  const sessionRows = limited.filter((row) => row.kind === "session");
  assert.equal(sessionRows.length, 8);
  assert.deepEqual(sessionRows.map((row) => row.key), ids.slice(0, 8).map((id) => id));
  const footer = limited.find((row) => row.kind === "groupMore");
  assert.ok(footer, "the cut group gets a footer row");
  assert.equal(footer.groupKey, "proj");
  assert.equal(footer.hiddenCount, 22);
  // Geometry: footer sits directly under the last shown row, one row of
  // GROUP_MORE_HEIGHT, before the main list.
  assert.equal(footer.offset, GROUP_HEADER_HEIGHT + 8 * SESSION_LIST_ITEM_HEIGHT);
  assert.equal(footer.height, GROUP_MORE_HEIGHT);

  // One "show more" step: 8 + 20 = 28 rows, 2 still hidden.
  const revealed = rowsFor(ids, 28);
  assert.equal(revealed.filter((row) => row.kind === "session").length, 28);
  assert.equal(revealed.find((row) => row.kind === "groupMore").hiddenCount, 2);

  // Overshoot (reveal-all click, uncapped): all 30 rows, footer flips to
  // "show less" — hidden 0, footer still present.
  const all = rowsFor(ids, 48);
  assert.equal(all.filter((row) => row.kind === "session").length, 30);
  const allFooter = all.find((row) => row.kind === "groupMore");
  assert.equal(allFooter.hiddenCount, 0);

  // Collapsed groups keep only their header regardless of the shown count.
  const collapsed = buildSidebarRows({
    pinnedProjects: [project],
    familiesByProject: new Map([[project.key, families(ids)]]),
    expandedKeys: new Set(),
    mainFamilies: [],
    groupVisibleCounts: new Map([[project.key, 8]]),
  });
  assert.deepEqual(collapsed.map((row) => row.kind), ["groupHeader"]);

  // A group at or below the limit never gets an entry from the component
  // (the sidebar only paginates groups over the limit), so it emits no
  // footer — the entry map is the component's contract, not the lib's.
  const small = rowsFor(["a", "b"], undefined);
  assert.equal(small.filter((row) => row.kind === "groupMore").length, 0);
});

test("the sidebar carries the upstream constants as component-local values", () => {
  assert.match(source, /const PINNED_VISIBLE_LIMIT = 8;/);
  assert.match(source, /const SHOW_MORE_STEP = 20;/);
  // Not a new module: the constants live in the sidebar file itself, and the
  // pin-order/expansion persistence module is untouched by pagination.
  assert.doesNotMatch(source, /from "@\/lib\/pinned-pagination"/);
});

test("shown counts are component state, never persisted", () => {
  assert.match(source, /const \[pinnedShownExtra, setPinnedShownExtra\] = useState<Record<string, number>>\(\{\}\);/);
  // No localStorage near the pagination state or handlers.
  const paginationBlock = source.slice(
    source.indexOf("const [pinnedShownExtra, setPinnedShownExtra]"),
    source.indexOf("const handlePinnedGroupShowLess"),
  );
  assert.ok(!paginationBlock.includes("localStorage"), "pagination state must not persist");
  // lib/pinned-expansion.ts is untouched by this feature: the sidebar still
  // imports only the original three helpers from it.
  assert.match(
    source,
    /import \{\s*discardExpandedGroupKey,\s*readExpandedGroupKeys,\s*writeExpandedGroupKeys,\s*\} from "@\/lib\/pinned-expansion";/,
  );
});

test("groups beyond the limit feed a shown count into the row model; smaller groups do not", () => {
  const buildBlock = source.slice(
    source.indexOf("const groupVisibleCounts = new Map<string, number>();"),
    source.indexOf("const visibleSidebarRows = getWindowedRows"),
  );
  assert.match(buildBlock, /length <= PINNED_VISIBLE_LIMIT\) continue;/);
  assert.match(buildBlock, /groupVisibleCounts\.set\(\s*project\.key,\s*PINNED_VISIBLE_LIMIT \+ \(typeof extra === "number" && extra > 0 \? Math\.floor\(extra\) : 0\),\s*\);/);
  assert.match(buildBlock, /groupVisibleCounts,\s*\}\);/);
});

test("the footer row renders show more / show less with i18n and reveal/reset clicks", () => {
  const footerBlock = source.slice(
    source.indexOf('row.kind === "groupMore" ? ('),
    source.indexOf("row.kind === \"groupMore\" ? (") + 2400,
  );
  assert.ok(footerBlock.includes("handlePinnedGroupShowMore"), "show-more handler wired");
  assert.ok(footerBlock.includes("handlePinnedGroupShowLess"), "show-less handler wired");
  assert.match(footerBlock, /t\("sidebar\.showMore", \{ count: row\.hiddenCount \}\)/);
  assert.match(footerBlock, /t\("sidebar\.showLess"\)/);
  // The footer sits inside the group content container (aria-controls
  // region), positioned like the group's other rows.
  assert.match(footerBlock, /top: row\.offset - contentTop/);
  // Handlers: +STEP uncapped reveal; show-less deletes the entry.
  const moreBlock = source.slice(
    source.indexOf("const handlePinnedGroupShowMore = useCallback"),
    source.indexOf("const handlePinnedGroupShowLess = useCallback"),
  );
  assert.match(moreBlock, /current \+ SHOW_MORE_STEP/);
  const lessBlock = source.slice(
    source.indexOf("const handlePinnedGroupShowLess = useCallback"),
    source.indexOf("}, []);", source.indexOf("const handlePinnedGroupShowLess = useCallback")) + 1,
  );
  assert.match(lessBlock, /delete next\[groupKey\];/);
});

test("an unpinned group leaves no stale pagination entry behind", () => {
  const pruneBlock = source.slice(
    source.indexOf("const pinnedGroupKeysSignature ="),
    source.indexOf("}, [pinnedGroupKeysSignature]);"),
  );
  assert.match(pruneBlock, /setPinnedShownExtra/);
  assert.match(pruneBlock, /keys\.has\(key\)/);
});
