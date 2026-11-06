import { NextResponse } from "next/server";
import { getRpcSession } from "@/lib/rpc-manager";
import { resolveSessionPath } from "@/lib/session-reader";
import { getClaudeSessionRecord } from "@/lib/claude-index";
import { getClaudeSession } from "@/lib/claude-session";

export async function GET(
  _req: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  try {
    // A claude session: live while its child is; never 404 while the index has it.
    if (getClaudeSessionRecord(id)) {
      const claude = getClaudeSession(id);
      return NextResponse.json(claude ? { running: true, state: await claude.send({ type: "get_state" }) } : { running: false });
    }
    const rpc = getRpcSession(id);
    if (rpc?.isAlive()) {
      const state = await rpc.send({ type: "get_state" });
      return NextResponse.json({ running: true, state });
    }

    if (!await resolveSessionPath(id)) {
      return NextResponse.json({ error: "Session not found" }, { status: 404 });
    }
    return NextResponse.json({ running: false });
  } catch (error) {
    return NextResponse.json({ error: String(error) }, { status: 500 });
  }
}
