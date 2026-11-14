// lib/polyforge-overrides.ts — polyforge#25 (stub, u8): the per-step
// overlay face the panel reaches: `pf [--profile p] --json override <wi>`
// (the view) and its one-action writes. The real face lands with this
// unit; the signatures below are the seams its tests compile against.

import { PolyforgeError } from "@/lib/polyforge-status";

/** One step row of the override view. */
export interface PolyforgeOverrideStepRow {
  id: string;
  status: string;
  skippable: boolean;
  models?: string[];
  modelsBy?: string;
  skip?: boolean;
  skipBy?: string;
  pending?: boolean;
  effective?: string[];
  source?: string;
}

/** The view: the work item's posture and one row per declared step. */
export interface PolyforgeOverrideView {
  wi: string;
  posture: string;
  steps: PolyforgeOverrideStepRow[];
}

/** One write's answer. */
export interface PolyforgeOverrideWriteResult {
  wi: string;
  changed: boolean;
  posture: string;
  published: boolean;
  note?: string;
  step: PolyforgeOverrideStepRow;
}

/** One overlay action, exactly one per write. */
export type PolyforgeOverrideAction =
  | { kind: "models"; step: string; models: string[] }
  | { kind: "clearModels"; step: string }
  | { kind: "skip"; step: string; value: boolean };

export function sanitizeOverrideView(machine: unknown): PolyforgeOverrideView | null {
  return null;
}

export function sanitizeOverrideWriteResult(machine: unknown): PolyforgeOverrideWriteResult | null {
  return null;
}

export function overrideActionArgv(wi: string, action: PolyforgeOverrideAction): string[] | null {
  return null;
}

export async function getPolyforgeOverride(wi: string, profile?: string): Promise<PolyforgeOverrideView | null> {
  void wi;
  void profile;
  return null;
}

export async function sendPolyforgeOverrideAction(
  wi: string,
  action: PolyforgeOverrideAction,
  profile?: string,
): Promise<PolyforgeOverrideWriteResult> {
  void wi;
  void action;
  void profile;
  throw new PolyforgeError("not implemented");
}
