import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import test from "node:test";
import vm from "node:vm";
import { createJiti } from "jiti";

const source = await readFile(new URL("./SessionSidebar.tsx", import.meta.url), "utf8");
const jiti = createJiti(import.meta.url);
const { getRecentProjects } = await jiti.import("../lib/project-groups.ts");
const { workspaceKeyOf } = await jiti.import("../lib/workspace-memory.ts");

// polyforge#252 r1#1: the entry auto-selection runs on the catalogue in hand.
// After pi-web deletes the globally last session, AppShell moves the workspace
// to "no project" and the sidebar clears selectedCwd — but the catalogue it
// still holds is the pre-delete one, so a second auto-selection would put the
// deleted project back and every request would keep its now-unauthorized cwd.
// The effect must choose the entry workspace once, not on every return to null.
function effectBody() {
  const start = source.indexOf("  // Auto-select cwd and restore session from URL on first load");
  const end = source.indexOf("  const commitCustomPath = useCallback", start);
  assert.notEqual(start, -1, "the entry auto-selection effect is not in SessionSidebar.tsx");
  assert.notEqual(end, -1, "commitCustomPath not found after the entry auto-selection effect");
  return source.slice(start, end);
}

const projectA = "/work/project-a";
const projectB = "/work/project-b";
// The selected session is in a worktree of project A; the catalogue's display
// root is project A.
const deleted = { id: "deleted-session", cwd: `${projectA}/wt-gone`, projectRoot: projectA, projectKey: projectA, modified: "2026-08-23T00:00:00.000Z" };
const survivor = { id: "survivor-session", cwd: projectB, projectRoot: projectB, projectKey: projectB, modified: "2026-08-21T00:00:00.000Z" };

test("a workspace cleared after a delete is not re-selected from the stale catalogue", () => {
  const selectedCwds = [];
  const context = vm.createContext({
    allSessions: [deleted, survivor],
    selectedCwd: null,
    initialSessionId: null,
    skipInitialProjectSelection: false,
    restoredRef: { current: false },
    initialWorkspaceSelectedRef: { current: false },
    getRecentProjects,
    workspaceKeyOf,
    setSelectedCwd: (cwd) => { selectedCwds.push(cwd); },
    setExplorerSelection() {},
    onSelectSession() {},
    onInitialRestoreDone() {},
    useEffect: (callback) => callback(),
  });
  const run = () => vm.runInContext(stripTypeScriptTypes(effectBody()), context);

  // First load: the most recently active project is the entry workspace.
  run();
  assert.deepEqual(selectedCwds, [projectA], "the entry workspace is the most recent project");

  // AppShell moves off the deleted session (workspaceSwitch root null); the
  // catalogue in hand still lists the deleted session and its project.
  context.selectedCwd = null;
  run();
  assert.deepEqual(selectedCwds, [projectA],
    "a deleted project must not be re-selected from the pre-delete catalogue");
});
