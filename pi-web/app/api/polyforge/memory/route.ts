import { NextResponse } from "next/server";
import { resolvePolyforgeCellScope } from "@/lib/polyforge-cells";
import { isApiRequestAllowed } from "@/lib/request-security";
import {
  browsePolyforgeMemory,
  PolyforgeMemoryError,
  POLYFORGE_MEMORY_WRITER_FILTER_MINIMUM_PF_VERSION,
  readPolyforgeMemoryCapability,
  readPolyforgeMemoryProjects,
  searchPolyforgeMemory,
  validateMemoryBrowse,
  validateMemorySearch,
} from "@/lib/polyforge-memory";

export const dynamic = "force-dynamic";

/**
 * GET /api/polyforge/memory?cwd=… — the Memory tab's three faces on one GET
 * (D2, browse):
 *   - no `q`, no `project`: the SCOPE read (capability plus the cwd's
 *     workspace projects, no memory verb);
 *   - no `q`, a `project` (optional `type`, `tags`, `after`, `writer_kind`):
 *     one BROWSE page, exactly one `pf recall` with no query (20 rows,
 *     updated-desc), answered with `hits`, `total` and, while more remain,
 *     `next_cursor`;
 *   - `q` (and `project`, `type`, optional `tags`, optional `any=1`, optional
 *     `writer_kind`): the keyword SEARCH, exactly one 200-row `pf recall`.
 * Degradation-first: pf absent, below the 2.20.12 floor, an unmapped cwd, an
 * off-list project's config read and a failed spawn all answer 200 with
 * fields, never 5xx; the only non-2xx answers are the gate's 403, the
 * shape-validation/membership 400s and the writer-filter 400 (Q4: a legal
 * `writer_kind` on a pf below the filter's own 0.2.3 floor — run before any
 * config read or recall spawn), which run before any recall spawn.
 */
function failureText(error: unknown): string {
  return error instanceof PolyforgeMemoryError ? error.message : "polyforge is unavailable";
}

/** Q4 (polyforge#120): a legal writer-kind request on a pf below the
 *  filter's own floor answers 400 with the upgrade hint — never a recall
 *  spawn that would silently drop the filter, never a filtered-looking
 *  answer. */
function writerFilterRefusal(pfVersion: string | null): { error: string } {
  return { error: `the writer filter needs pf ${POLYFORGE_MEMORY_WRITER_FILTER_MINIMUM_PF_VERSION} or newer (found ${pfVersion ?? "an unparseable version"}) — drop the writer filter or upgrade pf` };
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
      // the pinned Cell scope: the Cell pf resolves for this cwd, named in
      // the tab and carried by every pf call of this request (polyforge#18)
      const cellScope = await resolvePolyforgeCellScope(cwd);
      const scope = await readPolyforgeMemoryProjects(cwd, cellScope.pin.prefix);
      return NextResponse.json({ ...capability, ...scope, hits: [], ...(cellScope.cell ? { cell: cellScope.cell } : {}) });
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
      writer_kind: url.searchParams.get("writer_kind") ?? undefined,
    });
    if (!browse.ok) {
      return NextResponse.json({ error: browse.error }, { status: 400 });
    }
    const capability = await readPolyforgeMemoryCapability();
    if (!capability.capable) {
      return NextResponse.json({ ...capability, mapped: false, projects: [], hits: [], total: 0 });
    }
    if (browse.value.writer_kind && !capability.writerFilterSupported) {
      return NextResponse.json(writerFilterRefusal(capability.pfVersion), { status: 400 });
    }
    // the pinned Cell scope of this browse (polyforge#18): every call of
    // the request names the Cell pf resolves for the cwd
    const pin = (await resolvePolyforgeCellScope(browse.value.cwd)).pin.prefix;
    let browseScope: { mapped: boolean; projects: string[] };
    try {
      browseScope = await readPolyforgeMemoryProjects(browse.value.cwd, pin);
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
      const page = await browsePolyforgeMemory(browse.value, pin);
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
    writer_kind: url.searchParams.get("writer_kind") ?? undefined,
  });
  if (!validated.ok) {
    return NextResponse.json({ error: validated.error }, { status: 400 });
  }
  const capability = await readPolyforgeMemoryCapability();
  if (!capability.capable) {
    return NextResponse.json({ ...capability, mapped: false, projects: [], hits: [] });
  }
  if (validated.value.writer_kind && !capability.writerFilterSupported) {
    return NextResponse.json(writerFilterRefusal(capability.pfVersion), { status: 400 });
  }
  // the pinned Cell scope of this search (polyforge#18): every call of
  // the request names the Cell pf resolves for the cwd
  const pin = (await resolvePolyforgeCellScope(validated.value.cwd)).pin.prefix;
  let scope: { mapped: boolean; projects: string[] };
  try {
    scope = await readPolyforgeMemoryProjects(validated.value.cwd, pin);
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
    const hits = await searchPolyforgeMemory(validated.value, pin);
    return NextResponse.json({ ...capability, mapped: true, projects: scope.projects, hits });
  } catch (error) {
    return NextResponse.json({ ...capability, mapped: true, projects: scope.projects, hits: [], error: failureText(error) });
  }
}
