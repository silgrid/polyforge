import { NextResponse } from "next/server";
import { existsSync, statSync } from "fs";
import {
  attachSessionProjectInfo,
  openSessionManager,
  resolveSessionPath,
  resolveSessionIdByPath,
  invalidateSessionListCache,
  invalidateSessionManagerCache,
  buildSessionContext,
} from "@/lib/session-reader";
import { cachedSessionBuild } from "@/lib/context-build-cache";
import { getRpcSession, renameRpcSession, subagentDisplayStatus } from "@/lib/rpc-manager";
import { projectTreeForResponse, toSummaryTree } from "@/lib/project-tree";
import { computeSessionTotalActiveMs } from "@/lib/session-timing";
import { computeSessionStats } from "@/lib/session-stats";
import { startServerPerf } from "@/lib/perf";
import { computeSessionRevision } from "@/lib/session-revision";
import type { SessionEntry } from "@/lib/types";
import { readSubagentRun, readSubagentSessionResources } from "@/lib/subagents";
import { readSessionToolSelection } from "@/lib/session-tool-selection";
import { jsonResponse } from "@/lib/json-response";
import { deleteSessions } from "@/lib/session-delete";
import { attachTranscriptNotices, readSessionTranscriptNotices } from "@/lib/transcript-notices-server";
import { getClaudeSessionRecord, removeClaudeSessionRecord, updateClaudeSessionRecord, type ClaudeSessionRecord } from "@/lib/claude-index";
import { claudeSessionInfo, claudeSessionStats, getClaudeSession, readClaudeHistory } from "@/lib/claude-session";
import { removeClaudeTranscript } from "@/lib/claude-config-dir";
import { CLAUDE_PROVIDER } from "@/lib/claude-shared";
import { normalizeToolCalls } from "@/lib/normalize";

/**
 * A claude session's detail: the index's row, and the history read from
 * claude's own transcript through the adapter, with the subagent runs rebuilt from its
 * sidecars. No branch tree: the history is the transcript's active branch
 * alone, with the user messages an in-session edit can start from.
 */
async function claudeSessionDetail(req: Request, record: ClaudeSessionRecord) {
  const history = readClaudeHistory(record.id);
  const [info] = await attachSessionProjectInfo([claudeSessionInfo(record, history)]);
  const messages = history.messages.map(normalizeToolCalls);
  return jsonResponse(req, {
    sessionId: record.id,
    filePath: history.file ?? "",
    info,
    leafId: history.entryIds.at(-1) ?? null,
    tree: [],
    // The transcript grows behind pi-web's back: never cached as fresh.
    snapshotRevision: null,
    context: {
      messages,
      entryIds: history.entryIds,
      oldestEntryId: history.entryIds[0] ?? null,
      hasMore: false,
      turnsBefore: 0,
      thinkingLevel: "off",
      model: { provider: CLAUDE_PROVIDER, modelId: record.model },
    },
    stats: claudeSessionStats(record, history.messages),
    totalActiveMs: 0,
    // claude's tools are the user's claude config's; pi-web selects none.
    toolNames: [],
    // Its subagents' runs, their entries from claude's sidecars.
    claudeSubagents: history.subagents,
    // The user messages 「从此处编辑」 can start from.
    claudeEditEntryIds: history.editEntryIds,
  });
}

export async function GET(
  req: Request,
  { params }: { params: Promise<{ id: string }> }
) {
  const { id } = await params;
  const perf = startServerPerf("GET /api/sessions/[id]");
  try {
    const claudeRecord = getClaudeSessionRecord(id);
    if (claudeRecord) return await claudeSessionDetail(req, claudeRecord);
    perf?.span("resolve");
    const rpc = getRpcSession(id);
    const searchParams = new URL(req.url).searchParams;
    const force = searchParams.get("force") === "1";

    // A live wrapper only reflects the appends pi-web itself made. When another
    // pi process (the TUI) writes the same session file, the in-memory index
    // stays stale. Only probe on ?force=1 (session mount / page refresh): two
    // processes writing one JSONL is unsupported, so post-turn reads must not
    // scan disk. Eviction is idle-only; mid-run the wrapper owns the write path.
    let liveWrapper = rpc?.isAlive() ? rpc : undefined;
    let wrapperRebuilt = false;
    if (force && liveWrapper?.evictIfDiskAhead()) {
      wrapperRebuilt = true;
      liveWrapper = undefined;
    }
    const liveRpc = liveWrapper;
    const resolvedPath = liveRpc ? null : await resolveSessionPath(id);
    if (!liveRpc && !resolvedPath) {
      return NextResponse.json({ error: "Session not found" }, { status: 404 });
    }

    const sm = liveRpc?.inner.sessionManager ?? openSessionManager(resolvedPath!);
    perf?.span("open");
    const filePath = liveRpc?.sessionFile || sm.getSessionFile() || resolvedPath || "";
    const entries = sm.getEntries();
    const leafId = sm.getLeafId();
    const header = sm.getHeader();
    const summaryTree = searchParams.get("tree") === "summary";
    const deferThinking = searchParams.has("deferThinking");
    const deferToolResultImages = searchParams.has("deferMedia");
    const rawTail = Number(searchParams.get("tail"));
    const tail = Number.isFinite(rawTail) && rawTail > 0 ? Math.min(rawTail, 1000) : 50;
    // pi's showCacheMissNotices (#1265): cache, cost and provider notices on the messages they follow.
    const transcriptNotices = readSessionTranscriptNotices({
      entries: entries as unknown as SessionEntry[],
      cwd: sm.getHeader()?.cwd ?? "",
      leafId: leafId,
      models: liveRpc?.inner.modelRuntime,
    });
    // pi#83: the pure builds below (tree projection, context window, stats,
    // subagent scan) are memoized against the on-disk fingerprint, so re-opening
    // a tab or the client's background freshness read costs ~nothing when
    // nothing appended. Live RPC sessions bypass the cache (they mutate).
    const bundle = cachedSessionBuild(
      liveRpc ? "" : (resolvedPath || sm.getSessionFile() || ""),
      { route: "detail", summaryTree, deferThinking, deferToolResultImages, tail, leafId },
      () => {
        const builtTree = summaryTree
          ? toSummaryTree(projectTreeForResponse(sm.getTree()))
          : projectTreeForResponse(sm.getTree());
        const builtContext = buildSessionContext(entries as never, leafId, {
          deferThinking,
          deferToolResultImages,
          tail,
          sessionId: id, // local: lazy URLs for historical tool-result images
        });
        return {
          tree: builtTree,
          context: builtContext,
          stats: computeSessionStats(entries as unknown as SessionEntry[]),
          totalActiveMs: computeSessionTotalActiveMs(entries),
          subagent: header
            ? readSubagentRun(entries as never, header.id, filePath)
            : null,
          toolNames: readSubagentSessionResources(entries as never)?.tools
            ?? readSessionToolSelection(entries as never),
        };
      },
    );
    const tree = bundle.tree;
    const context = attachTranscriptNotices(bundle.context, transcriptNotices);
    const stats = bundle.stats;
    const totalActiveMs = bundle.totalActiveMs;
    const subagent = bundle.subagent;
    const toolNames = bundle.toolNames;
    perf?.span("context");
    // Opaque freshness token for the session view cache. Derived from the
    // disk fingerprint and the actual read source; null tells the client the
    // snapshot is unstable and must not be cached as fresh.
    const latestEntry = entries[entries.length - 1] as { id?: string } | undefined;
    const snapshotRevision = computeSessionRevision({
      filePath,
      // The notices switch changes the messages without touching the file.
      sourceId: `${liveRpc ? `runtime:${String(liveRpc.inner.sessionId)}` : "disk"}${transcriptNotices ? "+notices" : ""}`,
      entryCount: entries.length,
      latestEntryId: typeof latestEntry?.id === "string" ? latestEntry.id : null,
      leafId: leafId ?? null,
    });
    const sessionName = sm.getSessionName();
    const firstUserEntry = entries.find((entry) => entry.type === "message" && entry.message.role === "user");
    const firstUserMessage = firstUserEntry?.type === "message" ? firstUserEntry.message : undefined;

    let modified = header?.timestamp ?? new Date().toISOString();
    try { modified = statSync(filePath).mtime.toISOString(); } catch { /* use header timestamp */ }
    const parentSessionId = header?.parentSession
      ? await resolveSessionIdByPath(header.parentSession)
      : undefined;
    const info = header ? (await attachSessionProjectInfo([{
      path: filePath,
      id: header.id,
      cwd: header.cwd ?? "",
      name: sessionName,
      created: header.timestamp,
      modified,
      messageCount: stats.totalMessages,
      firstMessage: firstUserMessage
        ? (() => {
            const c = (firstUserMessage as { content: unknown }).content;
            return typeof c === "string" ? c : (Array.isArray(c) ? (c.find((b: { type: string }) => b.type === "text") as { text: string } | undefined)?.text ?? "" : "") || "(no messages)";
          })()
        : "(no messages)",
      parentSessionId,
      ...(subagent
        ? { relation: { kind: "subagent" as const, parentSessionId: subagent.parentSessionId, profile: subagent.profile, description: subagent.description, status: subagentDisplayStatus(header.id, subagent.status) } }
        : header.parentSession
          ? { relation: { kind: "fork" as const, ...(parentSessionId ? { originSessionId: parentSessionId } : {}) } }
          : {}),
      transient: !filePath || !existsSync(filePath),
    }]))[0] : null;

    return perf?.attach(jsonResponse(
      req,
      {
        sessionId: id,
        filePath,
        info,
        leafId,
        tree,
        ...(summaryTree ? { treeFormat: "summary" as const } : {}),
        snapshotRevision,
        context,
        stats,
        totalActiveMs,
        ...(toolNames !== undefined ? { toolNames } : {}),
        ...(wrapperRebuilt ? { wrapperRebuilt: true } : {}),
      },
    )) ?? jsonResponse(
      req,
      {
        sessionId: id,
        filePath,
        info,
        leafId,
        tree,
        ...(summaryTree ? { treeFormat: "summary" as const } : {}),
        snapshotRevision,
        context,
        stats,
        totalActiveMs,
        ...(toolNames !== undefined ? { toolNames } : {}),
        ...(wrapperRebuilt ? { wrapperRebuilt: true } : {}),
      },
    );
  } catch (error) {
    return NextResponse.json({ error: String(error) }, { status: 500 });
  }
}

// PATCH /api/sessions/[id]  body: { name: string } — a claude session also { archived: boolean }
export async function PATCH(
  req: Request,
  { params }: { params: Promise<{ id: string }> }
) {
  const { id } = await params;
  try {
    if (getClaudeSessionRecord(id)) {
      const { name, archived } = await req.json() as { name?: unknown; archived?: unknown };
      if ((name !== undefined && typeof name !== "string") || (archived !== undefined && typeof archived !== "boolean") || (name === undefined && archived === undefined)) {
        return NextResponse.json({ error: "name or archived is required" }, { status: 400 });
      }
      const updated = await updateClaudeSessionRecord(id, (record) => {
        const next: ClaudeSessionRecord = { ...record };
        if (typeof name === "string") {
          if (name.trim()) next.name = name.trim();
          else delete next.name;
        }
        if (archived === true) next.archived = true;
        if (archived === false) delete next.archived;
        return next;
      });
      return updated ? NextResponse.json({ ok: true }) : NextResponse.json({ error: "Session not found" }, { status: 404 });
    }
    const { name } = await req.json() as { name?: string };
    if (typeof name !== "string") {
      return NextResponse.json({ error: "name is required" }, { status: 400 });
    }
    const filePath = await resolveSessionPath(id);
    const trimmed = name.trim();
    // A session a wrapper holds is renamed in it, never on disk behind its back (#1388).
    const renamed = await renameRpcSession(id, trimmed, () => {
      if (!filePath) return false;
      // No wrapper: appendSessionInfo on a fresh manager, bypassing the cache.
      const sm = openSessionManager(filePath, { mutable: true });
      sm.appendSessionInfo(trimmed);
      invalidateSessionManagerCache(filePath);
      invalidateSessionListCache();
      return true;
    });
    if (!renamed) {
      return NextResponse.json({ error: "Session not found" }, { status: 404 });
    }
    return NextResponse.json({ ok: true });
  } catch (error) {
    return NextResponse.json({ error: String(error) }, { status: 500 });
  }
}

// DELETE /api/sessions/[id] - the session and the subagent sessions below it (lib/session-delete.ts).
export async function DELETE(
  _req: Request,
  { params }: { params: Promise<{ id: string }> }
) {
  const { id } = await params;
  try {
    // A claude session: its child, its index entry and its own transcript, nothing else.
    if (getClaudeSessionRecord(id)) {
      await getClaudeSession(id)?.shutdown();
      removeClaudeTranscript(id);
      await removeClaudeSessionRecord(id);
      return NextResponse.json({ ok: true });
    }
    const { missing } = await deleteSessions([id]);
    if (missing.length > 0) {
      return NextResponse.json({ error: "Session not found" }, { status: 404 });
    }
    return NextResponse.json({ ok: true });
  } catch (error) {
    return NextResponse.json({ error: String(error) }, { status: 500 });
  }
}
