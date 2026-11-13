import { NextResponse } from "next/server";
import { readPolyforgeResolvedCell } from "@/lib/polyforge-cells";
import { isApiRequestAllowed } from "@/lib/request-security";
import {
  browsePolyforgeMemory,
  PolyforgeMemoryError,
  readPolyforgeMemoryCapability,
  readPolyforgeMemoryProjects,
  searchPolyforgeMemory,
  validateMemoryBrowse,
  validateMemorySearch,
} from "@/lib/polyforge-memory";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/memory?cwd=… — the Memory tab's three faces on one GET
 * ( D2, browse):
 *   - no `q`, no `project`: the SCOPE read (capability plus the cwd's
 *     workspace projects, no memory verb);
 *   - no `q`, a `project` (optional `type`, `tags`, `after`): one BROWSE page,
 *     exactly one `pf recall` with no query (20 rows, updated-desc), answered
 *     with `hits`, `total` and, while more remain, `next_cursor`;
 *   - `q` (and `project`, `type`, optional `tags`, optional `any=1`): the
 *     keyword SEARCH, exactly one 200-row `pf recall`.
 * Degradation-first: pf absent, below the 2.20.12 floor, an unmapped cwd, an
 * off-list project's config read and a failed spawn all answer 200 with
 * fields, never 5xx; the only non-2xx answers are the gate's 403 and the
 * shape-validation/membership 400s, which run before any recall spawn.
 */
function failureText(error: unknown): string {
  return error instanceof PolyforgeMemoryError ? error.message : "polyforge is unavailable";
}

export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  const url = new URL(request.url);
  const cwd = url.searchParams.get("cwd");
  const q = url.searchParams.get("q");
  const project = url.searchParams.get("project");

  // The scope read: the dropdown's own source. It spawns no memory verb.
  if (q === null && project === null) {
    if (!cwd) {
      return NextResponse.json({ error: "cwd must be a directory path" }, { status: 400 });
    }
    const capability = await readPolyforgeMemoryCapability();
    if (!capability.capable) {
      return NextResponse.json({ ...capability, mapped: false, projects: [], hits: [] });
    }
    try {
      const scope = await readPolyforgeMemoryProjects(cwd);
      // the Cell the CLI resolves for this cwd, named in the tab
      const cell = await readPolyforgeResolvedCell(cwd);
      return NextResponse.json({ ...capability, ...scope, hits: [], ...(cell ? { cell } : {}) });
    } catch (error) {
      return NextResponse.json({ ...capability, mapped: false, projects: [], hits: [], error: failureText(error) });
    }
  }

  // The browse face. Shape validation first: a refusal answers 400 with no
  // pf process, the version probe included.
  if (q === null) {
    const browse = validateMemoryBrowse({
      cwd,
      project,
      type: url.searchParams.get("type") ?? undefined,
      tags: url.searchParams.get("tags") ?? undefined,
      after: url.searchParams.get("after") ?? undefined,
    });
    if (!browse.ok) {
      return NextResponse.json({ error: browse.error }, { status: 400 });
    }
    const capability = await readPolyforgeMemoryCapability();
    if (!capability.capable) {
      return NextResponse.json({ ...capability, mapped: false, projects: [], hits: [], total: 0 });
    }
    let browseScope: { mapped: boolean; projects: string[] };
    try {
      browseScope = await readPolyforgeMemoryProjects(browse.value.cwd);
    } catch (error) {
      return NextResponse.json({ ...capability, mapped: false, projects: [], hits: [], total: 0, error: failureText(error) });
    }
    if (!browseScope.mapped) {
      // An unmapped cwd lists nothing.
      return NextResponse.json({ ...capability, mapped: false, projects: [], hits: [], total: 0 });
    }
    // Membership against the same config read the dropdown came from: an
    // off-list project answers 400 with no recall spawn.
    if (!browseScope.projects.includes(browse.value.project)) {
      return NextResponse.json({ error: "project must be one of the workspace's projects" }, { status: 400 });
    }
    try {
      const page = await browsePolyforgeMemory(browse.value);
      return NextResponse.json({ ...capability, mapped: true, projects: browseScope.projects,
        hits: page.items, total: page.total, next_cursor: page.next_cursor });
    } catch (error) {
      return NextResponse.json({ ...capability, mapped: true, projects: browseScope.projects, hits: [], total: 0, error: failureText(error) });
    }
  }

  // The search face. Shape validation first (D4): a refusal answers 400 with
  // no pf process, the version probe included.
  const validated = validateMemorySearch({
    cwd,
    q,
    project,
    type: url.searchParams.get("type"),
    tags: url.searchParams.get("tags") ?? undefined,
    any: url.searchParams.get("any") ?? undefined,
  });
  if (!validated.ok) {
    return NextResponse.json({ error: validated.error }, { status: 400 });
  }
  const capability = await readPolyforgeMemoryCapability();
  if (!capability.capable) {
    return NextResponse.json({ ...capability, mapped: false, projects: [], hits: [] });
  }
  let scope: { mapped: boolean; projects: string[] };
  try {
    scope = await readPolyforgeMemoryProjects(validated.value.cwd);
  } catch (error) {
    return NextResponse.json({ ...capability, mapped: false, projects: [], hits: [], error: failureText(error) });
  }
  if (!scope.mapped) {
    // An unmapped cwd runs no search (Requirement 13).
    return NextResponse.json({ ...capability, mapped: false, projects: [], hits: [] });
  }
  // Membership ("one of the projects the panel offered", D4): the same config
  // read the dropdown came from. An off-list project answers 400 with no
  // recall spawn — the config read itself is the price of "offered".
  if (!scope.projects.includes(validated.value.project)) {
    return NextResponse.json({ error: "project must be one of the workspace's projects" }, { status: 400 });
  }
  try {
    const hits = await searchPolyforgeMemory(validated.value);
    return NextResponse.json({ ...capability, mapped: true, projects: scope.projects, hits });
  } catch (error) {
    return NextResponse.json({ ...capability, mapped: true, projects: scope.projects, hits: [], error: failureText(error) });
  }
}
