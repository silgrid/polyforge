import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// Split view is the default desktop layout (pi-web d344694), but the pane
// ChatWindows never received onOpenFile. MarkdownBody therefore rendered every
// local-file link with target="_blank" in the pane that users actually see:
// clicking a PDF page link opened a bare popup and the file viewer never
// mounted. The classic full-width ChatWindow was wired, so
// this only regressed in split view.
//
// The pane must authorize the open against its OWN session id. Pane focus
// only updates focusedPaneId, never selectedSession, so sharing the classic
// handler (which reads selectedSession.id) would tag a non-selected pane's
// link with the wrong session and /api/files would answer 403 for a file
// that only that pane's session references outside the allowed roots.
const source = await readFile(new URL("./AppShell.tsx", import.meta.url), "utf8");

const renderPaneStart = source.indexOf("renderPane={(sid, focused) => {");
assert.ok(renderPaneStart >= 0, "AppShell must define a split renderPane");
const classicStart = source.indexOf(") : showChat ? (", renderPaneStart);
assert.ok(classicStart > renderPaneStart, "the split branch must be followed by the classic ChatWindow");
const classicEnd = source.indexOf(') : initialCwdStatus === "validating" ? (', classicStart);
assert.ok(classicEnd > classicStart, "the classic ChatWindow branch must be delimited");
const renderPane = source.slice(renderPaneStart, classicStart);
const classicWindow = source.slice(classicStart, classicEnd);

const sentinelStart = renderPane.indexOf("if (isNewSessionTab(sid)) {");
assert.ok(sentinelStart >= 0, "renderPane must have a sentinel (new-session tab) branch");
const sessionPane = renderPane.slice(renderPane.indexOf("const paneSession", sentinelStart));

test("the split session pane forwards local-file links to the in-app viewer with its own session id", () => {
  assert.match(
    sessionPane,
    /onOpenFile=\{getPaneOpenFileHandler\(sid\)\}/,
    "the session pane must open a linked file through a handler bound to its own sid",
  );
  assert.match(
    source,
    /const handler = \(filePath: string, page\?: number\) => \{\s*handleOpenFileRef\.current\(filePath, getFileName\(filePath\), \{ sourceSessionId: sid, page \}\);\s*\}/,
    "the pane handler must pass sourceSessionId=sid, never the classic selectedSession id",
  );
  assert.doesNotMatch(
    sessionPane,
    /onOpenFile=\{handleOpenLinkedFile\}/,
    "the pane must not reuse the classic handler: it authorizes against selectedSession, which pane focus never updates",
  );
});

test("the classic full-width ChatWindow keeps its file-link wiring (no regression)", () => {
  assert.match(classicWindow, /onOpenFile=\{handleOpenLinkedFile\}/);
});
