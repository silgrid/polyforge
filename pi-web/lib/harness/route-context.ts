// The three harness routes' shared request context: the ?cwd= validation,
// the same file-access check the mcp routes use (an absolute path inside the
// allowed roots, and a directory). Nothing here writes.
import { statSync } from "node:fs";
import { isAbsolute, resolve } from "node:path";
import { getAllowedFileRoots, isExistingFilePathAllowed } from "../file-access";
import type { HarnessReadContext } from "./adapter";

/** A harness route's refusal: the status and the JSON body's error code. */
export interface HarnessRouteRefusal {
  status: number;
  code: string;
  message: string;
}

/** The ?cwd= of a harness route, as the adapters' read context. */
export async function harnessRouteContext(url: string): Promise<{ ctx: HarnessReadContext } | HarnessRouteRefusal> {
  const value = new URL(url).searchParams.get("cwd");
  if (value === null) return { ctx: {} };
  if (!value.trim() || !isAbsolute(value)) {
    return { status: 400, code: "cwd-invalid", message: "cwd must be an absolute path" };
  }
  const allowedRoots = await getAllowedFileRoots();
  if (!isExistingFilePathAllowed(value, allowedRoots)) {
    return { status: 403, code: "cwd-denied", message: "Access denied" };
  }
  const cwd = resolve(value);
  try {
    if (!statSync(cwd).isDirectory()) {
      return { status: 400, code: "cwd-not-directory", message: "cwd must be a directory" };
    }
  } catch {
    return { status: 400, code: "cwd-not-directory", message: "cwd must be a directory" };
  }
  return { ctx: { cwd } };
}

/** The refusal as a JSON error body's shape. */
export function harnessRefusalBody(refusal: HarnessRouteRefusal): { error: string; message: string } {
  return { error: refusal.code, message: refusal.message };
}
