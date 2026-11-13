import { spawn } from "node:child_process";
import { isPolyforgeVersionBelowMinimum } from "./polyforge-detect";
import { polyforgePfBin } from "./polyforge-pf-bin";
import { probePolyforgeVersion } from "./polyforge-status";
import { polyforgeNamedCell, type PolyforgeConfigScope, type PolyforgeScopeArgv } from "./polyforge-cells-pure";
import { pinPolyforgeScope } from "./polyforge-config-scope";
import { workspaceSectionPath } from "../components/polyforge-config-helpers";

/**
 * Server-side read of the Settings › Polyforge WORKFLOW card.
 * the card shows the union of every cli.toml-mapped
 * workspace's projects. One `pf --json config list` reads the
 * `[workspace."<path>"]` mapping, then each mapped workspace is read with its
 * own `--cwd`: `pf --json workflows` (the installed bundles, their steps,
 * candidates and each project's effective chain) and
 * `pf --json config workspace` (the declared values and the yaml CAS hash) —
 * the workspaces in parallel, each independently degraded. The bound is 1 mapping read
 * + 2 reads per mapped workspace, never one spawn per project, and nothing is
 * cached: every call (each ↻ click) spawns them afresh. A machine with no
 * mapping (or an unreadable list) reads the settings cwd alone, as #216 did.
 * The panel never parses a bundle or the yaml itself. Every degradation (pf
 * absent, below the floor, a failed or undecodable read) is a field of a 200
 * answer, never an exception.
 * every read here is pinned to the pane's Cell, never to the
 * Cell pf would resolve from the server's PF_PROFILE or cwd.:
 * every Cell's pane has the card — its workspaces are that Cell's own
 * mapping (`cli.toml` for default, `cli-<profile>.toml` for a named Cell),
 * read with `--profile <name>`. A named Cell with no mapping reads nothing
 * and says so (the settings cwd is the default Cell's fallback only); a pf
 * that cannot address the Cell answers capable:false with the upgrade hint.
 */

/** The workflow card's floor: the first pf with `pf workflows` (the next
 *  release after the v2.20.14 baseline). The configuration panel's 2.20.6
 *  floor is unchanged. */
export const POLYFORGE_WORKFLOW_CARD_MINIMUM_PF_VERSION = "2.20.15";

const READ_TIMEOUT_MS = 15000;

export interface PolyforgeWorkflowChain {
  models: string[];
  from: string;
}

export interface PolyforgeWorkflowStep {
  id: string;
  model_candidates?: string[];
  effective?: Record<string, PolyforgeWorkflowChain>;
  /** The lane declares the step `skippable: true`; kept only when true. */
  skippable?: true;
  /** Each mapped project's current `steps.<project>.<id>.skip`, as pf judged it
   *  (only a step its lane declares skippable carries it). */
  skip?: Record<string, boolean>;
}

export interface PolyforgeWorkflowBundle {
  name: string;
  defaults: { model_candidates: string[] | null; effective?: Record<string, PolyforgeWorkflowChain> };
  steps: PolyforgeWorkflowStep[];
}

/** `pf --json workflows` after the sanitizer (only the documented fields). */
export interface PolyforgeWorkflowsDoc {
  workflows_dir: string;
  daemon_reachable: boolean;
  reason: string;
  workspace: string | null;
  projects: string[];
  write_capable: boolean;
  write_reason: string;
  task?: Record<string, PolyforgeWorkflowChain>;
  bundles: PolyforgeWorkflowBundle[];
  bundle_errors: { bundle: string; reason: string }[];
}

export interface PolyforgeWorkspaceStep {
  declared: boolean;
  models: string[] | null;
  source: string;
  class: string;
}

/** The declared values the card shows, from `pf --json config workspace`. */
export interface PolyforgeWorkflowWorkspace {
  workspace: string;
  /** The workspace `.polyforge.yaml` (each project's source label). */
  yaml: { path: string; sha256: string | null; exists: boolean };
  steps: Record<string, Record<string, PolyforgeWorkspaceStep>>;
}

/** One mapped workspace's two reads. A failed read leaves
 *  `reason` set: the workspace contributes no project and so no write. */
export interface PolyforgeWorkflowsWorkspaceRead {
  workspace: string;
  workflows: PolyforgeWorkflowsDoc | null;
  declared: PolyforgeWorkflowWorkspace | null;
  reason?: string;
}

export interface PolyforgeWorkflowsRead {
  pfPresent: boolean;
  pfVersion: string | null;
  capable: boolean;
  reason?: string;
  /** The settings cwd's own workspace (#216 consumers); null when the cwd
   *  lies in no read workspace. */
  workflows: PolyforgeWorkflowsDoc | null;
  workspace: PolyforgeWorkflowWorkspace | null;
  /** Every read workspace, in the mapping's order. */
  workspaces?: PolyforgeWorkflowsWorkspaceRead[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

function booleans(value: unknown): Record<string, boolean> | undefined {
  if (!isRecord(value)) return undefined;
  const out: Record<string, boolean> = {};
  for (const [key, flag] of Object.entries(value)) {
    if (typeof flag === "boolean") out[key] = flag;
  }
  return out;
}

function chains(value: unknown): Record<string, PolyforgeWorkflowChain> | undefined {
  if (!isRecord(value)) return undefined;
  const out: Record<string, PolyforgeWorkflowChain> = {};
  for (const [project, chain] of Object.entries(value)) {
    if (isRecord(chain)) out[project] = { models: strings(chain.models), from: str(chain.from) };
  }
  return out;
}

/** `pf --json workflows` → the documented shape (unknown fields dropped). */
export function sanitizeWorkflowsDoc(raw: unknown): PolyforgeWorkflowsDoc | null {
  if (!isRecord(raw) || !Array.isArray(raw.bundles)) return null;
  const bundles: PolyforgeWorkflowBundle[] = [];
  for (const b of raw.bundles) {
    if (!isRecord(b) || !str(b.name)) continue;
    const defaults = isRecord(b.defaults) ? b.defaults : {};
    const steps: PolyforgeWorkflowStep[] = [];
    for (const s of Array.isArray(b.steps) ? b.steps : []) {
      if (!isRecord(s) || !str(s.id)) continue;
      const step: PolyforgeWorkflowStep = { id: str(s.id) };
      if (Array.isArray(s.model_candidates)) step.model_candidates = strings(s.model_candidates);
      const effective = chains(s.effective);
      if (effective) step.effective = effective;
      if (s.skippable === true) step.skippable = true;
      const skip = booleans(s.skip);
      if (skip) step.skip = skip;
      steps.push(step);
    }
    const effective = chains(defaults.effective);
    bundles.push({
      name: str(b.name),
      defaults: { model_candidates: Array.isArray(defaults.model_candidates) ? strings(defaults.model_candidates) : null, ...(effective ? { effective } : {}) },
      steps,
    });
  }
  const task = chains(raw.task);
  return {
    workflows_dir: str(raw.workflows_dir),
    daemon_reachable: raw.daemon_reachable === true,
    reason: str(raw.reason),
    workspace: typeof raw.workspace === "string" ? raw.workspace : null,
    projects: strings(raw.projects),
    write_capable: raw.write_capable === true,
    write_reason: str(raw.write_reason),
    ...(task ? { task } : {}),
    bundles,
    bundle_errors: (Array.isArray(raw.bundle_errors) ? raw.bundle_errors : []).filter(isRecord)
      .map((e) => ({ bundle: str(e.bundle), reason: str(e.reason) })),
  };
}

/** `pf --json config workspace` → the declared values the card reads (null
 *  for an unmapped cwd or a document without the step map). */
export function sanitizeWorkflowWorkspace(raw: unknown): PolyforgeWorkflowWorkspace | null {
  if (!isRecord(raw) || raw.mapped !== true || !isRecord(raw.steps)) return null;
  const yaml = isRecord(raw.yaml) ? raw.yaml : {};
  const steps: PolyforgeWorkflowWorkspace["steps"] = {};
  for (const [project, entries] of Object.entries(raw.steps)) {
    if (!isRecord(entries)) continue;
    steps[project] = {};
    for (const [id, e] of Object.entries(entries)) {
      if (!isRecord(e) || typeof e.declared !== "boolean") continue;
      const step: PolyforgeWorkspaceStep = {
        declared: e.declared,
        models: Array.isArray(e.models) ? strings(e.models) : null,
        source: str(e.source) || "default",
        class: str(e.class),
      };
      steps[project][id] = step;
    }
  }
  return {
    workspace: str(raw.workspace),
    yaml: {
      path: str(yaml.path) || (str(raw.workspace) ? `${str(raw.workspace)}/.polyforge.yaml` : ""),
      sha256: typeof yaml.sha256 === "string" ? yaml.sha256 : null,
      exists: yaml.exists === true,
    },
    steps,
  };
}

function runPfJson(argv: string[]): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const child = spawn(polyforgePfBin(), argv, { shell: false, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      reject(new Error("pf did not answer in time"));
    }, READ_TIMEOUT_MS);
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => { stdout += chunk; });
    child.stderr.on("data", (chunk: string) => { stderr += chunk; });
    child.on("error", (error) => {
      clearTimeout(timer);
      reject(error instanceof Error ? error : new Error("polyforge is unavailable"));
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      if (code !== 0) {
        reject(new Error(stderr.trim().slice(0, 512) || `pf exited ${code ?? 1}`));
        return;
      }
      try {
        resolve(JSON.parse(stdout));
      } catch {
        reject(new Error("pf answered something that is not JSON"));
      }
    });
  });
}

/** The cli.toml mapping's workspace paths, in pf's order (deduplicated):
 *  every `[workspace."<abs path>"]` section item of `pf --json config list`.
 *  Only an absolute path is ever passed on as a `--cwd` argv value. */
export function mappedWorkspacesFromList(raw: unknown): string[] {
  const items = isRecord(raw) && Array.isArray(raw.items) ? raw.items : [];
  const paths: string[] = [];
  for (const item of items) {
    if (!isRecord(item) || typeof item.key !== "string") continue;
    const path = workspaceSectionPath(item.key);
    if (path && path.startsWith("/") && !paths.includes(path)) paths.push(path);
  }
  return paths;
}

function failure(read: PromiseSettledResult<unknown>, fallback: string): string {
  if (read.status === "rejected") return read.reason instanceof Error ? read.reason.message : fallback;
  return fallback;
}

/** One workspace's two reads, degraded independently. The workspaces are
 *  read in parallel; inside one workspace the declared-value read runs
 *  first, so a finished `pf workflows` read always has its yaml read beside
 *  it (one deterministic spawn order per workspace). */
async function readWorkspace(workspace: string, pin: PolyforgeScopeArgv): Promise<PolyforgeWorkflowsWorkspaceRead> {
  const args = workspace ? ["--cwd", workspace] : [];
  const [declaredRead] = await Promise.allSettled([
    workspace ? runPfJson([...pin.prefix, "--json", "config", "workspace", "--cwd", workspace]) : Promise.resolve(null),
  ]);
  const [workflowsRead] = await Promise.allSettled([runPfJson([...pin.prefix, "--json", "workflows", ...args])]);
  const workflows = workflowsRead.status === "fulfilled" ? sanitizeWorkflowsDoc(workflowsRead.value) : null;
  const declared = declaredRead.status === "fulfilled" ? sanitizeWorkflowWorkspace(declaredRead.value) : null;
  let reason: string | undefined;
  if (!workflows) reason = failure(workflowsRead, "pf workflows answered without the documented shape");
  else if (workspace && declaredRead.status === "rejected") reason = failure(declaredRead, "pf config workspace is unavailable");
  return { workspace, workflows, declared, ...(reason ? { reason } : {}) };
}

function contains(workspace: string, cwd: string): boolean {
  return cwd === workspace || cwd.startsWith(`${workspace.replace(/\/+$/, "")}/`);
}

/** The one read the workflows route calls, for one Cell. Fresh on every call. */
export async function readPolyforgeWorkflows(cwd: string, scope: PolyforgeConfigScope): Promise<PolyforgeWorkflowsRead> {
  const presence = await probePolyforgeVersion(true);
  if (!presence.present) {
    return { pfPresent: false, pfVersion: null, capable: false, reason: "polyforge is not installed on this machine", workflows: null, workspace: null };
  }
  if (isPolyforgeVersionBelowMinimum(presence.version, POLYFORGE_WORKFLOW_CARD_MINIMUM_PF_VERSION)) {
    return {
      pfPresent: true,
      pfVersion: presence.version,
      capable: false,
      reason: `the workflow card needs pf ${POLYFORGE_WORKFLOW_CARD_MINIMUM_PF_VERSION} or newer (found ${presence.version ?? "an unparseable version"})`,
      workflows: null,
      workspace: null,
    };
  }
  const pinned = await pinPolyforgeScope(scope);
  if (!pinned.ok) {
    return { pfPresent: true, pfVersion: presence.version, capable: false, reason: pinned.reason, workflows: null, workspace: null };
  }
  const pin: PolyforgeScopeArgv = { prefix: pinned.prefix, suffix: pinned.suffix };
  let mapped: string[] = [];
  try {
    mapped = mappedWorkspacesFromList(await runPfJson([...pin.prefix, "--json", "config", "list", ...pin.suffix]));
  } catch {
    mapped = [];
  }
  const named = polyforgeNamedCell(scope);
  if (named && mapped.length === 0) {
    return {
      pfPresent: true,
      pfVersion: presence.version,
      capable: true,
      reason: `Cell ${named} maps no workspace yet, so its workflow card has no project to show — add a workspace to its mapping first`,
      workflows: null,
      workspace: null,
      workspaces: [],
    };
  }
  const workspaces = await Promise.all((mapped.length > 0 ? mapped : [cwd]).map((workspace) => readWorkspace(workspace, pin)));
  const own = mapped.length > 0 ? workspaces.find((entry) => cwd !== "" && contains(entry.workspace, cwd)) ?? null : workspaces[0];
  // The page-level reason is the daemon's (one fact for every workspace) or,
  // when no workspace could be read at all, every workspace's own failure;
  // otherwise a workspace's failure is only its own entry's reason.
  const docs = workspaces.map((entry) => entry.workflows).filter((doc): doc is PolyforgeWorkflowsDoc => doc !== null);
  let reason: string | undefined;
  if (docs.length === 0) {
    reason = workspaces.length === 1
      ? workspaces[0].reason
      : workspaces.map((entry) => `${entry.workspace}: ${entry.reason ?? "unreadable"}`).join("; ");
  } else if (!docs[0].daemon_reachable || docs[0].reason) {
    reason = docs[0].reason || "the daemon is not reachable";
  }
  return {
    pfPresent: true,
    pfVersion: presence.version,
    capable: true,
    ...(reason ? { reason } : {}),
    workflows: own?.workflows ?? null,
    workspace: own?.declared ?? null,
    workspaces,
  };
}
