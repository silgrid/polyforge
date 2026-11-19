// The doctor wrapper (polyforge#142, 85a2 AC1) — placeholder: the
// test-first commit carries only the signatures so the suite's assertions
// run red; the implementation lands with the unit.
import type { DoctorCellFacts } from "./harness/account-rows-facts";

export const DOCTOR_ARGV: readonly string[] = [];

export interface DoctorCellsRead {
  cells: DoctorCellFacts[] | null;
  error: string | null;
}

export function sanitizeDoctorCells(_answer: unknown): DoctorCellFacts[] | null {
  return null;
}

export async function readDoctorCells(_options: { binary?: string; timeoutMs?: number } = {}): Promise<DoctorCellsRead> {
  return { cells: null, error: null };
}
