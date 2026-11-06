import { readFileSync } from "fs";
import { NextResponse } from "next/server";
import type { AgentLanguageResponse } from "@/lib/api-types";
import { isAgentLanguage } from "@/lib/agent-language";
import { agentLanguageConfigPath, readStoredAgentLanguage, writeStoredAgentLanguage } from "@/lib/agent-language-config";
import { ConfigWriteError, readPolyforgeAgentLanguage, syncAgentLanguageToPolyforge } from "@/lib/polyforge-config-write";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

// GET/PUT /api/agent-language: the server-side Agent language that
// new pi and claude sessions capture. The only writer of `pi-web-settings.json`.
// a PUT first sets pf's `[daemon] agent_language` (the default
// Cell's step agents) through `pf config set` when the installed pf has the key;
// pf's refusal is answered and nothing is written locally. The local file is
// checked first, so a file the local write would refuse refuses the save before
// pf is touched (the two never diverge by a save).
// a GET answers pf's actual `[daemon] agent_language` as
// `polyforge` (null for no pf or a pf without the key). the GET
// only reads it — one `pf --profile default --json config list`, never a write;
// the backfill of an unset key runs at Pi Web's start only
// (`instrumentation-node.ts`).
// Reached through the proxy's `/api/*` authentication (PI_WEB_PASSWORD when set);
// the PUT also refuses an untrusted (cross-site / foreign host) request itself.

function failure(status: number, error: string) {
  return NextResponse.json({ error }, { status });
}

/** The refusal `writeStoredAgentLanguage` would raise for the current file (it
 *  exists but cannot be read, is not JSON, or is not a JSON object), or null. */
function localFileRefusal(): string | null {
  const path = agentLanguageConfigPath();
  let text: string;
  try {
    text = readFileSync(path, "utf8");
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
    return error instanceof Error ? error.message : String(error);
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return `${path} is not valid JSON; refusing to overwrite it`;
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return `${path} is not a JSON object; refusing to overwrite it`;
  }
  return null;
}

export async function GET() {
  const language = readStoredAgentLanguage();
  const polyforge = await readPolyforgeAgentLanguage();
  return NextResponse.json({ language, polyforge } satisfies AgentLanguageResponse);
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
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    return failure(400, "Expected a JSON object");
  }
  const keys = Object.keys(body);
  const language = (body as { language?: unknown }).language;
  if (keys.length !== 1 || keys[0] !== "language" || !isAgentLanguage(language)) {
    return failure(400, "Expected { language: \"en\" | \"zh-CN\" }");
  }
  const refusal = localFileRefusal();
  if (refusal) return failure(500, refusal);
  try {
    await syncAgentLanguageToPolyforge(language);
  } catch (error) {
    if (error instanceof ConfigWriteError) return failure(error.status, error.message);
    return failure(500, error instanceof Error ? error.message : String(error));
  }
  try {
    return NextResponse.json({ language: await writeStoredAgentLanguage(language) } satisfies AgentLanguageResponse);
  } catch (error) {
    return failure(500, error instanceof Error ? error.message : String(error));
  }
}
