import { NextResponse } from "next/server";
import {
  applyClaudeCustomModelsMigration,
  claudeCustomModelsMigrationStep,
} from "@/lib/claude-migration";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

// POST /api/claude-models-migration — the models page's protected migration
// POST (85a3-3, owner ruling Q2: the customModels phase of the retired
// self-stored files runs when the Claude section of the models page is first
// entered — never on a GET, never at server start):
//   POST {}            → the phase as it stands now: { status: "done", changed,
//                        shadowed } when there is nothing left or the empty
//                        target just migrated; { status: "blocked" | "deferred",
//                        reason, shadowed } when the source is illegal or
//                        over-cap, the native shape is bad, or claude's version
//                        is unknown (nothing was written); or
//                        { status: "preview", native, missing, digest, shadowed } —
//                        the merge preview (the native rows win, only the
//                        missing names would be appended), written by nothing.
//   POST { confirm }    → apply the preview under its digest; the source or
//                        target having changed answers 409 { status: "conflict" }
//                        with the fresh preview, never a silent merge.
// The body is checked whole before anything runs; the writes go through the
// claude adapter's shared version gate and key floors, and after the target
// reads back only the old file's customModels key is removed. Behind the API
// guard like every /api route; JSON only; no GET handler exists — the page's
// reads stay side-effect-free.

function failure(status: number, error: string, message: string) {
  return NextResponse.json({ error, message }, { status });
}

export async function POST(req: Request) {
  if (!isApiRequestAllowed(req)) {
    return failure(403, "request-denied", "Untrusted API request");
  }
  if (!hasJsonContentType(req)) {
    return failure(415, "not-json", "Content-Type must be application/json");
  }
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return failure(400, "invalid-json", "Invalid JSON body");
  }
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    return failure(400, "invalid-body", "Expected a JSON object");
  }
  const keys = Object.keys(body as Record<string, unknown>);
  if (keys.length > 1 || (keys.length === 1 && keys[0] !== "confirm")) {
    return failure(400, "invalid-body", "The body is {} or { confirm }");
  }
  const confirm = (body as Record<string, unknown>).confirm;
  if (confirm !== undefined && typeof confirm !== "string") {
    return failure(400, "invalid-body", "confirm must be the preview's digest string");
  }
  // The zero-value skeleton (85a3-3 u3): the route's guards answer, the
  // phase itself is not wired yet.
  return failure(501, "not-implemented", "the migration POST is not implemented yet");
}
