import { NextResponse } from "next/server";
import { lstatSync, realpathSync, renameSync, rmSync } from "fs";
import { isApiRequestAllowed } from "@/lib/request-security";
import { listLiveSessionCwds } from "@/lib/rpc-manager";
import {
  resolveFsManageFs,
  resolveFsManageMutator,
  runFsManageGuards,
  setFsManageSessionCwdSource,
  type FsManageFs,
  type FsManageMutator,
} from "@/lib/fs-manage-guards";

/**
 * POST /api/fs-manage — guarded browse-area directory rename/delete
 * (wi pi#59).
 *
 * Body: { action: "rename" | "delete", path, nextPath?, confirm? }.
 *
 * EVERY typed outcome answers HTTP 200 with exactly
 *   { ok: true, path }  |  { ok: false, reason }
 * — success, refusal and invalidBody alike. No refusal ever mutates the
 * filesystem (all guards run before the mutation), and no response ever
 * carries raw exception prose or paths: any mutation error maps to the
 * typed `ioFailure` code alone.
 */

// The route wires the REAL seams; the guard engine stays pure and injectable.
const REAL_FS_SEAMS: FsManageFs = { lstatSync, realpathSync };
const REAL_MUTATOR: FsManageMutator = {
  // Recursive delete; force:false so nothing is silently skipped, and fs.rm
  // throws on the first failure — a partial recursive delete therefore
  // reports ioFailure honestly, never success.
  removeDirectory: (path) => rmSync(path, { recursive: true, force: false }),
  renameDirectory: (source, destination) => renameSync(source, destination),
};
setFsManageSessionCwdSource(listLiveSessionCwds);

function typedResponse(body: { ok: true; path: string } | { ok: false; reason: string }): NextResponse {
  return NextResponse.json(body, { status: 200 });
}

export async function POST(request: Request) {
  // Standard trust gate, exactly like every other POST route.
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return typedResponse({ ok: false, reason: "invalidBody" });
  }

  // Every guard, in the fixed order, before any mutation.
  const guards = runFsManageGuards(body, resolveFsManageFs(REAL_FS_SEAMS));
  if (!guards.ok) {
    return typedResponse({ ok: false, reason: guards.reason });
  }

  try {
    const mutator = resolveFsManageMutator(REAL_MUTATOR);
    if (guards.action === "delete") mutator.removeDirectory(guards.source);
    else mutator.renameDirectory(guards.source, guards.path);
  } catch {
    // Typed code only — zero raw exception prose, zero paths.
    return typedResponse({ ok: false, reason: "ioFailure" });
  }

  // Rename answers with the destination path; delete with the resolved entry.
  return typedResponse({ ok: true, path: guards.path });
}
