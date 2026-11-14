import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

/** Stub (polyforge#25 u8): the override face lands with this unit. */
export async function GET() {
  return NextResponse.json({ status: null, error: "not implemented" }, { status: 501 });
}

export async function POST() {
  return NextResponse.json({ ok: false, error: "not implemented" }, { status: 501 });
}
