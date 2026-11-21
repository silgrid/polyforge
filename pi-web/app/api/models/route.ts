import { existsSync } from "fs";
import { stat } from "fs/promises";
import { resolve } from "path";
import {
  loadModelsWithCache,
  withSafeModelLoadFailure,
  type ModelsData,
} from "@/lib/models-cache";
import { loadModelsData } from "@/lib/models-load";
import { getAllowedFileRoots, isExistingFilePathAllowed } from "@/lib/file-access";
import { inferRemovedWorktree } from "@/lib/worktree";
import { cwdMissingBody } from "@/lib/missing-cwd";

export const dynamic = "force-dynamic";

const EMPTY_MODELS: ModelsData = {
  models: {},
  modelList: [],
  defaultModel: null,
  defaultThinkingLevel: null,
  savedDefaultThinkingLevel: null,
  thinkingLevels: {},
  thinkingLevelMaps: {},
  thinkingLevelPins: {},
  isScoped: false,
};

export async function GET(req: Request) {
  const url = new URL(req.url);
  const requestedCwd = resolve(url.searchParams.get("cwd") || process.cwd());
  const scopeAll = url.searchParams.get("scope") === "all";
  // A subagent run isolated in a worktree has that worktree removed when the run
  // finishes, yet its session file still records the worktree as its cwd. Fall back
  // to the main repo it branched from, so opening a finished subagent resolves the
  // same models a non-isolated one would. The roots check below runs on the folder
  // actually answered from, so the fallback reaches no project that was not allowed
  // by name already.
  const cwd = existsSync(requestedCwd)
    ? requestedCwd
    : (await inferRemovedWorktree(requestedCwd))?.projectRoot ?? requestedCwd;

  let cwdStat;
  try {
    cwdStat = await stat(cwd);
  } catch {
    // Typed: a moved project's session is not a model problem (#1422).
    return Response.json(cwdMissingBody(cwd), { status: 400 });
  }
  if (!cwdStat.isDirectory()) {
    return Response.json({ error: `Not a directory: ${cwd}` }, { status: 400 });
  }
  const allowedRoots = await getAllowedFileRoots();
  if (!isExistingFilePathAllowed(cwd, allowedRoots)) {
    return Response.json({ error: "Access denied" }, { status: 403 });
  }

  try {
    const cacheKey = scopeAll ? `${cwd}#all` : cwd;
    return Response.json(await loadModelsWithCache(cacheKey, () => loadModelsData(cwd, scopeAll)));
  } catch {
    return Response.json(withSafeModelLoadFailure(EMPTY_MODELS));
  }
}
