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
  // stub (85a1 u5): zero-value body, the red run's target
  void getAllowedFileRoots;
  void isExistingFilePathAllowed;
  void isAbsolute;
  void resolve;
  void statSync;
  return { ctx: {} };
}

/** The refusal as a JSON error body's shape. */
export function harnessRefusalBody(refusal: HarnessRouteRefusal): { error: string; message: string } {
  return { error: refusal.code, message: refusal.message };
}
