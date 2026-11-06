import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// pi#72 review fixes (F1/F2/F3): the "currently open" row highlight now
// derives from a REAL active-file identity threaded down as a prop instead
// of a local click echo, and compares paths through the repo's established
// normalization instead of raw `===`.

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
// Importing the modules is a parse smoke test.
await jiti.import("./FileExplorer.tsx");
await jiti.import("./MultiRootFileExplorer.tsx");

const { normalizeFilePathSlashes } = await jiti.import("../lib/file-paths.ts");

const source = await readFile(new URL("./FileExplorer.tsx", import.meta.url), "utf8");
const multiRootSource = await readFile(new URL("./MultiRootFileExplorer.tsx", import.meta.url), "utf8");

function sliceBetween(text, startMarker, endMarker) {
  const start = text.indexOf(startMarker);
  assert.ok(start !== -1, `marker not found: ${startMarker}`);
  const end = text.indexOf(endMarker, start);
  assert.ok(end !== -1, `end marker not found after ${startMarker}: ${endMarker}`);
  return text.slice(start, end);
}

// ---------------------------------------------------------------------------
// (a) Indent-guide geometry: the guide offsets must converge to each
// ancestor's own chevron absolute position under the (depth+1)*14 margin
// scheme (reviewer hand-verified; pinned here against regression).
// ---------------------------------------------------------------------------

test("indent-guide constants match the row's own margin scheme", () => {
  const stepMatch = source.match(/const INDENT_STEP = (\d+);/);
  const insetMatch = source.match(/const GUIDE_INSET = (\d+); \/\/ row paddingLeft \(8\) \+ half the chevron width \(5\)/);
  assert.ok(stepMatch, "INDENT_STEP constant must exist");
  assert.ok(insetMatch, "GUIDE_INSET constant must exist (documented as paddingLeft + half chevron width)");
  const INDENT_STEP = Number(stepMatch[1]);
  const GUIDE_INSET = Number(insetMatch[1]);

  // The row box itself is indented (depth + 1) * INDENT_STEP (pi#57), and a
  // row's own chevron sits GUIDE_INSET past its own left edge (8px padding
  // + half the 10px-wide chevron). So a row at depth `d`'s chevron sits at
  // absolute offset (d + 1) * INDENT_STEP + GUIDE_INSET from the tree's left
  // edge — the source's own row margin literal must agree with INDENT_STEP.
  assert.match(source, new RegExp(`marginLeft: \\(depth \\+ 1\\) \\* ${INDENT_STEP},`));

  const chevronAbsoluteLeft = (ancestorDepth) => (ancestorDepth + 1) * INDENT_STEP + GUIDE_INSET;
  // IndentGuides({ depth }) renders `depth` lines; line index i (0-based,
  // i=0 is the nearest ancestor) sits at GUIDE_INSET - INDENT_STEP*(i+1)
  // relative to the row's own (already indented) box.
  const guideAbsoluteLeft = (rowDepth, ancestorIndex) =>
    (rowDepth + 1) * INDENT_STEP + (GUIDE_INSET - INDENT_STEP * (ancestorIndex + 1));

  for (let rowDepth = 1; rowDepth <= 6; rowDepth++) {
    for (let ancestorIndex = 0; ancestorIndex < rowDepth; ancestorIndex++) {
      const ancestorDepth = rowDepth - 1 - ancestorIndex;
      assert.equal(
        guideAbsoluteLeft(rowDepth, ancestorIndex),
        chevronAbsoluteLeft(ancestorDepth),
        `row depth ${rowDepth}, ancestor index ${ancestorIndex} (ancestor depth ${ancestorDepth}) must align`,
      );
    }
  }
});

test("IndentGuides renders exactly one guide per ancestor level, none for a root row", () => {
  assert.match(source, /function IndentGuides\(\{ depth \}: \{ depth: number \}\) \{/);
  assert.match(source, /if \(depth <= 0\) return null;/);
  assert.match(source, /Array\.from\(\{ length: depth \}, \(_, i\) => \(/);
  assert.match(source, /left: GUIDE_INSET - INDENT_STEP \* \(i \+ 1\),/);
});

// ---------------------------------------------------------------------------
// (b) Active-row derivation: given an active file path, exactly the owning
// row is marked active, via a normalization-agnostic comparison.
// ---------------------------------------------------------------------------

test("TreeNode compares through normalizeFilePathSlashes, not raw ===, and only for files", () => {
  const activeBlock = sliceBetween(source, "const normalizedPath = normalizeFilePathSlashes(node.fullPath);", "const gitStatus = gitStatusByPath.get(normalizedPath);");
  assert.match(activeBlock, /const active = !node\.isDir && activeFilePath != null/);
  assert.match(activeBlock, /normalizeFilePathSlashes\(activeFilePath\) === normalizedPath;/);
  assert.doesNotMatch(source, /activeFilePath === node\.fullPath/, "raw equality must not remain");
});

test("ChangeRow's active flag is computed the same normalized way at the call site", () => {
  const block = sliceBetween(source, "{gitFiles.map((status) => (", "))}\n        </div>\n      )}");
  assert.match(block, /active=\{activeFilePath != null\s*&&\s*normalizeFilePathSlashes\(activeFilePath\) === normalizeFilePathSlashes\(status\.filePath\)\}/);
  assert.doesNotMatch(source, /active=\{activeFilePath === status\.filePath\}/, "raw equality must not remain");
});

test("normalizeFilePathSlashes makes the comparison separator-agnostic (real behavior, not just source shape)", () => {
  // git prints POSIX separators even on Windows; a node's own fullPath is
  // built with native-style joins. Simulate the mismatch directly against
  // the real helper the component imports.
  const windowsStyleNodePath = "C:\\repo\\src\\index.ts";
  const gitEmittedActivePath = "C:/repo/src/index.ts";
  assert.notEqual(windowsStyleNodePath, gitEmittedActivePath, "raw strings differ (this is exactly what F2 flags)");
  assert.equal(
    normalizeFilePathSlashes(windowsStyleNodePath),
    normalizeFilePathSlashes(gitEmittedActivePath),
    "normalized forms must agree",
  );

  // Replicate the exact TreeNode predicate against a small multi-root node
  // set and confirm EXACTLY the owning row is active, including through a
  // path that needs normalization.
  function isActive(isDir, nodeFullPath, activeFilePath) {
    const normalizedPath = normalizeFilePathSlashes(nodeFullPath);
    return !isDir && activeFilePath != null && normalizeFilePathSlashes(activeFilePath) === normalizedPath;
  }

  const rootA = ["C:\\repo\\src\\index.ts", "C:\\repo\\src\\other.ts"];
  const rootB = ["C:\\repo2\\readme.md"];
  const active = "C:/repo/src/index.ts"; // POSIX form, as git would emit it

  const activeInRootA = rootA.filter((p) => isActive(false, p, active));
  const activeInRootB = rootB.filter((p) => isActive(false, p, active));
  assert.deepEqual(activeInRootA, ["C:\\repo\\src\\index.ts"], "exactly the owning row in the owning root is active");
  assert.deepEqual(activeInRootB, [], "a non-owning root must not highlight anything");

  // A directory never highlights even if its path happens to match.
  assert.equal(isActive(true, "C:\\repo\\src\\index.ts", active), false);
  // No active path at all: nothing highlights.
  assert.equal(isActive(false, "C:\\repo\\src\\index.ts", null), false);
});

// ---------------------------------------------------------------------------
// F1: the active identity is a threaded prop, not a local click echo, and
// (c) it cannot change from unrelated interactions because there is no
// local state left for them to mutate.
// ---------------------------------------------------------------------------

test("FileExplorer takes activeFilePath as a prop; no local echo state remains", () => {
  // Props must declare activeFilePath (followed by later additive props such
  // as the showIgnoredFiles opt-in, wi pi#101).
  assert.match(source, /activeFilePath\?: string \| null;/, "Props must declare activeFilePath");
  assert.match(source, /activeFilePath = null,\n\s*showIgnoredFiles = false,\n\}, ref\) \{/, "component must destructure it as a prop with a null default");
  assert.doesNotMatch(source, /const \[activeFilePath, setActiveFilePath\] = useState/, "no local echo state may remain");
  assert.doesNotMatch(source, /onActivateFile/, "the click-to-echo callback must be fully removed");
  assert.doesNotMatch(source, /setActiveFilePath/, "no setter for a local echo may remain");
});

test("the cwd-change reset effect no longer touches an activeFilePath setter (nothing local to reset)", () => {
  const resetBlock = sliceBetween(source, "if (cwdChanged) {", "setLoading(cwdChanged);");
  assert.doesNotMatch(resetBlock, /setActiveFilePath/);
});

test("activeFilePath threads unchanged into every TreeNode/ChangeRow render site (main tree, search results, changes list)", () => {
  const occurrences = source.match(/activeFilePath=\{activeFilePath\}/g) ?? [];
  // Main tree + search-results tree both forward the prop by identity.
  assert.ok(occurrences.length >= 2, `expected at least 2 pass-through sites, found ${occurrences.length}`);
});

test("MultiRootFileExplorer forwards one activeFilePath prop unchanged to every mounted root", () => {
  assert.match(multiRootSource, /activeFilePath\?: string \| null;/);
  assert.match(multiRootSource, /activeFilePath=\{activeFilePath\}/);
  // Exactly one FileExplorer mount site per root; the same value reaches
  // every root, so only the owning root's own TreeNode comparison highlights.
  const mountCount = (multiRootSource.match(/activeFilePath=\{activeFilePath\}/g) ?? []).length;
  assert.equal(mountCount, 1, "MultiRootFileExplorer mounts FileExplorer from a single roots.map site");
});
