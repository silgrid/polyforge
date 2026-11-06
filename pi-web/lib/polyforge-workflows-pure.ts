import type {
  PolyforgeWorkflowChain,
  PolyforgeWorkflowsDoc,
  PolyforgeWorkflowsRead,
  PolyforgeWorkflowWorkspace,
} from "./polyforge-workflows";

/**
 * Client-safe row model of the Settings › Polyforge workflow card
 *: no React, no Node import (type imports only). It decides
 * the default project, one row per step id across bundles (with every bundle
 * it appears in), the row order (`default`, the step ids alphabetically, then
 * `task`), the declared-vs-inherited chain, the per-bundle effective chain
 * when bundles disagree, and the chain edit operations. The precedence itself
 * is pf's (`effective`/`from` of `pf workflows`); nothing here resolves it.
 */

export interface WorkflowChainView {
  /** The bundle this chain belongs to; null when every bundle agrees. */
  bundle: string | null;
  models: string[];
  from: string;
}

export interface WorkflowBundleRef {
  bundle: string;
  /** The bundle's own candidates for this row (its step model_candidates,
   *  or its defaults for the `default` row); null = it declares none. */
  candidates: string[] | null;
}

export type WorkflowRowKind = "default" | "step" | "task";

export interface WorkflowRow {
  id: string;
  kind: WorkflowRowKind;
  bundles: WorkflowBundleRef[];
  /** steps.<project>.<id>.models is declared in the yaml (declared-empty too). */
  declared: boolean;
  declaredModels: string[] | null;
  /** One entry when the bundles agree (bundle null), else one per bundle;
   *  empty when no level governs (the harness default model runs). */
  effective: WorkflowChainView[];
  diverges: boolean;
  /** Some lane declares the step `skippable: true`: the row
   *  may carry the project's skip switch. Always false for default/task. */
  skippable: boolean;
  /** The project's current `steps.<project>.<id>.skip` as pf reports it,
   *  read only from the lanes that declare the step skippable. */
  skip: boolean;
}

export const DEFAULT_ROW_ID = "default";
export const TASK_ROW_ID = "task";

/** One workspace of the card's union: its yaml path (the
 *  projects' source label) and its two reads; `reason` marks a failed read. */
export interface WorkflowWorkspaceEntry {
  workspace: string;
  yamlPath: string;
  workflows: PolyforgeWorkflowsDoc | null;
  declared: PolyforgeWorkflowWorkspace | null;
  reason?: string;
}

/** The union of every read workspace's projects: each project with the
 *  workspace that owns it (the first to list it), the readable workspaces in
 *  the mapping's order, and the failed ones (no project, so no write). */
export interface WorkflowUnion {
  projects: { project: string; owner: WorkflowWorkspaceEntry }[];
  sources: WorkflowWorkspaceEntry[];
  failures: WorkflowWorkspaceEntry[];
}

function yamlPathOf(workspace: string, declared: PolyforgeWorkflowWorkspace | null): string {
  return declared?.yaml.path || (workspace ? `${workspace.replace(/\/+$/, "")}/.polyforge.yaml` : ".polyforge.yaml");
}

/** The read → the union. A route answer without `workspaces` (an older
 *  server or a single-workspace fixture) is one entry built from its
 *  top-level `workflows`/`workspace` fields. */
export function workflowUnion(read: Pick<PolyforgeWorkflowsRead, "workflows" | "workspace" | "workspaces"> | null): WorkflowUnion {
  const raw = read?.workspaces
    ?? (read?.workflows ? [{ workspace: read.workflows.workspace ?? read.workspace?.workspace ?? "", workflows: read.workflows, declared: read.workspace }] : []);
  const union: WorkflowUnion = { projects: [], sources: [], failures: [] };
  for (const item of raw) {
    const entry: WorkflowWorkspaceEntry = { ...item, yamlPath: yamlPathOf(item.workspace, item.declared) };
    if (entry.reason || !entry.workflows) {
      union.failures.push({ ...entry, reason: entry.reason || "unreadable" });
      continue;
    }
    union.sources.push(entry);
    for (const project of entry.workflows.projects) {
      if (!union.projects.some((known) => known.project === project)) union.projects.push({ project, owner: entry });
    }
  }
  return union;
}

/** The workspace that owns a project in the union (null: no such project). */
export function projectOwner(union: WorkflowUnion, project: string | null): WorkflowWorkspaceEntry | null {
  return union.projects.find((entry) => entry.project === project)?.owner ?? null;
}

/** The card's project: the first project the cwd mapping lists. */
export function defaultProject(projects: readonly string[]): string | null {
  return projects.length > 0 ? projects[0] : null;
}

function sameChain(a: PolyforgeWorkflowChain | undefined, b: PolyforgeWorkflowChain | undefined): boolean {
  if (!a || !b) return a === b;
  return a.models.length === b.models.length && a.models.every((model, i) => model === b.models[i]);
}

function effectiveViews(perBundle: { bundle: string; chain: PolyforgeWorkflowChain | undefined }[]): { views: WorkflowChainView[]; diverges: boolean } {
  if (perBundle.length === 0) return { views: [], diverges: false };
  const first = perBundle[0].chain;
  const diverges = perBundle.some((entry) => !sameChain(entry.chain, first));
  if (!diverges) return { views: first ? [{ bundle: null, models: first.models, from: first.from }] : [], diverges: false };
  return {
    views: perBundle.filter((entry) => entry.chain).map((entry) => ({ bundle: entry.bundle, models: entry.chain!.models, from: entry.chain!.from })),
    diverges: true,
  };
}

/** The card's rows for one project, in the card's order. */
export function buildWorkflowRows(
  doc: PolyforgeWorkflowsDoc | null,
  workspace: PolyforgeWorkflowWorkspace | null,
  project: string | null,
): WorkflowRow[] {
  if (!doc || !project) return [];
  const declaredSteps = workspace?.steps[project] ?? {};
  const bundles = [...doc.bundles].sort((a, b) => a.name.localeCompare(b.name));
  const declared = (id: string) => {
    const entry = declaredSteps[id];
    return { declared: entry?.declared === true, declaredModels: entry?.declared ? entry.models ?? [] : null };
  };

  const defaultEffective = effectiveViews(bundles.map((b) => ({ bundle: b.name, chain: b.defaults.effective?.[project] })));
  const rows: WorkflowRow[] = [{
    id: DEFAULT_ROW_ID,
    kind: "default",
    bundles: bundles.map((b) => ({ bundle: b.name, candidates: b.defaults.model_candidates })),
    ...declared(DEFAULT_ROW_ID),
    effective: defaultEffective.views,
    diverges: defaultEffective.diverges,
    skippable: false,
    skip: false,
  }];

  const ids = [...new Set(bundles.flatMap((b) => b.steps.map((s) => s.id)))]
    .filter((id) => id !== DEFAULT_ROW_ID && id !== TASK_ROW_ID)
    .sort((a, b) => a.localeCompare(b));
  for (const id of ids) {
    const holders = bundles.flatMap((b) => {
      const step = b.steps.find((s) => s.id === id);
      return step ? [{ bundle: b.name, step }] : [];
    });
    const eff = effectiveViews(holders.map((h) => ({ bundle: h.bundle, chain: h.step.effective?.[project] })));
    rows.push({
      id,
      kind: "step",
      bundles: holders.map((h) => ({ bundle: h.bundle, candidates: h.step.model_candidates ?? null })),
      ...declared(id),
      effective: eff.views,
      diverges: eff.diverges,
      skippable: holders.some((h) => h.step.skippable === true),
      // a skip counts only where its lane declares the step skippable
      skip: holders.some((h) => h.step.skippable === true && h.step.skip?.[project] === true),
    });
  }

  const task = doc.task?.[project];
  rows.push({
    id: TASK_ROW_ID,
    kind: "task",
    bundles: [],
    ...declared(TASK_ROW_ID),
    effective: task ? [{ bundle: null, models: task.models, from: task.from }] : [],
    diverges: false,
    skippable: false,
    skip: false,
  });
  return rows;
}

/** Append one candidate (trimmed; a duplicate or blank is ignored). */
export function addCandidate(list: readonly string[], candidate: string): string[] {
  const value = candidate.trim();
  if (!value || list.includes(value)) return [...list];
  return [...list, value];
}

export function removeCandidate(list: readonly string[], index: number): string[] {
  return list.filter((_, i) => i !== index);
}

/** Move the candidate at index by delta (-1 up, +1 down); out of range is a no-op. */
export function moveCandidate(list: readonly string[], index: number, delta: number): string[] {
  const target = index + delta;
  if (index < 0 || index >= list.length || target < 0 || target >= list.length) return [...list];
  const next = [...list];
  [next[index], next[target]] = [next[target], next[index]];
  return next;
}
