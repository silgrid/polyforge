"use client";

import { useEffect, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import { isClaudePermissionMode, type ClaudePermissionMode } from "@/lib/claude-shared";
import { ConfigSwitch } from "./SettingsUi";

/**
 * Settings › General's claude permission block (; docs/agents/settings-ui.md,
 * claude-sessions.md): the global permission mode — 「弹窗询问」 (claude's prompts as Pi Web
 * dialogs) or 「静默拒绝」 (claude denies them itself) — the pending-permission push switch,
 * and, when the open session is a claude session, that session's override (follow the
 * default / ask / deny). Everything goes through `/api/claude-permissions`; a value is shown
 * only after the server stored it. A busy session (409) or a refused value (400) keeps the
 * old value and shows why.
 */

interface ClaudePermissionState {
  mode: ClaudePermissionMode;
  push: boolean;
  session: { override: ClaudePermissionMode | null } | null;
}

type SessionChoice = ClaudePermissionMode | "default";

function parseState(data: unknown): ClaudePermissionState | null {
  if (!data || typeof data !== "object") return null;
  const record = data as Record<string, unknown>;
  if (!isClaudePermissionMode(record.mode) || typeof record.push !== "boolean") return null;
  const session = record.session && typeof record.session === "object"
    ? (record.session as Record<string, unknown>).override
    : undefined;
  return {
    mode: record.mode,
    push: record.push,
    session: session === undefined ? null : { override: isClaudePermissionMode(session) ? session : null },
  };
}

class SaveError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

async function putJson(body: Record<string, unknown>): Promise<Record<string, unknown>> {
  const response = await fetch("/api/claude-permissions", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({})) as Record<string, unknown>;
  if (!response.ok) throw new SaveError(response.status, typeof data.error === "string" ? data.error : `HTTP ${response.status}`);
  return data;
}

export function ClaudePermissionSettings({ sessionId }: { sessionId: string | null }) {
  const { t } = useI18n();
  const [state, setState] = useState<ClaudePermissionState | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const query = sessionId ? `?session=${encodeURIComponent(sessionId)}` : "";
    void fetch(`/api/claude-permissions${query}`)
      .then(async (response) => {
        const parsed = parseState(await response.json());
        if (!response.ok || !parsed) throw new Error(`HTTP ${response.status}`);
        if (cancelled) return;
        setState(parsed);
        setLoadFailed(false);
      })
      .catch(() => {
        if (!cancelled) setLoadFailed(true);
      });
    return () => { cancelled = true; };
  }, [sessionId]);

  const run = async (save: () => Promise<void>) => {
    setSaving(true);
    setSaveError(null);
    try {
      await save();
    } catch (error) {
      if (error instanceof SaveError && error.status === 409) setSaveError(t("settings.claudePermissionBusy"));
      else setSaveError(t("settings.claudePermissionSaveError", { error: error instanceof Error ? error.message : String(error) }));
    } finally {
      setSaving(false);
    }
  };

  const saveGlobal = (patch: { mode?: ClaudePermissionMode; push?: boolean }) => run(async () => {
    const data = await putJson(patch);
    if (!isClaudePermissionMode(data.mode) || typeof data.push !== "boolean") throw new Error("Unexpected response");
    const { mode, push } = data;
    setState((previous) => (previous ? { ...previous, mode, push } : previous));
  });

  const saveOverride = (choice: SessionChoice) => run(async () => {
    if (!sessionId) return;
    const data = await putJson({ session: sessionId, override: choice === "default" ? null : choice });
    const override = isClaudePermissionMode(data.override) ? data.override : null;
    setState((previous) => (previous ? { ...previous, session: { override } } : previous));
  });

  if (state === null) {
    if (!loadFailed) return null;
    return (
      <section className="settings-general-section">
        <h3 className="settings-general-heading">{t("settings.claudePermission")}</h3>
        <p role="alert" className="settings-general-error">{t("settings.claudePermissionLoadFailed")}</p>
      </section>
    );
  }

  const modeLabels: Record<ClaudePermissionMode, string> = {
    ask: t("settings.claudePermissionAsk"),
    deny: t("settings.claudePermissionDeny"),
  };
  const modeOptions: { id: ClaudePermissionMode; label: string }[] = [
    { id: "ask", label: modeLabels.ask },
    { id: "deny", label: modeLabels.deny },
  ];
  const sessionOptions: { id: SessionChoice; label: string }[] = [
    { id: "default", label: t("settings.claudePermissionFollow") },
    ...modeOptions,
  ];
  const sessionChoice: SessionChoice | null = state.session ? state.session.override ?? "default" : null;

  const radio = (key: string, label: string, selected: boolean, onPick: () => void) => (
    <button
      key={key}
      type="button"
      role="radio"
      aria-checked={selected}
      disabled={saving}
      onClick={() => { if (!selected) onPick(); }}
      className="settings-language-option"
    >
      <span className="settings-language-radio">
        {selected && <span className="settings-language-radio-dot" />}
      </span>
      <span className="settings-language-label">{label}</span>
    </button>
  );

  return (
    <section className="settings-general-section">
      <h3 className="settings-general-heading">{t("settings.claudePermission")}</h3>
      <p className="settings-general-description">{t("settings.claudePermissionDescription")}</p>
      <p className="settings-general-description">{t("settings.claudePermissionMode")}</p>
      <div role="radiogroup" aria-label={t("settings.claudePermissionMode")} className="settings-agent-language-options">
        {modeOptions.map((option) => radio(option.id, option.label, state.mode === option.id, () => void saveGlobal({ mode: option.id })))}
      </div>
      {sessionChoice !== null && (
        <>
          <p className="settings-general-description">{t("settings.claudePermissionSession")}</p>
          <div role="radiogroup" aria-label={t("settings.claudePermissionSession")} className="settings-agent-language-options">
            {sessionOptions.map((option) => radio(`session-${option.id}`, option.label, sessionChoice === option.id, () => void saveOverride(option.id)))}
          </div>
        </>
      )}
      <div className="settings-shell-option">
        <span>{t("settings.claudePermissionPush")}</span>
        <ConfigSwitch
          checked={state.push}
          loading={saving}
          label={t("settings.claudePermissionPush")}
          onChange={(enabled) => void saveGlobal({ push: enabled })}
        />
      </div>
      {saveError && <p role="alert" className="settings-general-error">{saveError}</p>}
    </section>
  );
}
