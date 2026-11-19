"use client";

// The account tab (polyforge#142, 85a2) — placeholder: the test-first commit
// carries only the signatures so the suite's assertions run red; the
// implementation lands with the unit.
import { useI18n } from "@/hooks/useI18n";

export interface AccountRead {
  claude: {
    available: boolean;
    loggedIn: boolean | null;
    authMethod: string | null;
    configDirectory: string | null;
    reason?: string;
    envCredentials: Record<string, boolean>;
  } | null;
  cells: never[];
  doctorError: string | null;
}

export interface AccountHarnessCap {
  id: string;
  loginCommands: { id: string; argv: string[]; cell: boolean }[];
  credentialsFollowConfigRoot: boolean;
}

export function AccountHarnessPane(_props: { harness: string; read: unknown; capabilities: unknown; onOpenLoginTerminal: (id: string) => void; onSaveCredentials?: (body: Record<string, unknown>) => void }) {
  return null;
}

export function AccountClaudeCard(_props: { claude: AccountRead["claude"] }) {
  return null;
}

export function ClaudeCredentialForm(_props: { cells: never[]; onSave?: (body: Record<string, unknown>) => void }) {
  return null;
}

export function AccountSettings(_props: { embedded?: boolean }) {
  const { t } = useI18n();
  return <div className="settings-general harness-placeholder-page"><section className="settings-general-section"><h3 className="settings-general-heading">{t("settings.tabAccount")}</h3></section></div>;
}
