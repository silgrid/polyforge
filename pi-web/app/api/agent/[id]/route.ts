import { NextResponse } from "next/server";
import { resolveSessionPath } from "@/lib/session-reader";
import { getRpcSession, isSessionReloadedFromDiskError, setRpcSessionTools, startRpcSession } from "@/lib/rpc-manager";
import { cwdMissingBody, missingCwdFromBody } from "@/lib/missing-cwd";
import { getClaudeSessionRecord } from "@/lib/claude-index";
import {
  CLAUDE_SESSION_COMMANDS,
  claudeCommandEffort,
  claudeLaunchPermissionModeRefusal,
  claudeSessionApprovalMode,
  claudeCommandEntryId,
  claudeCommandModel,
  claudeCommandTaskId,
  claudeCommandToolCallId,
  editClaudeSession,
  forkClaudeSession,
  getClaudeSession,
  setIdleClaudeSetting,
  startClaudeSession,
} from "@/lib/claude-session";
import { validateClaudeImages } from "@/lib/image-attachments";

// POST /api/agent/[id] - Send a command to an existing session
export async function POST(
  req: Request,
  { params }: { params: Promise<{ id: string }> }
) {
  const { id } = await params;
  let commandType: string | undefined;
  let promptAccepted = false;

  try {
    const body = await req.json() as { type: string; [key: string]: unknown };
    commandType = typeof body.type === "string" ? body.type : undefined;
    const requestedToolNames = body.toolNames;
    if (
      requestedToolNames !== undefined
      && (!Array.isArray(requestedToolNames) || requestedToolNames.some((name) => typeof name !== "string"))
    ) {
      throw new Error("toolNames must be an array of strings");
    }
    const toolNames = requestedToolNames as string[] | undefined;

    // A claude session: its own child, started (`--resume`) for a command
    // when none is alive. Beside prompt/abort/state, it takes a model switch, an effort
    // change and claude's own compaction, a fork at one of its user
    // messages and a prompt that edits one in place, moving a running
    // foreground subagent to the background, a launch permission-mode switch
    // and the answer to a permission dialog on the live child;
    // every other pi-only command is refused, never routed to a pi wrapper, and starts nothing.
    const claudeRecord = getClaudeSessionRecord(id);
    if (claudeRecord) {
      if (!commandType || !CLAUDE_SESSION_COMMANDS.has(commandType)) {
        return NextResponse.json({ error: `${String(commandType)} is not available for claude sessions` }, { status: 400 });
      }
      // Images are checked before a child starts or anything reaches its stdin.
      const imageError = commandType === "prompt" ? validateClaudeImages(body.images) : null;
      if (imageError) {
        return NextResponse.json({ error: imageError, code: "prompt_rejected", accepted: false }, { status: 400 });
      }
      if (commandType === "set_model" && !claudeCommandModel(body)) {
        return NextResponse.json({ error: `Not a claude model: ${String(body.model ?? body.modelId)}` }, { status: 400 });
      }
      if (commandType === "set_effort" && !claudeCommandEffort(body)) {
        return NextResponse.json({ error: `Not a claude effort: ${String(body.effort)}` }, { status: 400 });
      }
      // an unknown mode, or plan for a session whose approvals are not the
      // ask (its live child's mode, else its next launch's; r3), touches nothing.
      const permissionModeRefusal = commandType === "set_permission_mode" ? claudeLaunchPermissionModeRefusal(body, claudeSessionApprovalMode(claudeRecord)) : null;
      if (permissionModeRefusal) return NextResponse.json({ error: permissionModeRefusal }, { status: 400 });
      if (commandType === "prompt" && body.entryId !== undefined) {
        const entryId = claudeCommandEntryId(body);
        if (!entryId) {
          return NextResponse.json({ error: "An edit needs the entryId of a user message", code: "prompt_rejected", accepted: false }, { status: 400 });
        }
        // An in-session edit: the same session resumed truncated before that message, then the prompt.
        const result = await editClaudeSession(claudeRecord, entryId, body);
        promptAccepted = true;
        return NextResponse.json({ success: true, data: result });
      }
      if (commandType === "fork") {
        const entryId = claudeCommandEntryId(body);
        if (!entryId) return NextResponse.json({ error: "A fork needs the entryId of a user message" }, { status: 400 });
        // A fork copies the transcript on disk: it needs no child and starts none.
        return NextResponse.json({ success: true, data: await forkClaudeSession(id, entryId) });
      }
      const live = getClaudeSession(id);
      // moving a subagent to the background acts on the running child only;
      // with none there is no subagent to move, so it is refused and spawns nothing.
      if (commandType === "background_task" && (!live || !claudeCommandToolCallId(body))) {
        const error = !live ? "background_task needs a running session: no live claude child" : "background_task needs the tool call id of a running Agent call";
        return NextResponse.json({ error }, { status: 400 });
      }
      // stopping a background task or reading its output acts on the running
      // child only (its tasks die with it), so with none it is refused and spawns nothing.
      if ((commandType === "stop_task" || commandType === "task_output") && (!live || !claudeCommandTaskId(body))) {
        const error = !live ? `${commandType} needs a running session: no live claude child` : `${commandType} needs the task id of a background task`;
        return NextResponse.json({ error }, { status: 400 });
      }
      // a permission dialog lives only in the live child's broker; with none
      // there is nothing to answer, so the response is refused and spawns nothing.
      if (commandType === "extension_ui_response" && !live) {
        return NextResponse.json({ error: "extension_ui_response needs a running session: no live claude child" }, { status: 400 });
      }
      // No live child: a switch is the index's alone and spawns nothing.
      if (!live && (commandType === "set_model" || commandType === "set_effort" || commandType === "set_permission_mode")) {
        return NextResponse.json({ success: true, data: await setIdleClaudeSetting(id, body) });
      }
      const claudeSession = live ?? startClaudeSession(claudeRecord);
      const result = await claudeSession.send(body);
      promptAccepted = body.type === "prompt";
      return NextResponse.json({ success: true, data: result });
    }

    // Fast path: already-running session
    const existing = getRpcSession(id);
    if (body.type === "set_tools") {
      const filePath = existing?.sessionFile || await resolveSessionPath(id) || undefined;
      if (!existing?.isAlive() && !filePath) {
        return NextResponse.json({ error: "Session not found" }, { status: 404 });
      }
      const changed = await setRpcSessionTools(id, filePath, toolNames);
      return NextResponse.json({
        success: true,
        data: { sessionId: changed.sessionId, recreated: changed.recreated },
      });
    }
    // Prompt submissions probe disk-ahead inside AgentSessionWrapper.send(),
    // AFTER prompt admission is acquired, so an external append between the
    // probe and the submission can no longer chain the prompt onto a stale
    // in-memory head and fork the session tree. When that probe evicts the
    // wrapper, send() throws SessionReloadedFromDiskError; fall through to
    // the cold-start path below so the prompt continues against freshly
    // loaded state and the response reports the reload to the client.
    let reloadedFromDisk = false;
    if (existing?.isAlive()) {
      try {
        const result = await existing.send(body);
        promptAccepted = body.type === "prompt";
        return NextResponse.json({ success: true, data: result });
      } catch (error) {
        if (!isSessionReloadedFromDiskError(error)) throw error;
        reloadedFromDisk = true;
      }
    }

    const filePath = await resolveSessionPath(id);
    if (!filePath) {
      return NextResponse.json({
        error: "Session not found",
        ...(body.type === "prompt"
          ? { code: "prompt_rejected", accepted: false }
          : {}),
      }, { status: 404 });
    }

    const { session } = await startRpcSession(id, filePath, undefined, {
      ...(toolNames !== undefined ? { toolNames } : {}),
    });
    const result = await session.send(body);
    promptAccepted = body.type === "prompt";

    // Prompt results are otherwise always null; report the external-write
    // reload through the prompt payload so the client can surface a notice.
    return NextResponse.json({
      success: true,
      data: reloadedFromDisk && body.type === "prompt" ? { reloadedFromDisk: true } : result,
    });
  } catch (error) {
    // Read by its fields: the wrapper that threw may come from another route's bundle.
    const missingCwd = missingCwdFromBody(error);
    return NextResponse.json({
      error: error instanceof Error ? error.message : String(error),
      ...(missingCwd ? cwdMissingBody(missingCwd) : {}),
      ...(commandType === "prompt" && !promptAccepted
        ? { code: "prompt_rejected", accepted: false }
        : {}),
    }, { status: 500 });
  }
}

// GET /api/agent/[id] - Get current agent state
export async function GET(
  req: Request,
  { params }: { params: Promise<{ id: string }> }
) {
  const { id } = await params;

  try {
    const claudeSession = getClaudeSession(id);
    if (claudeSession) return NextResponse.json({ running: true, state: await claudeSession.send({ type: "get_state" }) });
    const session = getRpcSession(id);
    if (!session || !session.isAlive()) {
      return NextResponse.json({ running: false });
    }

    const state = await session.send({ type: "get_state" }) as Record<string, unknown>;
    // Status reads leave out the system prompt, most of the reply (agentStatusUrl()).
    if (new URL(req.url).searchParams.get("systemPrompt") === "0") delete state.systemPrompt;
    return NextResponse.json({ running: true, state });
  } catch (error) {
    return NextResponse.json({ error: String(error) }, { status: 500 });
  }
}
