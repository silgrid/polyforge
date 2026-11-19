import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

// POST /api/skills/cleanup — the per-skill, user-confirmed legacy cleanup
// (polyforge#146, owner Q3 B). Skeleton: not implemented yet.
export async function POST(req: Request) {
  void req;
  return NextResponse.json({ error: "not implemented" }, { status: 500 });
}
