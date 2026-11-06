import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// Parse smoke test, same convention as the build-outputs suite.
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
await jiti.import("./FileExplorer.tsx");

const source = await readFile(new URL("./FileExplorer.tsx", import.meta.url), "utf8");
const multiRootSource = await readFile(new URL("./MultiRootFileExplorer.tsx", import.meta.url), "utf8");

function sliceBetween(text, startMarker, endMarker) {
  const start = text.indexOf(startMarker);
  assert.ok(start !== -1, `marker not found: ${startMarker}`);
  const end = text.indexOf(endMarker, start);
  assert.ok(end !== -1, `end marker not found after ${startMarker}: ${endMarker}`);
  return text.slice(start, end);
}

test("fetchEntries asks for ignored entries under the opt-in and carries the marker into nodes", () => {
  assert.match(source, /if \(opts\?\.showIgnored\) query\.set\("showIgnored", "1"\);/);
  // The server's additive marker flows into the node model untouched, in
  // both the wire entry type and the tree node type.
  assert.match(source, /interface FileEntry[\s\S]*?ignored\?: boolean;/);
  assert.match(source, /interface FileNode[\s\S]*?ignored\?: boolean;/);
  const mapBlock = sliceBetween(
    source,
    "return (data.entries ?? []).map((e) => ({",
    "}));",
  );
  assert.match(mapBlock, /ignored: e\.ignored,/);
});

test("ignored rows render dimmed with the reason in the tooltip", () => {
  // The row box dims (fork's visual language for surfaced-but-muted rows).
  assert.match(source, /opacity: node\.ignored \? 0\.55 : 1,/);
  // The name's tooltip carries the reason next to the path.
  assert.match(
    source,
    /title=\{node\.ignored \? `\$\{node\.fullPath\} \(\$\{t\("files\.ignoredByGit"\)\}\)` : node\.fullPath\}/,
  );
});

test("the showIgnoredFiles opt-in is threaded from the section root to every lazy child listing", () => {
  // Prop on the explorer, default off.
  assert.match(source, /showIgnoredFiles\?: boolean;/);
  assert.match(source, /showIgnoredFiles = false,/);
  // The root effect depends on it (in-place re-fetch on a toggle flip)...
  assert.match(source, /fetchEntries\(cwd, \{ showBuildOutputs, showIgnored: showIgnoredFiles \}\)/);
  // ...and the flip bumps the shared refresh token so every expanded
  // TreeNode re-fetches (the build-outputs broadcast pattern).
  const flipBlock = sliceBetween(
    source,
    "const prevShowIgnoredFilesRef = useRef(showIgnoredFiles);",
    "}, [showIgnoredFiles]);",
  );
  assert.match(flipBlock, /setTreeRefreshKey\(\(key\) => key \+ 1\);/);
  // TreeNode receives and forwards it, both in its own fetch and in the
  // recursive child render.
  assert.match(source, /fetchEntries\(node\.fullPath, \{\s*showBuildOutputs: getShowBuildOutputs\(\),\s*showIgnored: showIgnoredFiles,\s*\}\)/);
  const recurseBlock = sliceBetween(
    source,
    "children.map((child) => (\n            <TreeNode",
    "key={child.fullPath}",
  );
  assert.ok(
    sliceBetween(source, "children.map((child) => (\n            <TreeNode", "{children.length === 0 && loaded && !loadError").includes("showIgnoredFiles={showIgnoredFiles}"),
    "the recursive child render must forward showIgnoredFiles",
  );
  // The main tree's root call site forwards it too (search results, which
  // come from the bounded file-index surface, are not ignored-marked).
  const mainTreeBlock = sliceBetween(source, "roots.map((node) => (", "<div style={{ padding: \"8px 12px\", fontSize: 11, color: \"var(--text-dim)\" }}>");
  assert.ok(mainTreeBlock.includes("showIgnoredFiles={showIgnoredFiles}"));
});

test("MultiRootFileExplorer renders exactly one global ignored toggle and passes it to every section", () => {
  const checkboxCount = (multiRootSource.match(/checked=\{showIgnoredFiles\}/g) ?? []).length;
  assert.equal(checkboxCount, 1, "exactly one ignored-files checkbox");
  assert.match(multiRootSource, /const \[showIgnoredFiles, setShowIgnoredFiles\] = useState\(false\);/);
  assert.match(multiRootSource, /setShowIgnoredFiles\(\(prev\) => !prev\);/);
  // The mounted section render receives the flag; each section's fetch runs
  // git against its own root — that is the multi-root semantics.
  const sectionCount = (multiRootSource.match(/showIgnoredFiles=\{showIgnoredFiles\}/g) ?? []).length;
  assert.equal(sectionCount, 1, "every mounted section receives the flag");
  assert.match(multiRootSource, /aria-label=\{t\("sidebar\.showIgnoredFiles"\)\}/);
  assert.match(multiRootSource, /title=\{t\("sidebar\.showIgnoredFilesHint"\)\}/);
  // The toggle is deliberately NOT persisted: no storage call rides along
  // (the state declaration line itself is excluded from the check).
  const toggleBlock = sliceBetween(
    multiRootSource,
    "const handleToggleShowIgnoredFiles = useCallback(() => {",
    "}, []);",
  );
  assert.ok(!toggleBlock.includes("localStorage"), "the ignored toggle must not persist");
  assert.match(toggleBlock, /setShowIgnoredFiles\(\(prev\) => !prev\);/);
});
