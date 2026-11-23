import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

// polyforge#167 (91-onboarding): the state API's signatures, zero-value
// bodies. The unit's test commits on top of this and is red on assertions.
export async function GET(_request: Request) {
  return NextResponse.json({ imageManaged: false });
}

export async function POST(_request: Request) {
  return NextResponse.json({ imageManaged: false });
}
