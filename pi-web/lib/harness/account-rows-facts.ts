// The account-row wire types (polyforge#142, 85a2): client-safe, shared by
// the doctor wrapper (lib/polyforge-doctor.ts), the account route and the
// row model (account-rows.ts). Values never appear in any of them.

/** One harness's doctor facts of one Cell (the daemon's /status?harnesses=1
 * entry, sanitized): set/unset and provenance only. */
export interface DoctorHarnessFacts {
  configRoot?: string;
  credential?: string;
  source?: string;
  sourceKind?: string;
  credentialsFile?: string;
  settingsEnv?: Record<string, string>;
}

/** One Cell's doctor facts: the harnesses entries plus the Cell's
 * execution-plane reason, or the probe error when the daemon did not
 * answer. */
export interface DoctorCellFacts {
  profile: string;
  executionDisabled?: string;
  probeError?: string;
  source?: string;
  sourceKind?: string;
  harnesses?: Record<string, DoctorHarnessFacts>;
}

/** The capability slice the Cell rows need. */
export interface HarnessCapabilityFacts {
  id: string;
  credentialsFollowConfigRoot: boolean;
}

/** One pi step-status check of a model-credential source dir. */
export interface PiStepCheck {
  provider?: string | null;
  checkedAt?: string;
  basis?: "pi-auth-check-no-refresh" | "provider-selection";
  sourceDir: string;
  status: "ready" | "not_ready" | "unknown";
  authType: string | null;
  reason: string | null;
}

/** One harness's row of one Cell. */
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
  provider: string | null;
  checkedAt: string | null;
  checkBasis: "pi-auth-check-no-refresh" | "provider-selection" | "not-checked";
  observation: "observed" | "not-read" | "read-failed";
  modelUsable: boolean | null;
  usable: boolean | null;
  reason: string | null;
}

/** One Cell's row: every harness the page shows. */
export interface AccountCellRow {
  profile: string;
  executionDisabled: string | null;
  probeError: string | null;
  harnessOrder: string[];
  harnesses: Record<string, AccountHarnessRow>;
}
