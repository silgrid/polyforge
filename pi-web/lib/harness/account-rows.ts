// The account page's Cell-row model (polyforge#142, 85a2 AC1/AC7) —
// placeholder: the test-first commit carries only the types and signature
// so the suite's assertions run red; the implementation lands with the unit.

export interface DoctorHarnessFacts {
  configRoot?: string;
  credential?: string;
  source?: string;
  sourceKind?: string;
  credentialsFile?: string;
  settingsEnv?: Record<string, string>;
}

export interface DoctorCellFacts {
  profile: string;
  executionDisabled?: string;
  probeError?: string;
  source?: string;
  sourceKind?: string;
  harnesses?: Record<string, DoctorHarnessFacts>;
}

export interface HarnessCapabilityFacts {
  id: string;
  credentialsFollowConfigRoot: boolean;
}

export interface PiStepCheck {
  sourceDir: string;
  status: "ready" | "not_ready" | "unknown";
  authType: string | null;
  reason: string | null;
}

export interface AccountHarnessRow {
  id: string;
  configRoot: string | null;
  credential: "set" | "unset" | null;
  status: "ready" | "not_ready" | "unknown";
  authType: string | null;
  checkReason: string | null;
  source: string | null;
  sourceKind: string | null;
  credentialsFollow: boolean;
  keychainHint: boolean;
  credentialKinds: { oauthToken: boolean; apiKey: boolean; gateway: boolean } | null;
  credentialsFile: "present" | "absent" | null;
  usable: boolean;
  reason: string | null;
}

export interface AccountCellRow {
  profile: string;
  executionDisabled: string | null;
  probeError: string | null;
  harnessOrder: string[];
  harnesses: Record<string, AccountHarnessRow>;
}

export function buildAccountCellRow(_cell: DoctorCellFacts, _capabilities: Record<string, HarnessCapabilityFacts>, _piChecks: Map<string, PiStepCheck>): AccountCellRow {
  return { profile: _cell.profile, executionDisabled: null, probeError: null, harnessOrder: [], harnesses: {} };
}
