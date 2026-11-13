import { createAgentEventStream } from "@/lib/agent-event-stream";
import { resolveSessionPath } from "@/lib/session-reader";
import { getRpcSession, startRpcSession } from "@/lib/rpc-manager";
import { isValidLivenessHolder } from "@/lib/session-liveness";
import { describeStreamClient } from "@/lib/stream-client";
import { getClaudeSessionRecord } from "@/lib/claude-index";
import { startClaudeSession } from "@/lib/claude-session";

export const dynamic = "force-dynamic";

// GET /api/agent/[id]/events?holder=<page id> - SSE stream of agent events. The holder, the
// request's reported address and its browser label only label the stream for the debug panel: the
// page's lease renewals keep the session alive, never the stream (#1283).
export async function GET(
  req: Request,
  { params }: { params: Promise<{ id: string }> }
) {
  const { id } = await params;
  if (req.signal.aborted) return new Response(null, { status: 204 });
  const holderParam = new URL(req.url).searchParams.get("holder");
  const holder = isValidLivenessHolder(holderParam) ? holderParam : null;

  // A claude session's stream is its child's: opening it starts one (`--resume`) when none is alive.
  const claudeRecord = getClaudeSessionRecord(id);
  // Fast path: already-running session
  const session = claudeRecord ? undefined : getRpcSession(id);
  let sessionPromise;
  if (claudeRecord) {
    sessionPromise = Promise.resolve().then(() => startClaudeSession(claudeRecord));
  } else if (session?.isAlive()) {
    sessionPromise = Promise.resolve(session);
  } else {
    const filePath = await resolveSessionPath(id);
    if (!filePath) {
      return new Response("Session not found", { status: 404 });
    }
    if (req.signal.aborted) return new Response(null, { status: 204 });
    sessionPromise = startRpcSession(id, filePath, undefined).then((result) => result.session);
  }

  const stream = createAgentEventStream(req, id, sessionPromise, { holder, ...describeStreamClient(req.headers) });

  return new Response(stream, {
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache, no-transform",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    },
  });
}
