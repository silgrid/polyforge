import { NextResponse } from "next/server";
import { getClaudeSessionRecord } from "@/lib/claude-index";
import { readClaudePermissionConfig, writeClaudePermissionConfig, type ClaudePermissionConfig } from "@/lib/claude-permission-config";
import { ClaudeSessionBusyError, setClaudePermissionOverride } from "@/lib/claude-session";
import { isClaudePermissionMode, type ClaudePermissionMode } from "@/lib/claude-shared";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

// GET/PUT /api/claude-permissions: the claude permission settings.
// GET ?session=<id> → { mode, push, session: { override } | null } — the global default
//   mode ("ask" | "deny"), the pending-permission push switch, and that claude session's
//   override (null: follows the default); session is null with no id or a non-claude id.
// PUT { mode?, push? } → { mode, push }: stores the global settings (`claude-permissions.json`);
//   a running child keeps its mode until its next start.
// PUT { session, override: "ask" | "deny" | null } → { override, restarted }: a session's
//   override in the claude index; an idle live child is ended so the next start carries it.
//   A non-claude id or a bad value is 400; a busy child (turn, pending prompt, unsettled
//   background subagent) is 409 and nothing changes.
// The body is checked whole before anything is written. Reached through the proxy's `/api/*`
// authentication (PI_WEB_PASSWORD when set) like every other route; no aihub, no step agent.
// The PUT also refuses an untrusted (cross-site / foreign host) request itself.

function failure(status: number, error: string) {
  return NextResponse.json({ error }, { status });
}

export async function GET(req: Request) {
  const { mode, push } = readClaudePermissionConfig();
  const id = new URL(req.url).searchParams.get("session");
  const record = id ? getClaudeSessionRecord(id) : undefined;
  return NextResponse.json({ mode, push, session: record ? { override: record.permissionMode ?? null } : null });
}

const BODY_SHAPE = "Expected { mode?: \"ask\" | \"deny\", push?: boolean } or { session: <claude session id>, override: \"ask\" | \"deny\" | null }";

export async function PUT(req: Request) {
  if (!isApiRequestAllowed(req)) return failure(403, "Untrusted API request");
  if (!hasJsonContentType(req)) return failure(415, "Content-Type must be application/json");

  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return failure(400, "Invalid JSON body");
  }
  if (typeof body !== "object" || body === null || Array.isArray(body)) return failure(400, "Expected a JSON object");
  const fields = body as Record<string, unknown>;
  const keys = Object.keys(fields);

  if (keys.includes("session")) {
    if (keys.length !== 2 || !keys.includes("override")) return failure(400, BODY_SHAPE);
    const { session, override } = fields;
    if (typeof session !== "string" || !session) return failure(400, BODY_SHAPE);
    if (override !== null && !isClaudePermissionMode(override)) return failure(400, BODY_SHAPE);
    if (!getClaudeSessionRecord(session)) return failure(400, `Not a claude session: ${session}`);
    try {
      return NextResponse.json(await setClaudePermissionOverride(session, override as ClaudePermissionMode | null));
    } catch (error) {
      if (error instanceof ClaudeSessionBusyError) return failure(409, error.message);
      // The record vanished between the check and the write.
      if (error instanceof Error && error.message === "Session not found") return failure(400, `Not a claude session: ${session}`);
      return failure(500, error instanceof Error ? error.message : String(error));
    }
  }

  if (keys.length === 0 || keys.some((key) => key !== "mode" && key !== "push")) return failure(400, BODY_SHAPE);
  const patch: Partial<ClaudePermissionConfig> = {};
  if ("mode" in fields) {
    if (!isClaudePermissionMode(fields.mode)) return failure(400, BODY_SHAPE);
    patch.mode = fields.mode;
  }
  if ("push" in fields) {
    if (typeof fields.push !== "boolean") return failure(400, BODY_SHAPE);
    patch.push = fields.push;
  }
  try {
    const { mode, push } = await writeClaudePermissionConfig(patch);
    return NextResponse.json({ mode, push });
  } catch (error) {
    return failure(500, error instanceof Error ? error.message : String(error));
  }
}
