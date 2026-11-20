import { NextResponse } from "next/server";
import { getBgRuntime } from "@/lib/rpc-manager";
import { BG_OUTPUT_MAX_BYTES } from "@/lib/bg-protocol";

export const dynamic = "force-dynamic";

// GET /api/agent/[id]/bg-tasks/[taskId]/logs — the bounded tail of one task's
// log from the executor's registry (polyforge#173, AC2). The whole answer stays
// within 8 KiB whatever the client asks; the session's own id scopes the read,
// so another session's task id reads as not found. The answer is a tail and
// names it: `truncated` says the excerpt starts mid-log, `tail` is always true.
export async function GET(
  req: Request,
  { params }: { params: Promise<{ id: string; taskId: string }> },
) {
  const { id, taskId } = await params;
  const url = new URL(req.url);
  const maxBytesParam = url.searchParams.get("maxBytes");
  const maxBytes = maxBytesParam === null ? undefined : Number(maxBytesParam);
  // The route owns the cap: the client's ask is clamped to the 8 KiB the
  // interface promises (AC2), never passed through larger.
  const budget = typeof maxBytes === "number" && Number.isFinite(maxBytes) && maxBytes > 0
    ? Math.min(Math.floor(maxBytes), BG_OUTPUT_MAX_BYTES)
    : BG_OUTPUT_MAX_BYTES;
  try {
    const output = getBgRuntime().readOutput(id, taskId, budget);
    return NextResponse.json(output);
  } catch (error) {
    return NextResponse.json(
      { error: error instanceof Error ? error.message : String(error) },
      { status: 404 },
    );
  }
}
