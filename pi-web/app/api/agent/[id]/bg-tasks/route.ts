import { NextResponse } from "next/server";
import { getBgRuntime } from "@/lib/rpc-manager";

export const dynamic = "force-dynamic";

// GET /api/agent/[id]/bg-tasks — the session's built-in background tasks from the
// executor's registry (polyforge#173, AC2): every record the session owns, live
// and settled, the ones a restart left behind included. The URL's session id
// scopes the read by construction — another session's tasks never appear, with
// or without a live wrapper. This and the logs/kill routes are the old
// `pi-background-tasks` bridge's paths, kept as the built-in interface's shell;
// the v1 bridge and its package probe are gone.
export async function GET(
  _req: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  return NextResponse.json({ tasks: getBgRuntime().listAll(id) });
}
