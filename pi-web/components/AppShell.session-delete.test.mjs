import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import test from "node:test";
import vm from "node:vm";
import { createJiti } from "jiti";

const source = await readFile(new URL("./AppShell.tsx", import.meta.url), "utf8");
const jiti = createJiti(import.meta.url);
const { workspaceAfterSessionRemoval } = await jiti.import("../lib/project-groups.ts");
const { workspaceKeyOf } = await jiti.import("../lib/workspace-memory.ts");

// The callbacks are run in a vm sandbox (polyforge#252). The workspace
// decision and the key derivation are the real ones; only React state setters,
// the router and the refs are stubbed, so a case asserts the cwd the interface
// hands downstream after a session vanished.
function sourceBetween(startMarker, endMarker) {
  const start = source.indexOf(startMarker);
  const end = source.indexOf(endMarker, start);
  assert.notEqual(start, -1, `callback not found: ${startMarker}`);
  assert.notEqual(end, -1, `end marker not found after ${startMarker}: ${endMarker}`);
  return source.slice(start, end);
}

// moveOffVanishedSession is declared immediately before handleSessionsChange.
const moveOffBody = sourceBetween(
  "  const moveOffVanishedSession = useCallback",
  "  const handleSessionsChange = useCallback",
);
const sessionsChangeBody = sourceBetween(
  "  const handleSessionsChange = useCallback",
  "  // Sidebar-detected external write",
);
const sessionDeletedBody = sourceBetween(
  "  const handleSessionDeleted = useCallback",
  "  const handleOpenFile = useCallback",
);

const projectA = "/work/project-a";
const projectB = "/work/project-b";
// The vanished session lives in a worktree of project A: its cwd differs from
// the project root, so a "keep the project" result must name the remaining
// session's root, never the gone cwd (polyforge#252 r1#7).
const gone = { id: "gone-session", cwd: `${projectA}/wt-gone`, projectRoot: projectA, projectKey: projectA, modified: "2026-08-23T00:00:00.000Z" };
const sameProject = { id: "same-project", cwd: `${projectA}/worktree`, projectRoot: projectA, projectKey: projectA, modified: "2026-08-22T00:00:00.000Z" };
const otherProject = { id: "other-project", cwd: projectB, projectRoot: projectB, projectKey: projectB, modified: "2026-08-21T00:00:00.000Z" };

/**
 * Runs `body` + `call` in a sandbox whose selection is `active`.
 * `reported` primes the ids the last catalogue report carried; a reported
 * catalogue is what lets the external-delete guard tell "deleted elsewhere"
 * from "not in the list yet" (a fork still landing).
 */
function run(active, sessionCatalog, body, call, reported = []) {
  const state = {
    setSelectedSession: [],
    setNewSessionCwd: [],
    setActiveCwd: [],
    workspaceSwitch: undefined,
    newSessionDraftId: null,
    sessionKey: 0,
    routerReplaced: null,
    refreshBumped: false,
    sessionCatalog: undefined,
  };
  const context = vm.createContext({
    crypto: globalThis.crypto,
    window: { location: { pathname: "/", search: "" } },
    router: { replace: (url) => { state.routerReplaced = url; } },
    workspaceAfterSessionRemoval,
    workspaceKeyOf,
    sessionCatalog,
    selectedSessionRef: { current: active },
    workspaceSwitchTokenRef: { current: 0 },
    reportedSessionIdsRef: { current: new Set(reported) },
    activeNewSessionDraftKeyRef: { current: null },
    invalidateWorkspaceRestore() {},
    clearTabOpenSession() {},
    setRefreshKey: () => { state.refreshBumped = true; },
    setSessionCatalog: (sessions) => { state.sessionCatalog = sessions; },
    setSessionCatalogReported: () => {},
    setSelectedSession: (value) => { state.setSelectedSession.push(value); },
    setNewSessionDraftId: (id) => { state.newSessionDraftId = id; },
    setNewSessionCwd: (value) => { state.setNewSessionCwd.push(value); },
    setActiveCwd: (value) => { state.setActiveCwd.push(value); },
    setWorkspaceSwitch: (value) => { state.workspaceSwitch = value; },
    setSessionKey: (fn) => { state.sessionKey = fn(state.sessionKey); },
    setBranchTree() {},
    setBranchActiveLeafId() {},
    setBranchSwitchLocked() {},
    setSystemPrompt() {},
    setSystemTools() {},
    setSystemInfoLoading() {},
    setActiveTopPanel() {},
    useCallback: (callback) => callback,
    useEffect: () => {},
  });
  vm.runInContext(stripTypeScriptTypes(`${moveOffBody}\n${body}\n${call}\n`), context);
  return state;
}

const deleteSession = (active, catalog) => run(active, catalog, sessionDeletedBody, 'handleSessionDeleted("gone-session");');

/** Runs the catalogue callback once per catalog in `catalogs`, in order. */
function reportCatalog(active, catalogs, reported = []) {
  const calls = catalogs.map((_, index) => `handleSessionsChange(catalogs[${index}]);`).join("\n");
  return run(active, undefined, sessionsChangeBody, `const catalogs = ${JSON.stringify(catalogs)};\n${calls}`, reported);
}

test("a delete completing after the user navigated away keeps the new chat", () => {
  const state = deleteSession(otherProject, [otherProject]);
  assert.equal(state.refreshBumped, true, "sidebar list still refreshes");
  assert.deepEqual(state.setSelectedSession, [], "selection is untouched");
  assert.deepEqual(state.setNewSessionCwd, [], "no empty composer");
  assert.deepEqual(state.setActiveCwd, [], "the workspace does not move");
  assert.equal(state.workspaceSwitch, undefined);
  assert.equal(state.newSessionDraftId, null);
  assert.equal(state.sessionKey, 0, "chat does not remount");
  assert.equal(state.routerReplaced, null, "URL does not move");
});

test("deleting a session whose project has no sessions left moves off its cwd", () => {
  const state = deleteSession(gone, [gone]);
  assert.equal(state.refreshBumped, true);
  assert.deepEqual(state.setSelectedSession, [null]);
  assert.deepEqual(state.setNewSessionCwd, [null], "the vanished cwd is never the composer's");
  assert.deepEqual(state.setActiveCwd, [null], "no component keeps fetching with the vanished cwd");
  assert.equal(state.workspaceSwitch.root, null, "the sidebar drops the project");
  assert.equal(state.workspaceSwitch.key, null, "the dropped project keeps no identity");
  assert.equal(state.workspaceSwitch.token, 1);
  assert.equal(state.routerReplaced, "/", "the URL keeps no cwd");
});

test("deleting a session keeps the project while another of its sessions remains", () => {
  const state = deleteSession(gone, [gone, sameProject]);
  assert.deepEqual(state.setSelectedSession, [null]);
  assert.deepEqual(state.setNewSessionCwd, [projectA], "the composer stays in the project, at its authorized root");
  assert.deepEqual(state.setActiveCwd, [projectA]);
  assert.equal(state.workspaceSwitch.root, projectA, "the sidebar keeps the project");
  assert.equal(state.workspaceSwitch.key, projectA);
  assert.equal(state.workspaceSwitch.token, 1);
  assert.equal(state.routerReplaced, `?cwd=${encodeURIComponent(projectA)}`);
});

test("deleting the last session of a project switches to the most recent remaining project", () => {
  const state = deleteSession(gone, [gone, otherProject]);
  assert.deepEqual(state.setSelectedSession, [null]);
  assert.deepEqual(state.setNewSessionCwd, [projectB]);
  assert.deepEqual(state.setActiveCwd, [projectB]);
  assert.equal(state.workspaceSwitch.root, projectB, "the sidebar follows the switch");
  assert.equal(state.workspaceSwitch.key, projectB);
  assert.equal(state.workspaceSwitch.token, 1);
  assert.equal(state.routerReplaced, `?cwd=${encodeURIComponent(projectB)}`);
});

test("a refreshed catalogue that no longer carries a reported selected session moves the workspace too", () => {
  // The first report carries the session; the second no longer does (an
  // external delete somewhere between the two).
  const state = reportCatalog(gone, [[gone, otherProject], [otherProject]], [gone.id]);
  assert.equal(state.setSelectedSession.filter((value) => value === null).length, 1,
    "the vanished selection is cleared once");
  assert.equal(state.setSelectedSession.at(-1), null);
  assert.deepEqual(state.setNewSessionCwd, [projectB], "the external delete moves the composer off the gone cwd");
  assert.deepEqual(state.setActiveCwd, [projectB]);
  assert.equal(state.workspaceSwitch.root, projectB);
  assert.equal(state.routerReplaced, `?cwd=${encodeURIComponent(projectB)}`);
});

test("a refreshed catalogue that still carries the selected session keeps the workspace", () => {
  const state = reportCatalog(gone, [[gone, otherProject]], [gone.id]);
  assert.deepEqual(state.setNewSessionCwd, []);
  assert.deepEqual(state.setActiveCwd, []);
  assert.equal(state.workspaceSwitch, undefined);
  assert.equal(state.routerReplaced, null);
});

test("a selected session no catalogue has reported yet is not treated as vanished", () => {
  // A fork (or a just-created session) is selected before the list carries
  // it; a stale report must not kick the user out of it (polyforge#252 r1#8).
  const state = reportCatalog(gone, [[otherProject]], []);
  assert.deepEqual(state.setNewSessionCwd, []);
  assert.deepEqual(state.setActiveCwd, []);
  assert.equal(state.workspaceSwitch, undefined);
});

test("a transient new-session selection is not treated as a vanished session", () => {
  const transient = { ...gone, id: "draft-session", transient: true };
  const state = reportCatalog(transient, [[otherProject]], [transient.id]);
  assert.deepEqual(state.setNewSessionCwd, []);
  assert.deepEqual(state.setActiveCwd, []);
  assert.equal(state.workspaceSwitch, undefined);
});
