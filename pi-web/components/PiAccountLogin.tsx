"use client";

// pi's sign-in, sign-out and API-key entry (polyforge#142, 85a2) —
// placeholder: the test-first commit carries only the signatures so the
// suite's assertions run red; the implementation lands with the unit.
import { useI18n } from "@/hooks/useI18n";

export function PiAccountLogin() {
  const { t } = useI18n();
  return <div data-pi-account-login="pending">{t("settings.accountLoading")}</div>;
}

export function OAuthDetail(_props: { provider: { id: string; name: string; usesCallbackServer: boolean; loggedIn: boolean }; onRefresh: () => void }) {
  return null;
}

export function ApiKeyDetail(_props: { provider: { id: string; displayName: string; configured: boolean; modelCount: number }; onRefresh: () => void }) {
  return null;
}

export function AddProviderPicker(_props: { oauthProviders: unknown[]; apiKeyProviders: unknown[]; onSelectOAuth: (id: string) => void; onSelectApiKey: (id: string) => void; onClose: () => void }) {
  return null;
}
