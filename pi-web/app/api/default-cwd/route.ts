import { NextResponse } from "next/server";
import { mkdirSync } from "fs";
import { defaultCwdPath } from "@/lib/default-cwd";

// GET /api/default-cwd
// Reports today's default directory (~/pi-cwd-<YYYYMMDD>) WITHOUT creating
// it, so the sidebar can apply shortcut-visibility rules (pi#18: hide the
// "use default directory" button when pinned projects diverge from it)
// without side effects. Selection still goes through POST, which creates
// the directory and allow-lists it for the file browser.
export async function GET() {
  return NextResponse.json({ cwd: defaultCwdPath() });
}

// POST /api/default-cwd
// Creates ~/pi-cwd/<YYYYMMDD> (local date) if it doesn't exist and returns the path.
// The client then selects it through /api/cwd/validate like any other directory.
export async function POST() {
  try {
    const dir = defaultCwdPath();
    mkdirSync(dir, { recursive: true });
    return NextResponse.json({ cwd: dir });
  } catch (error) {
    return NextResponse.json({ error: String(error) }, { status: 500 });
  }
}
