import { NextResponse } from "next/server";
import type { ThinkingLevel } from "@earendil-works/pi-agent-core";
import { existsSync } from "fs";
import { randomUUID } from "crypto";
import { allowFileRoot } from "@/lib/file-access";
import { isRegistrableRoot } from "@/lib/root-registration-policy";
import { invalidateSessionListCache } from "@/lib/session-reader";
import { startRpcSession } from "@/lib/rpc-manager";
import { cwdMissingBody } from "@/lib/missing-cwd";
import { readStoredAgentLanguage } from "@/lib/agent-language-config";
import { readClaudeSettings } from "@/lib/claude-settings";
import { EMPTY_CLAUDE_USAGE, removeClaudeSessionRecord, upsertClaudeSessionRecord } from "@/lib/claude-index";
import { findClaudeBinary, startClaudeSession } from "@/lib/claude-session";
import { validateClaudeImages } from "@/lib/image-attachments";
import {
  CLAUDE_PROVIDER,
  CLAUDE_EFFORT_DEFAULT,
  claudeFirstMessage,
  isClaudeEffort,
  isClaudeHarness,
  isClaudeModelName,
  isSessionHarness,
  type ClaudeEffort,
} from "@/lib/claude-shared";

const THINKING_LEVELS = new Set<ThinkingLevel>(["off", "minimal", "low", "medium", "high", "xhigh", "max"]);

function parseThinkingLevel(value: unknown): ThinkingLevel | undefined {
  if (value === undefined) return undefined;
  if (typeof value === "string" && THINKING_LEVELS.has(value as ThinkingLevel)) {
    return value as ThinkingLevel;
  }
  throw new Error(`Invalid thinking level: ${String(value)}`);
}
function rejected(commandType: string | undefined, body: Record<string, unknown>, status: number) {
  return NextResponse.json({
    ...body,
    ...(commandType === "prompt" ? { code: "prompt_rejected", accepted: false } : {}),
  }, { status });
}

/**
 * A new claude session (#250), after the cwd guard: claude must be on PATH,
 * then the index entry (the model name as given, effort, no preset; the Agent language and
 * Settings › Claude's permission mode and hook switch as they are now,
 * #445) and the first child
 * (`--session-id <new id>`, `--effort` for a non-default level).
 * The child is spawned before the reply; a prompt goes to its stdin.
 */
async function createClaudeSession(
  cwd: string,
  model: string,
  effort: ClaudeEffort,
  command: Record<string, unknown>,
  commandType: string | undefined,
) {
  if (!findClaudeBinary()) {
    return rejected(commandType, { error: "claude was not found on PATH" }, 400);
  }
  const now = new Date().toISOString();
  const claudeSettings = readClaudeSettings();
  const record = {
    id: randomUUID(),
    cwd,
    model,
    effort,
    // The server setting at creation; later launches read this record.
    agentLanguage: readStoredAgentLanguage(),
    // Settings › Claude at creation: a later change reaches new sessions only.
    launchPermissionMode: claudeSettings.launchPermissionMode,
    includeHookEvents: claudeSettings.includeHookEvents,
    created: now,
    modified: now,
    firstMessage: commandType === "prompt"
      ? claudeFirstMessage(typeof command.message === "string" ? command.message : "", Array.isArray(command.images) ? command.images.length : 0)
      : "",
    usage: { ...EMPTY_CLAUDE_USAGE },
  };
  await upsertClaudeSessionRecord(record);
  let session;
  try {
    session = startClaudeSession(record);
  } catch (error) {
    await removeClaudeSessionRecord(record.id);
    throw error;
  }
  allowFileRoot(cwd);
  const data = commandType === "prompt" ? await session.send(command) : null;
  return NextResponse.json({
    success: true,
    sessionId: record.id,
    data,
    model: { provider: CLAUDE_PROVIDER, modelId: model },
    thinkingLevel: "off",
    harness: "claude",
  });
}

// POST /api/agent/new  body: { cwd: string; type: string; message?: string; harness?: "pi" | "claude"; ... }
// Spawns a brand-new pi session, or with harness "claude" a claude session
// (`model`: a name `--model` takes — one of claude's aliases or a full name; optional `effort`: a claude level or "default")
// (type "prompt" or "ensure_session";
// a #219 `claudePreset` is dropped unread). Most calls immediately send the first command;
// type:"ensure_session" only creates the runtime so clients can query commands.
// Returns pi's real session id plus the model/thinking state selected at startup.
export async function POST(req: Request) {
  let commandType: string | undefined;
  let promptAccepted = false;
  try {
    const body = await req.json() as { cwd?: string; [key: string]: unknown };
    const { cwd, harness, ...command } = body;
    // The retired #219 fields: a supplied preset or alias is dropped, never read.
    delete command.claudePreset;
    delete command.modelAlias;
    commandType = typeof command.type === "string" ? command.type : undefined;
    if (harness !== undefined && !isSessionHarness(harness)) {
      return rejected(commandType, { error: `Unknown harness: ${String(harness)}` }, 400);
    }
    const claude = isClaudeHarness(harness);
    // A claude session's model is a name `--model` takes (an alias or a full
    // name, no fixed list); pi's command keeps its own fields.
    const claudeModel = claude ? command.model : undefined;
    const claudeEffort = claude ? (command.effort ?? CLAUDE_EFFORT_DEFAULT) : undefined;
    if (claude) {
      delete command.model;
      delete command.effort;
      if (!isClaudeModelName(claudeModel)) {
        return rejected(commandType, { error: `A claude session needs a model: ${String(claudeModel)}` }, 400);
      }
      if (!isClaudeEffort(claudeEffort)) {
        return rejected(commandType, { error: `Unknown claude effort: ${String(claudeEffort)}` }, 400);
      }
      if (commandType !== "prompt" && commandType !== "ensure_session") {
        return rejected(commandType, { error: `${String(commandType)} is not available for claude sessions` }, 400);
      }
      // Images are checked before a record is created or a child starts.
      const imageError = commandType === "prompt" ? validateClaudeImages(command.images) : null;
      if (imageError) return rejected(commandType, { error: imageError }, 400);
    }

    if (!cwd || typeof cwd !== "string") {
      return NextResponse.json({
        error: "cwd is required",
        ...(commandType === "prompt"
          ? { code: "prompt_rejected", accepted: false }
          : {}),
      }, { status: 400 });
    }
    if (!existsSync(cwd)) {
      return NextResponse.json({
        ...cwdMissingBody(cwd),
        ...(commandType === "prompt"
          ? { code: "prompt_rejected", accepted: false }
          : {}),
      }, { status: 400 });
    }

    // Guard the workspace BEFORE any session creation or file-root promotion:
    // the cwd's realpath must lie within an allowed registration prefix
    // (operator homedir, PI_WEB_ALLOWED_ROOT_PREFIXES, or an already-
    // registered root). A client must not be able to start the agent in an
    // arbitrary host directory such as / or /etc. Every downstream consumer
    // (session creation AND root registration) uses the guard's CANONICAL
    // path — the client-supplied alias must never be registered, or a
    // later retargeted symlink inside home would smuggle an outside
    // directory into the trusted prefixes (review r1 P1, pi#55).
    const registrable = isRegistrableRoot(cwd);
    if (!registrable.ok) {
      return NextResponse.json({
        error: "Workspace path is not registrable as a file root",
        reason: registrable.reason,
        ...(commandType === "prompt"
          ? { code: "prompt_rejected", accepted: false }
          : {}),
      }, { status: 403 });
    }

    if (claude) {
      return await createClaudeSession(
        registrable.path,
        claudeModel as string,
        claudeEffort as ClaudeEffort,
        command,
        commandType,
      );
    }

    // Use a one-time key so startRpcSession's lock doesn't conflict with real session ids
    const { provider, modelId, toolNames, thinkingLevel, ...promptCommand } = command as { provider?: string; modelId?: string; toolNames?: string[]; thinkingLevel?: unknown; [key: string]: unknown };
    if ((provider && !modelId) || (!provider && modelId)) {
      throw new Error("provider and modelId must be provided together");
    }
    const explicitThinkingLevel = parseThinkingLevel(thinkingLevel);

    // Must be unique per request: startRpcSession coalesces concurrent callers
    // that share a key onto one session. Date.now() (ms resolution) collides for
    // requests in the same millisecond, merging two new sessions into one.
    const tempKey = `__new__${randomUUID()}`;
    const { session, realSessionId } = await startRpcSession(tempKey, "", registrable.path, {
      ...(toolNames ? { toolNames } : {}),
      ...(provider && modelId ? { initialModel: { provider, modelId } } : {}),
      ...(explicitThinkingLevel ? { thinkingLevel: explicitThinkingLevel } : {}),
    });

    // Keep the files-route allowed-roots cache (see app/api/files/[...path]/route.ts)
    // in sync so the new cwd is immediately readable via /api/files. Without this,
    // a file request under a brand-new cwd would 403 for up to the cache TTL.
    allowFileRoot(registrable.path);
    invalidateSessionListCache();

    const state = await session.send({ type: "get_state" }) as {
      model?: { id: string; provider: string };
      thinkingLevel?: string;
    };

    if (promptCommand.type === "ensure_session") {
      return NextResponse.json({
        success: true,
        sessionId: realSessionId,
        data: null,
        model: state.model
          ? { provider: state.model.provider, modelId: state.model.id }
          : null,
        thinkingLevel: state.thinkingLevel,
      });
    }

    const result = await session.send(promptCommand);
    promptAccepted = promptCommand.type === "prompt";

    return NextResponse.json({
      success: true,
      sessionId: realSessionId,
      data: result,
      model: state.model
        ? { provider: state.model.provider, modelId: state.model.id }
        : null,
      thinkingLevel: state.thinkingLevel,
    });
  } catch (error) {
    return NextResponse.json({
      error: error instanceof Error ? error.message : String(error),
      ...(commandType === "prompt" && !promptAccepted
        ? { code: "prompt_rejected", accepted: false }
        : {}),
    }, { status: 500 });
  }
}
