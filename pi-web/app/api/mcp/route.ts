import { join } from "node:path";
import { NextResponse } from "next/server";
import { getAgentDir, type McpExposure } from "@earendil-works/pi-coding-agent";
import type {
  ClaudeMcpFileInfo,
  McpActionItemResult,
  McpActionResponse,
  McpApplyResult,
  McpErrorResponse,
  McpRefusalReason,
  McpResponse,
  McpScope,
  McpServerRef,
  ProjectTrustStatus,
} from "@/lib/api-types";
import {
  insertMcpServer,
  isMcpConfigWriteError,
  removeMcpServer,
  setMcpServerEnabledInProject,
  setMcpServerExposure,
  setMcpServersEnabled,
  type McpConfigFileTarget,
  type McpEnabledOutcome,
} from "@/lib/mcp-config-file";
import {
  MCP_ADD_MAX_TEXT,
  prepareMcpAdd,
  readMcpAddSecretReferences,
  readMcpAddValues,
  type McpAddRefusal,
  type McpAddRequest,
} from "@/lib/mcp-add";
import { readMcpOverview, readMcpServerConfigs, readMcpServerEntry } from "@/lib/mcp-config-read";
import { readClaudeMcpRawEntry, readClaudeMcpServers, claudeMcpStatePath } from "@/lib/claude-mcp-read";
import { claudeMcpAddJson, claudeMcpRemove, claudeMcpWriteEntry, claudeMcpWriteGate } from "@/lib/claude-mcp-write";
import { claudeMcpEntryFromPi, piMcpEntryFromClaude, unifiedMcpRows } from "@/lib/mcp-unified";
import { findClaudeBinary } from "@/lib/claude-session";
import {
  isMcpEntryRefusal,
  mcpInternalsOrRefusal,
  mcpProjectTrustRefusal,
  readMcpServerConfig,
  validateMcpProject,
  type McpEntryRefusal,
} from "@/lib/mcp-entry-request";
import { MCP_EXPOSURES, isMcpExposure, suggestFreeName } from "@/lib/mcp-import";
import { mcpOAuthUrl, signOutMcpServer } from "@/lib/mcp-sign-in";
import { forgetMcpEntryStatuses } from "@/lib/mcp-status";
import { findWebPasswordField } from "@/lib/mcp-transport";
import { holdRemovedEntry, returnRemovedEntry, takeRemovedEntry } from "@/lib/mcp-undo";
import type { PiSdkInternals } from "@/lib/pi-sdk-internals";
import { invalidateModelsCache } from "@/lib/models-cache";
import { freshFolderTrustBreadth, getProjectTrustStatus, trustFreshFolderAndWrite } from "@/lib/project-trust";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";

export const dynamic = "force-dynamic";

// Settings › MCP (ADR 0006). GET reads files only: it never spawns a server,
// opens a connection, or runs a `!command` value, so it lists an untrusted
// project's servers as safely as the global ones. Without `cwd` it lists the
// global file alone; Settings › MCP does not need a project.
//
// POST changes one file through `lib/mcp-config-file.ts` (locked, atomic, the
// SDK editor's bytes) and answers with the overview GET would give, so the
// panel replaces its listing without a second request. Open sessions apply a
// change at their next message, when their MCP host reads the files again.
// `sign-out` changes `mcp-auth.json` instead, as `pi mcp logout` does. `add`
// parses pasted text again with the importer the panel previewed it with
// (`lib/mcp-add.ts`), and for a fresh folder trusts it in the same step.

type Project = { cwd: string; allowedRoots: Set<string> };

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

type RefusalParams = Omit<McpErrorResponse, "error" | "reason">;

function refusal(status: number, reason: McpRefusalReason, error: string, params: RefusalParams = {}) {
  return NextResponse.json({ error, reason, ...params } satisfies McpErrorResponse, { status });
}

/** A typed refusal: the whole answer of a request, or one server's result in a bulk switch. */
class Refusal {
  constructor(
    readonly status: number,
    readonly reason: McpRefusalReason,
    readonly error: string,
    readonly params: RefusalParams = {},
  ) {}

  response() {
    return refusal(this.status, this.reason, this.error, this.params);
  }

  item(server: McpServerRef): McpActionItemResult {
    return { ...server, error: this.error, reason: this.reason };
  }
}

/** The shared checks of `lib/mcp-entry-request.ts`, as one of this route's refusals. */
function asRefusal(refused: McpEntryRefusal): Refusal {
  const { error, reason, ...params } = refused.body;
  return new Refusal(refused.status, reason, error, params);
}

/** The project folder the request names, checked as Test, sign-in and the trust route check it (`validateMcpProject()`). */
async function validateProject(value: unknown): Promise<Project | Refusal> {
  const result = await validateMcpProject(value);
  return isMcpEntryRefusal(result) ? asRefusal(result) : result;
}

// GET /api/mcp?cwd=<absolute project folder>
export async function GET(req: Request) {
  const value = new URL(req.url).searchParams.get("cwd");
  let project: Project | undefined;
  if (value !== null) {
    const result = await validateProject(value);
    if (result instanceof Refusal) return result.response();
    project = result;
  }

  try {
    const overview = await readMcpOverview({ agentDir: getAgentDir(), project });
    const claude = claudeMcpSection(project?.cwd);
    return NextResponse.json({
      ...overview,
      claude,
      unified: unifiedMcpRows(overview.servers, claude.servers),
    } satisfies McpResponse);
  } catch (error) {
    return refusal(500, "internal", errorMessage(error));
  }
}

/**
 * Where a write to `scope` goes. A project file is written only while a
 * decision, exact or inherited, trusts the folder: the rule the MCP host reads
 * it by (`mayReadProjectConfigNow()`), never `trusted`, which a folder with no
 * trust-requiring resources gets with no decision at all.
 */
function writeTarget(scope: McpScope, project: Project | undefined, agentDir: string): McpConfigFileTarget | Refusal {
  if (scope === "global") return { scope: "global", agentDir };
  if (!project) return new Refusal(400, "invalid-request", "A project server needs the project's cwd");
  const refused = mcpProjectTrustRefusal(project.cwd, agentDir, "The project is not trusted, so its .pi/mcp.json is not changed");
  if (refused) return asRefusal(refused);
  return { scope: "project", cwd: project.cwd, allowedRoots: project.allowedRoots };
}

/** A writer refusal as the route answers it: 409 for a file state the user can fix, 500 for anything else. */
function writeRefusal(error: unknown): Refusal {
  if (isMcpConfigWriteError(error)) {
    const params = { path: error.path, ...(error.serverName !== undefined ? { name: error.serverName } : {}) };
    if (error.reason === "unreadable") return new Refusal(500, "internal", error.message, params);
    return new Refusal(409, error.reason, error.message, params);
  }
  return new Refusal(500, "internal", errorMessage(error));
}

/**
 * MCP off means the panel is read-only: the operator turned it off for this
 * server (nothing in the browser may override that, and the pi CLI reads the
 * same files), or the SDK's MCP modules cannot load, so nothing can be
 * checked. `-builtin:mcp` does not: it is a setting a project may reverse, and
 * whether a global switch worked must not depend on the folder Settings shows.
 */
async function mcpWritable(): Promise<PiSdkInternals | Refusal> {
  const internals = await mcpInternalsOrRefusal("changes no MCP server");
  return isMcpEntryRefusal(internals) ? asRefusal(internals) : internals;
}

/** The scope of a pi entry: pi never lists or writes a `local` one (#85 85c). */
function readScope(value: unknown): Exclude<McpScope, "local"> | undefined {
  return value === "global" || value === "project" ? value : undefined;
}

function readName(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 && value.length <= 1024 ? value : undefined;
}

const MAX_BULK_SERVERS = 500;

function readServerList(value: unknown): McpServerRef[] | undefined {
  if (!Array.isArray(value) || value.length === 0 || value.length > MAX_BULK_SERVERS) return undefined;
  const servers = new Map<string, McpServerRef>();
  for (const item of value) {
    const scope = isRecord(item) ? readScope(item.scope) : undefined;
    const name = isRecord(item) ? readName(item.name) : undefined;
    if (!scope || name === undefined) return undefined;
    servers.set(`${scope}\0${name}`, { scope, name });
  }
  return [...servers.values()];
}

/**
 * claude's half of the unified list (#85 85c, AC1): its files only — the
 * state file and the project's .mcp.json — plus a PATH lookup for `present`,
 * which says a write could reach a CLI. Nothing here spawns, exactly like
 * GET's pi half.
 */
function claudeMcpSection(cwd: string | undefined): ClaudeMcpFileInfo {
  const read = readClaudeMcpServers({ ...(cwd !== undefined ? { cwd } : {}), env: process.env });
  return {
    present: findClaudeBinary() !== null,
    configPath: read.configPath,
    stateReadable: read.stateReadable,
    servers: read.servers,
    disabledMcpServers: read.disabledMcpServers,
  };
}

/** The unified scopes of the MCP list (#85 85c): `local` is claude's own group. */
const UNIFIED_SCOPES: readonly McpScope[] = ["global", "project", "local"];

function readUnifiedScope(value: unknown): McpScope | undefined {
  return UNIFIED_SCOPES.includes(value as McpScope) ? (value as McpScope) : undefined;
}

/** The 「应用到」 targets of an add or a set-apply: default pi alone (the panel it grew from), never empty. */
function readHarnesses(value: unknown): ("pi" | "claude")[] | undefined {
  if (value === undefined || value === null) return ["pi"];
  if (!Array.isArray(value) || value.length === 0) return undefined;
  const out: ("pi" | "claude")[] = [];
  for (const item of value) {
    if (item !== "pi" && item !== "claude") return undefined;
    if (!out.includes(item)) out.push(item);
  }
  return out;
}

/** A claude-mcp-write outcome as this route answers it: a write the CLI refused or could not make. */
function claudeWriteRefusal(outcome: { ok: false; reason: string; error: string }): Refusal {
  if (outcome.reason === "invalid-request") return new Refusal(400, "invalid-request", outcome.error);
  return new Refusal(409, "claude-unwritable", outcome.error);
}

/** claude's write context: the panel's project folder and the env claude resolves its own files by. */
function claudeWriteContext(project: Project | undefined) {
  return { ...(project !== undefined ? { cwd: project.cwd } : {}), env: process.env };
}

/** The names claude defines in `scope` right now, for a name an add must not take. */
function claudeNamesInScope(scope: McpScope, cwd: string | undefined): string[] {
  const read = readClaudeMcpServers({ ...(cwd !== undefined ? { cwd } : {}), env: process.env });
  return read.servers.filter((server) => server.scope === scope).map((server) => server.name);
}

async function overviewResponse(agentDir: string, project: Project | undefined, extra: Omit<McpActionResponse, keyof McpResponse> = {}) {
  try {
    const overview = await readMcpOverview({ agentDir, project });
    const claude = claudeMcpSection(project?.cwd);
    return NextResponse.json({
      ...overview,
      claude,
      unified: unifiedMcpRows(overview.servers, claude.servers),
      ...extra,
    } satisfies McpActionResponse);
  } catch (error) {
    return refusal(500, "internal", errorMessage(error));
  }
}

/** Turning on an entry that references PI_WEB_PASSWORD is refused: Pi Web would not connect it, and the pi CLI would send the password. */
function webPasswordRefusal(internals: PiSdkInternals) {
  return (_name: string, entry: Record<string, unknown>) => (findWebPasswordField(entry, internals) ? "web-password" as const : undefined);
}

/** Why the writer left one server as it was, as a 409; undefined when it now says what was asked. */
function outcomeRefusal(outcome: McpEnabledOutcome<"web-password">, path: string): Refusal | undefined {
  const name = outcome.name;
  switch (outcome.outcome) {
    case "missing":
      return new Refusal(409, "server-missing", `${path} does not define MCP server "${name}"`, { path, name });
    case "not-an-object":
      return new Refusal(409, "entry-not-object", `${path} defines MCP server "${name}" as something other than an object, so it has nothing to switch`, { path, name });
    case "refused":
      return new Refusal(409, "web-password", `"${name}" references PI_WEB_PASSWORD, so Pi Web does not turn it on`, { name });
    default:
      return undefined;
  }
}

async function switchServer(
  agentDir: string,
  project: Project | undefined,
  internals: PiSdkInternals,
  server: McpServerRef,
  enabled: boolean,
) {
  const target = writeTarget(server.scope, project, agentDir);
  if (target instanceof Refusal) return target.response();
  try {
    const { value: [outcome], path } = await setMcpServersEnabled(
      target,
      [server.name],
      enabled,
      enabled ? webPasswordRefusal(internals) : undefined,
    );
    const refused = outcomeRefusal(outcome, path);
    if (refused) return refused.response();
  } catch (error) {
    return writeRefusal(error).response();
  }
  return overviewResponse(agentDir, project);
}

/**
 * How one server's tools reach the model. Nothing connects because of it:
 * open sessions register the changed entry again at their next message, so
 * an entry that references PI_WEB_PASSWORD may change too, and stays off.
 */
async function setServerExposure(
  agentDir: string,
  project: Project | undefined,
  server: McpServerRef,
  exposure: McpExposure,
) {
  const target = writeTarget(server.scope, project, agentDir);
  if (target instanceof Refusal) return target.response();
  try {
    const { value: outcome, path } = await setMcpServerExposure(target, server.name, exposure);
    const refused = outcomeRefusal(outcome, path);
    if (refused) return refused.response();
  } catch (error) {
    return writeRefusal(error).response();
  }
  return overviewResponse(agentDir, project);
}

/**
 * Turns a global server on or off in the panel's project alone, as `/mcp`'s
 * "Enable / Disable in this project" does: an override entry in the project's
 * `.pi/mcp.json` (pi 1.0.1), written under any project write's checks. The
 * global entry must be one pi loads, since an override of anything else is
 * skipped; turning on one that references PI_WEB_PASSWORD is refused, as its
 * switch would be. Remove on the override's row goes back to the global value.
 */
async function switchServerInProject(
  agentDir: string,
  project: Project | undefined,
  internals: PiSdkInternals,
  name: string,
  enabled: boolean,
) {
  const target = writeTarget("project", project, agentDir);
  if (target instanceof Refusal) return target.response();
  if (target.scope !== "project") return refusal(500, "internal", "No project file to write to");
  const read = readMcpServerEntry({ agentDir, scope: "global", name });
  if (!read.ok) {
    const params = { path: read.path, ...(read.reason === "server-missing" ? { name } : {}) };
    if (read.reason === "unreadable") return refusal(500, "internal", read.error, params);
    return refusal(409, read.reason, read.error, params);
  }
  const global = readMcpServerConfig({ agentDir, internals, scope: "global", name });
  if (typeof global === "string") return refusal(409, "server-invalid", global, { name });
  if (enabled && findWebPasswordField(global.config, internals)) {
    return refusal(409, "web-password", `"${name}" references PI_WEB_PASSWORD, so Pi Web does not turn it on`, { name });
  }
  try {
    const { value: outcome, path } = await setMcpServerEnabledInProject(target, name, enabled);
    const refused = outcomeRefusal(outcome, path);
    if (refused) return refused.response();
  } catch (error) {
    return writeRefusal(error).response();
  }
  return overviewResponse(agentDir, project);
}

/**
 * The group switch: every server asked for, one write per file, and a result
 * per server, so one the route refuses (removed meanwhile, not an object,
 * referencing PI_WEB_PASSWORD, in an untrusted project, in a file that no
 * longer parses) keeps the rest from failing.
 */
async function switchServers(
  agentDir: string,
  project: Project | undefined,
  internals: PiSdkInternals,
  servers: McpServerRef[],
  enabled: boolean,
) {
  const results = new Map<string, McpActionItemResult>();
  const key = (server: McpServerRef) => `${server.scope}\0${server.name}`;
  for (const scope of ["global", "project"] as const) {
    const group = servers.filter((server) => server.scope === scope);
    if (group.length === 0) continue;
    const target = writeTarget(scope, project, agentDir);
    if (target instanceof Refusal) {
      for (const server of group) results.set(key(server), target.item(server));
      continue;
    }
    try {
      const { value: outcomes, path } = await setMcpServersEnabled(
        target,
        group.map((server) => server.name),
        enabled,
        enabled ? webPasswordRefusal(internals) : undefined,
      );
      for (const outcome of outcomes) {
        const server = { scope, name: outcome.name };
        results.set(key(server), outcomeRefusal(outcome, path)?.item(server) ?? server);
      }
    } catch (error) {
      const failure = writeRefusal(error);
      for (const server of group) results.set(key(server), failure.item(server));
    }
  }
  return overviewResponse(agentDir, project, { results: servers.map((server) => results.get(key(server)) ?? server) });
}

async function removeServer(agentDir: string, project: Project | undefined, server: McpServerRef) {
  const target = writeTarget(server.scope, project, agentDir);
  if (target instanceof Refusal) return target.response();
  let removed: Awaited<ReturnType<typeof removeMcpServer>>;
  try {
    removed = await removeMcpServer(target, server.name);
  } catch (error) {
    return writeRefusal(error).response();
  }
  const { token, expiresAt } = holdRemovedEntry({
    ...server,
    path: removed.path,
    ...(target.scope === "project" ? { cwd: target.cwd } : {}),
    entry: removed.value.entry,
    index: removed.value.index,
  });
  return overviewResponse(agentDir, project, {
    undo: { ...server, token, path: removed.path, expiresInMs: Math.max(0, expiresAt - Date.now()) },
  });
}

/**
 * Puts a removed entry back where it stood, under the same checks as any
 * write: a project entry's folder must still be allowed and trusted. The
 * entry is restored as it was, a PI_WEB_PASSWORD reference included, since
 * undo returns the file to what it held a moment ago; the panel shows such an
 * entry as refused. A name the file defines again since is never replaced.
 */
async function undoRemoval(agentDir: string, project: Project | undefined, token: string) {
  const held = takeRemovedEntry(token);
  if (!held) return refusal(410, "undo-unavailable", "Nothing to undo: the removal is unknown, already undone, or older than 60 seconds");
  const server = { scope: held.scope, name: held.name };
  // A claude removal comes back through claude's own CLI (AC2), under the
  // same version gate as the write that removed it.
  if (held.harness === "claude") {
    if (held.scope !== "global" && held.cwd === undefined) {
      returnRemovedEntry(held);
      return refusal(410, "undo-unavailable", "The project folder this claude entry lived in is gone, so the removal is not undone");
    }
    const outcome = await claudeMcpAddJson(held.scope, held.name, held.entry as Record<string, unknown>, {
      ...(held.cwd !== undefined ? { cwd: held.cwd } : {}),
      env: process.env,
    });
    if (!outcome.ok) {
      returnRemovedEntry(held);
      return claudeWriteRefusal(outcome).response();
    }
    return overviewResponse(agentDir, project, { restored: server });
  }
  let failure: Refusal | undefined;
  try {
    let heldProject: Project | undefined;
    if (held.scope === "project") {
      const result = await validateProject(held.cwd);
      if (result instanceof Refusal) failure = result;
      else heldProject = result;
    }
    if (!failure) {
      const target = writeTarget(held.scope, heldProject, agentDir);
      if (target instanceof Refusal) failure = target;
      else await insertMcpServer(target, held.name, held.entry, held.index);
    }
  } catch (error) {
    failure = isMcpConfigWriteError(error) && error.reason === "name-taken"
      ? new Refusal(409, "undo-name-taken", `${error.path} defines "${held.name}" again, so the removal is not undone`, {
          path: error.path,
          name: held.name,
        })
      : writeRefusal(error);
  }
  if (failure) {
    // Still undoable once the cause is fixed, for the time it has left.
    returnRemovedEntry(held);
    return failure.response();
  }
  return overviewResponse(agentDir, project, { restored: server });
}

/**
 * Signs out of a server as `pi mcp logout` does: deletes its URL's tokens and
 * client registration from `mcp-auth.json` (`lib/mcp-sign-in.ts`), after
 * barring every sign-in run of the URL from writing there again and
 * cancelling the one under way. The URL is read from the entry's file, never
 * taken from the browser, under the checks of any change to that entry: a
 * project entry is a repository's, so its folder must be trusted. What
 * connections found before is forgotten, since signed-out ones no longer see
 * it; open sessions lose access at their next request to the server.
 */
async function signOutServer(agentDir: string, project: Project | undefined, internals: PiSdkInternals, server: McpServerRef & { scope: Exclude<McpScope, "local"> }) {
  const target = writeTarget(server.scope, project, agentDir);
  if (target instanceof Refusal) return target.response();
  const { name } = server;
  const read = readMcpServerEntry({ agentDir, scope: server.scope, name, project });
  if (!read.ok) {
    const params = { path: read.path, ...(read.reason === "server-missing" ? { name } : {}) };
    if (read.reason === "unreadable") return refusal(500, "internal", read.error, params);
    return refusal(409, read.reason, read.error, params);
  }
  if (!isRecord(read.value)) {
    return refusal(409, "entry-not-object", `${read.sourcePath} defines MCP server "${name}" as something other than an object`, { name });
  }
  // A project override signs in and out as the global server it changes: same name, same URL.
  const resolved = readMcpServerConfig({ agentDir, project, internals, scope: server.scope, name });
  if (typeof resolved === "string") return refusal(409, "server-invalid", resolved, { name });
  const url = mcpOAuthUrl(resolved.config);
  if (url === undefined) {
    return refusal(409, "sign-in-not-oauth", `MCP server "${name}" does not use OAuth: only an HTTP server without an Authorization header does`, { name });
  }
  const removed = signOutMcpServer(name, url, agentDir, internals);
  forgetMcpEntryStatuses({ scope: server.scope, sourcePath: read.sourcePath, name });
  return overviewResponse(agentDir, project, { signedOut: { ...server, removed } });
}

/** The names a file defines now, for a name the add must not take; empty when the file cannot be listed (the write then says why). */
function namesInFile(agentDir: string, project: Project | undefined, scope: McpScope, internals: PiSdkInternals): string[] {
  try {
    const { servers } = readMcpServerConfigs({ agentDir, project: scope === "project" ? project : undefined, internals });
    return servers.filter((server) => server.scope === scope).map((server) => server.name);
  } catch {
    return [];
  }
}

function addRefusal({ status, reason, error, name, suggestedName, notes, names, fields }: McpAddRefusal): Refusal {
  return new Refusal(status, reason, error, {
    ...(name !== undefined ? { name } : {}),
    ...(suggestedName !== undefined ? { suggestedName } : {}),
    ...(notes ? { notes } : {}),
    ...(names ? { names } : {}),
    ...(fields ? { fields } : {}),
  });
}

/** A failed insert as the add answers it: a name taken meanwhile gets a free one to suggest. */
function insertRefusal(error: unknown, agentDir: string, project: Project | undefined, scope: McpScope, internals: PiSdkInternals): Refusal {
  if (isMcpConfigWriteError(error) && error.reason === "name-taken" && error.serverName !== undefined) {
    const suggestedName = suggestFreeName(error.serverName, namesInFile(agentDir, project, scope, internals));
    return new Refusal(409, "name-taken", error.message, { path: error.path, name: error.serverName, suggestedName });
  }
  return writeRefusal(error);
}

function readAddRequest(body: Record<string, unknown>): McpAddRequest | Refusal {
  const scope = readUnifiedScope(body.scope);
  const harnesses = readHarnesses(body.harnesses);
  const values = readMcpAddValues(body.values);
  const secretReferences = readMcpAddSecretReferences(body.secretReferences);
  const server = body.server === undefined ? 0 : body.server;
  const confirm = body.confirmHostEnv === undefined ? [] : body.confirmHostEnv;
  if (
    typeof body.text !== "string" || body.text.length > MCP_ADD_MAX_TEXT || !scope || !values || !secretReferences
    || !harnesses
    || typeof server !== "number" || !Number.isInteger(server) || server < 0
    || (body.name !== undefined && readName(body.name) === undefined)
    || (body.rawPi !== undefined && typeof body.rawPi !== "boolean")
    || (body.trustFolder !== undefined && typeof body.trustFolder !== "boolean")
    || !Array.isArray(confirm) || confirm.length > 100 || !confirm.every((name) => typeof name === "string")
  ) {
    return new Refusal(400, "invalid-request", `add needs text (at most ${MCP_ADD_MAX_TEXT} characters), scope, and optionally values, secretReferences, server, name, rawPi, trustFolder and confirmHostEnv`);
  }
  return {
    text: body.text,
    values,
    secretReferences,
    server,
    ...(typeof body.name === "string" ? { name: body.name } : {}),
    scope,
    harnesses,
    rawPi: body.rawPi === true,
    confirmHostEnv: confirm as string[],
  };
}

/** The folder's trust for the page after a project write: `.pi/mcp.json` alone makes it require trust. */
function trustAfterWrite(cwd: string, agentDir: string): ProjectTrustStatus | undefined {
  try {
    const status = getProjectTrustStatus(cwd, agentDir);
    return status.decisionError === undefined ? status : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Adds a pasted server: `prepareMcpAdd()` parses the text again, fills in the
 * values and checks the result; then it is appended to its file through the
 * writer, under the file's lock, which refuses a name taken meanwhile. A
 * project server goes only to a folder a decision trusts (the rule of every
 * project write), except with `trustFolder` for a fresh folder, which
 * `trustFreshFolderAndWrite()` trusts first and then writes, as one step.
 */
async function addServer(
  agentDir: string,
  project: Project | undefined,
  internals: PiSdkInternals,
  body: Record<string, unknown>,
) {
  const request = readAddRequest(body);
  if (request instanceof Refusal) return request.response();
  const { scope } = request;
  const harnesses = request.harnesses ?? ["pi"];
  const piTarget = harnesses.includes("pi");
  const claudeTarget = harnesses.includes("claude");
  if (scope === "local" && piTarget) {
    return refusal(409, "harness-unsupported", "pi holds no local scope: the local group is Claude's alone");
  }
  if (scope !== "global" && !project) return refusal(400, "invalid-request", "A project or local server needs the project's cwd");
  // claude's gate runs before any file is written (r2#3): a binary missing or
  // a version the gate does not recognize refuses the whole add, so no side
  // is left half-written; a CLI failure after the gate takes pi's entry back.
  if (claudeTarget) {
    const gate = await claudeMcpWriteGate(scope, claudeWriteContext(project));
    if (!gate.ok) return claudeWriteRefusal(gate).response();
  }
  const trustFolder = scope === "project" && piTarget && body.trustFolder === true && project !== undefined;
  // Where it may go is said first: a confirmation asked for a server the folder then refuses would be asked for nothing.
  const target = trustFolder || !piTarget ? undefined : writeTarget(scope, project, agentDir);
  if (target instanceof Refusal) return target.response();
  if (trustFolder && project) {
    const breadth = freshFolderTrustBreadth(project.cwd, { agentDir, knownFolders: project.allowedRoots });
    if (breadth) return refusal(409, "trust-too-broad", `Trusting ${project.cwd} would also trust ${breadth.path}`, { breadth });
  }
  // A name either side's file already defines is refused, exactly as pi's own file is checked.
  const takenNames = [
    ...(piTarget && scope !== "local" ? namesInFile(agentDir, project, scope, internals) : []),
    ...(claudeTarget ? claudeNamesInScope(scope, project?.cwd) : []),
  ];
  const prepared = prepareMcpAdd(request, { takenNames, internals });
  if (!prepared.ok) return addRefusal(prepared).response();
  const { name, entry } = prepared;

  // The pi half, when the selection names it.
  let piPath: string | undefined;
  let piExtras: Omit<McpActionResponse, keyof McpResponse> = {};
  if (piTarget) {
    if (trustFolder && project) {
      const trustTarget = { scope: "project" as const, cwd: project.cwd, allowedRoots: project.allowedRoots };
      const result = await trustFreshFolderAndWrite(
        project.cwd,
        agentDir,
        () => insertMcpServer(trustTarget, name, entry),
        { knownFolders: project.allowedRoots },
      );
      if (!result.ok) {
        switch (result.reason) {
          case "trust-too-broad":
            return refusal(409, "trust-too-broad", result.error, { breadth: result.breadth });
          case "folder-not-fresh":
            return refusal(409, "folder-not-fresh", result.error, result.status ? { trust: result.status } : {});
          case "trust-unreadable":
            return refusal(409, "trust-unreadable", result.error);
          case "write-failed": {
            const failure = insertRefusal(result.writeError, agentDir, project, scope, internals);
            if (result.rollbackError === undefined) return failure.response();
            // The write's own reason still says why nothing was added; `trustKept` says the folder stays trusted.
            return refusal(failure.status, failure.reason, `${failure.error}; the folder stays trusted, since taking the trust back failed: ${result.rollbackError}`, {
              ...failure.params,
              trustKept: true,
              ...(result.status ? { trust: result.status } : {}),
            });
          }
        }
      }
      invalidateModelsCache();
      piPath = result.value.path;
      piExtras = { trust: result.status, trustedFolder: true };
    } else {
      if (!target) return refusal(500, "internal", "No file to write to");
      try {
        ({ path: piPath } = await insertMcpServer(target, name, entry));
      } catch (error) {
        return insertRefusal(error, agentDir, project, scope, internals).response();
      }
      if (scope === "project" && project) {
        const trust = trustAfterWrite(project.cwd, agentDir);
        if (trust) piExtras = { trust };
      }
    }
  }

  // The claude half, when the selection names it (AC2): pi's prepared entry
  // projected to the fields claude's own config keeps, through its CLI alone.
  // A failure here is not the whole add's failure alone (r2#3): pi's entry
  // goes back out, so a retry meets the same refusal, never a name-taken.
  if (claudeTarget) {
    const outcome = await claudeMcpAddJson(scope, name, claudeMcpEntryFromPi(entry as unknown as Record<string, unknown>), claudeWriteContext(project));
    if (!outcome.ok) {
      if (piTarget && target) await removeMcpServer(target, name).catch(() => {});
      return claudeWriteRefusal(outcome).response();
    }
  }

  const claudePath = scope === "project" && project
    ? join(project.cwd, ".mcp.json")
    : claudeMcpStatePath({ env: process.env });
  return overviewResponse(agentDir, project, {
    ...piExtras,
    added: { scope, name, path: piTarget ? (piPath as string) : claudePath, ...(claudeTarget ? { harness: "claude" as const } : {}) },
  });
}

/**
 * Removes a claude entry through its CLI only (AC2, Q1 A — the panel words it
 * 「从 Claude 移除」): the raw entry is read first and held server-side for
 * the 60-second undo, exactly as pi's removal holds its own, so taking a
 * claude entry away can be put back through `claude mcp add-json`.
 */
async function removeClaudeServer(agentDir: string, project: Project | undefined, scope: McpScope, name: string) {
  if (scope !== "global" && !project) {
    return refusal(400, "invalid-request", `claude's ${scope} scope needs the project's cwd`);
  }
  const raw = readClaudeMcpRawEntry({ scope, name, ...(project !== undefined ? { cwd: project.cwd } : {}), env: process.env });
  if (!raw.ok) return refusal(409, "server-missing", raw.error, { path: raw.path, name });
  const outcome = await claudeMcpRemove(scope, name, claudeWriteContext(project));
  if (!outcome.ok) return claudeWriteRefusal(outcome).response();
  const { token, expiresAt } = holdRemovedEntry({
    scope,
    name,
    ...(project !== undefined ? { cwd: project.cwd } : {}),
    harness: "claude",
    path: raw.sourcePath,
    entry: raw.entry,
    index: 0,
  });
  return overviewResponse(agentDir, project, {
    undo: { scope, name, harness: "claude", token, path: raw.sourcePath, expiresInMs: Math.max(0, expiresAt - Date.now()) },
  });
}

/**
 * The 「应用到」 change (AC3): the row's chosen harnesses hold the entry
 * afterwards, and nothing else moves — a harness that just lost its copy is
 * removed from (pi's writer, claude's CLI, either way undoable), a chosen
 * harness that lacks it gets the other side's native definition projected.
 * The answer carries one result per harness, so a failure on one side never
 * reads as a total success.
 */
async function applyToChange(
  agentDir: string,
  project: Project | undefined,
  scope: McpScope,
  name: string,
  harnesses: ("pi" | "claude")[],
) {
  if (harnesses.includes("pi") && scope === "local") {
    return refusal(409, "harness-unsupported", `pi holds no local scope: "${name}" in the local group is Claude's alone`, { name });
  }
  const piRaw = scope === "local" ? undefined : readMcpServerEntry({ agentDir, scope, name, project });
  const claudeRaw = readClaudeMcpRawEntry({ scope, name, ...(project !== undefined ? { cwd: project.cwd } : {}), env: process.env });
  const piHas = piRaw?.ok === true;
  const claudeHas = claudeRaw.ok;
  if (!piHas && !claudeHas) {
    const path = piRaw && !piRaw.ok ? piRaw.path : claudeRaw.path;
    return refusal(409, "server-missing", `No harness defines MCP server "${name}" in the ${scope} group`, { path, name });
  }
  const targets = new Set(harnesses);
  const results: McpApplyResult[] = [];
  for (const harness of ["pi", "claude"] as const) {
    if (harness === "pi" && scope === "local") continue;
    const want = targets.has(harness);
    const has = harness === "pi" ? piHas : claudeHas;
    if (want === has) {
      results.push({ harness, ok: true });
      continue;
    }
    if (want) {
      // Add the missing harness's copy from the other side's native definition.
      if (harness === "claude") {
        if (!piRaw?.ok) {
          results.push({ harness, ok: false, reason: "server-missing", error: `pi does not define "${name}", so there is nothing to apply to claude` });
          continue;
        }
        const outcome = await claudeMcpAddJson(scope, name, claudeMcpEntryFromPi(piRaw.value as unknown as Record<string, unknown>), claudeWriteContext(project));
        results.push(outcome.ok ? { harness, ok: true } : { harness, ok: false, reason: "claude-unwritable", error: outcome.error });
      } else {
        if (!claudeRaw.ok) {
          results.push({ harness, ok: false, reason: "server-missing", error: `claude does not define "${name}", so there is nothing to apply to pi` });
          continue;
        }
        const projected = piMcpEntryFromClaude(claudeRaw.entry);
        if (projected === undefined) {
          results.push({ harness, ok: false, reason: "harness-unsupported", error: `pi loads no sse or ws entry: "${name}"` });
          continue;
        }
        const target = writeTarget(scope, project, agentDir);
        if (target instanceof Refusal) {
          results.push({ harness, ok: false, reason: target.reason, error: target.error });
          continue;
        }
        try {
          await insertMcpServer(target, name, projected);
          results.push({ harness, ok: true });
        } catch (error) {
          const failure = writeRefusal(error);
          results.push({ harness, ok: false, reason: failure.reason, error: failure.error });
        }
      }
      continue;
    }
    // The harness holds a copy the selection no longer names: remove it, undoably.
    if (harness === "claude") {
      if (!claudeRaw.ok) {
        results.push({ harness, ok: false, reason: "server-missing", error: `claude does not define "${name}" anymore` });
        continue;
      }
      const removal = await claudeMcpRemove(scope, name, claudeWriteContext(project));
      if (removal.ok) {
        const { token, expiresAt } = holdRemovedEntry({
          scope,
          name,
          ...(project !== undefined ? { cwd: project.cwd } : {}),
          harness: "claude",
          path: claudeRaw.sourcePath,
          entry: claudeRaw.entry,
          index: 0,
        });
        results.push({ harness, ok: true, undo: { token, path: claudeRaw.sourcePath, expiresInMs: Math.max(0, expiresAt - Date.now()) } });
      } else {
        results.push({ harness, ok: false, reason: "claude-unwritable", error: removal.error });
      }
    } else {
      const target = writeTarget(scope, project, agentDir);
      if (target instanceof Refusal) {
        results.push({ harness, ok: false, reason: target.reason, error: target.error });
        continue;
      }
      try {
        const removed = await removeMcpServer(target, name);
        const { token, expiresAt } = holdRemovedEntry({
          scope,
          name,
          path: removed.path,
          ...(target.scope === "project" ? { cwd: target.cwd } : {}),
          entry: removed.value.entry,
          index: removed.value.index,
        });
        results.push({ harness, ok: true, undo: { token, path: removed.path, expiresInMs: Math.max(0, expiresAt - Date.now()) } });
      } catch (error) {
        const failure = writeRefusal(error);
        results.push({ harness, ok: false, reason: failure.reason, error: failure.error });
      }
    }
  }
  return overviewResponse(agentDir, project, { applied: { scope, name, results } });
}

/**
 * 「统一成当前值」 (AC3): one side's definition wins and only the other side
 * is written — claude's as remove + add-json of the projected entry with the
 * staged old definition rolled back on failure, pi's as remove + insert
 * under its own writer, put back if the insert fails.
 */
async function unifyServer(
  agentDir: string,
  project: Project | undefined,
  scope: McpScope,
  name: string,
  from: "pi" | "claude",
) {
  if (scope === "local") {
    return refusal(409, "harness-unsupported", "The local group is Claude's alone; there is no other side to unify");
  }
  const piRaw = readMcpServerEntry({ agentDir, scope, name, project });
  const claudeRaw = readClaudeMcpRawEntry({ scope, name, ...(project !== undefined ? { cwd: project.cwd } : {}), env: process.env });
  if (!piRaw.ok) return refusal(409, "server-missing", `Both harnesses must define "${name}" to unify it`, { path: piRaw.path, name });
  if (!claudeRaw.ok) return refusal(409, "server-missing", `Both harnesses must define "${name}" to unify it`, { path: claudeRaw.path, name });
  if (from === "pi") {
    const outcome = await claudeMcpWriteEntry(scope, name, claudeMcpEntryFromPi(piRaw.value as unknown as Record<string, unknown>), {
      ...claudeWriteContext(project),
      previous: claudeRaw.entry,
    });
    if (!outcome.ok) return claudeWriteRefusal(outcome).response();
  } else {
    const projected = piMcpEntryFromClaude(claudeRaw.entry);
    if (projected === undefined) {
      return refusal(409, "harness-unsupported", `pi loads no sse or ws entry: "${name}"`, { name });
    }
    const target = writeTarget(scope, project, agentDir);
    if (target instanceof Refusal) return target.response();
    try {
      const removed = await removeMcpServer(target, name);
      try {
        await insertMcpServer(target, name, projected);
      } catch (error) {
        // Put the old entry back where it stood; a failure to do so is the
        // write's own failure, already on its way out.
        await insertMcpServer(target, name, removed.value.entry, removed.value.index).catch(() => {});
        throw error;
      }
    } catch (error) {
      return writeRefusal(error).response();
    }
  }
  return overviewResponse(agentDir, project);
}

// POST /api/mcp body: { action, cwd?, ... }
//   enable | disable | remove | sign-out: { scope, name } (pi's own scopes)
//   remove: { scope, name, harness: "claude" } → claude's copy, through its CLI, undoably (#85 85c)
//   set-apply: { scope, name, harnesses } → the row's 「应用到」, per-harness `applied.results` (#85 85c)
//   unify: { scope, name, from } → 「统一成当前值」, only the other side written (#85 85c)
//   set-enabled: { enabled, servers: [{ scope, name }] } → per-server `results`
//   set-exposure: { scope, name, exposure }
//   set-in-project: { name, enabled } → a global server on or off in `cwd`'s project alone
//   undo: { token } (from a remove's `undo`, pi's or claude's)
//   add: { text, scope, harnesses?, values?, secretReferences?, server?, name?, rawPi?, trustFolder?, confirmHostEnv? }
// `cwd` is the panel's project: required for a project server, and the
// overview in the answer covers it.
export async function POST(req: Request) {
  if (!isApiRequestAllowed(req)) return refusal(403, "request-denied", "Untrusted API request");
  if (!hasJsonContentType(req)) return refusal(415, "content-type", "Content-Type must be application/json");
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return refusal(400, "invalid-request", "Invalid JSON body");
  }
  if (!isRecord(body)) return refusal(400, "invalid-request", "Expected a JSON object");

  let project: Project | undefined;
  if (body.cwd !== undefined && body.cwd !== null) {
    const result = await validateProject(body.cwd);
    if (result instanceof Refusal) return result.response();
    project = result;
  }

  const internals = await mcpWritable();
  if (internals instanceof Refusal) return internals.response();
  const agentDir = getAgentDir();

  try {
    switch (body.action) {
      case "enable":
      case "disable":
      case "remove":
      case "sign-out": {
        const name = readName(body.name);
        if (name === undefined) return refusal(400, "invalid-request", "name must be a server name");
        if (body.action === "remove" && body.harness === "claude") {
          const scope = readUnifiedScope(body.scope);
          if (!scope) return refusal(400, "invalid-request", "scope must be \"global\", \"project\" or \"local\" for a claude removal");
          return await removeClaudeServer(agentDir, project, scope, name);
        }
        const scope = readScope(body.scope);
        if (!scope) return refusal(400, "invalid-request", "scope must be \"global\" or \"project\", and name a server name");
        if (body.action === "remove") return await removeServer(agentDir, project, { scope, name });
        if (body.action === "sign-out") return await signOutServer(agentDir, project, internals, { scope, name });
        return await switchServer(agentDir, project, internals, { scope, name }, body.action === "enable");
      }
      case "set-apply": {
        const scope = readUnifiedScope(body.scope);
        const name = readName(body.name);
        const harnesses = readHarnesses(body.harnesses);
        if (!scope || name === undefined || !harnesses) {
          return refusal(400, "invalid-request", "set-apply needs scope (global, project or local), name, and a non-empty harnesses list");
        }
        if (scope !== "global" && !project) return refusal(400, "invalid-request", "A project or local row needs the project's cwd");
        return await applyToChange(agentDir, project, scope, name, harnesses);
      }
      case "unify": {
        const scope = readUnifiedScope(body.scope);
        const name = readName(body.name);
        if (!scope || name === undefined || (body.from !== "pi" && body.from !== "claude")) {
          return refusal(400, "invalid-request", "unify needs scope, name, and from (\"pi\" or \"claude\")");
        }
        if (scope !== "global" && !project) return refusal(400, "invalid-request", "A project row needs the project's cwd");
        return await unifyServer(agentDir, project, scope, name, body.from);
      }
      case "set-enabled": {
        const servers = readServerList(body.servers);
        if (typeof body.enabled !== "boolean" || !servers) {
          return refusal(400, "invalid-request", `enabled must be a boolean, and servers a list of 1 to ${MAX_BULK_SERVERS} { scope, name }`);
        }
        return await switchServers(agentDir, project, internals, servers, body.enabled);
      }
      case "set-exposure": {
        const scope = readScope(body.scope);
        const name = readName(body.name);
        if (!scope || name === undefined || !isMcpExposure(body.exposure)) {
          return refusal(400, "invalid-request", `scope must be "global" or "project", name a server name, and exposure one of ${MCP_EXPOSURES.join(", ")}`);
        }
        return await setServerExposure(agentDir, project, { scope, name }, body.exposure);
      }
      case "set-in-project": {
        const name = readName(body.name);
        if (name === undefined || typeof body.enabled !== "boolean") {
          return refusal(400, "invalid-request", "name must name a global server, and enabled be a boolean");
        }
        return await switchServerInProject(agentDir, project, internals, name, body.enabled);
      }
      case "undo": {
        if (typeof body.token !== "string" || body.token.length === 0 || body.token.length > 100) {
          return refusal(400, "invalid-request", "token must be the token a remove answered with");
        }
        return await undoRemoval(agentDir, project, body.token);
      }
      case "add":
        return await addServer(agentDir, project, internals, body);
      default:
        return refusal(400, "invalid-request", "action must be add, enable, disable, remove, set-enabled, set-exposure, set-in-project, undo or sign-out");
    }
  } catch (error) {
    return refusal(500, "internal", errorMessage(error));
  }
}
