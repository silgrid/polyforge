import { NextResponse } from "next/server";
import type { ClaudeSettingsResponse } from "@/lib/api-types";
import { readClaudePermissionConfig } from "@/lib/claude-permission-config";
import { readClaudeSettings, writeClaudeSettings } from "@/lib/claude-settings";
import {
  CLAUDE_CUSTOM_MODELS_MAX,
  CLAUDE_PLAN_UNAVAILABLE_REASON,
  claudePlanModeAllowed,
  isClaudeModelName,
  isClaudeLaunchPermissionMode,
} from "@/lib/claude-shared";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

// GET/PUT /api/claude-settings: Settings › Claude — the permission mode
// and hook switch a new claude session starts with, and the custom model names offered
// beside claude's aliases. The only writer of `pi-web-claude-settings.json`
// (lib/claude-settings.ts); a save reaches new sessions only. Plan is offered only while
//'s global claude tool permissions are ask ( r3, plan D11): GET
// answers it as `planAvailable`, the PUT refuses plan on deny.
// Reached through the proxy's `/api/*` authentication (PI_WEB_PASSWORD when set);
// the PUT also refuses an untrusted (cross-site / foreign host) request itself.

const BODY_KEYS = ["customModels", "includeHookEvents", "launchPermissionMode"];
const BODY_SHAPE = "Expected { launchPermissionMode: \"default\" | \"acceptEdits\" | \"plan\", customModels: string[], includeHookEvents: boolean }";

function failure(status: number, error: string) {
  return NextResponse.json({ error }, { status });
}

export async function GET() {
  const planAvailable = claudePlanModeAllowed(readClaudePermissionConfig().mode);
  return NextResponse.json({ ...readClaudeSettings(), planAvailable } satisfies ClaudeSettingsResponse & { planAvailable: boolean });
}

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
  if (Object.keys(fields).sort().join(",") !== BODY_KEYS.join(",")) return failure(400, BODY_SHAPE);
  const { launchPermissionMode, customModels, includeHookEvents } = fields;
  if (!isClaudeLaunchPermissionMode(launchPermissionMode) || typeof includeHookEvents !== "boolean" || !Array.isArray(customModels)) {
    return failure(400, BODY_SHAPE);
  }
  if (launchPermissionMode === "plan" && !claudePlanModeAllowed(readClaudePermissionConfig().mode)) return failure(400, CLAUDE_PLAN_UNAVAILABLE_REASON);
  const invalid = customModels.find((name) => !isClaudeModelName(name));
  if (invalid !== undefined) {
    return failure(400, `Not a claude model name: ${JSON.stringify(invalid)} (1–128 letters, digits and . _ : @ / [ ] -, not starting with -)`);
  }
  const names = [...new Set(customModels as string[])];
  if (names.length > CLAUDE_CUSTOM_MODELS_MAX) return failure(400, `At most ${CLAUDE_CUSTOM_MODELS_MAX} custom model names`);

  try {
    const saved = await writeClaudeSettings({ launchPermissionMode, customModels: names, includeHookEvents });
    return NextResponse.json(saved satisfies ClaudeSettingsResponse);
  } catch (error) {
    return failure(500, error instanceof Error ? error.message : String(error));
  }
}
