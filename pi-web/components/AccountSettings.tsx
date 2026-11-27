"use client";

// The account tab (polyforge#142, 85a2 AC1/AC6/AC7): the left column lists
// the harnesses (pi, Claude), the right column shows the interactive
// session card and one daemon-step row per Cell — 状态、来源含「共享
// default」、配置根、凭据是否跟随配置目录（the keychain hint, AC7）、步骤能
// 否使用及原因 — all read through the account route (one cached doctor
// read, Q8). Claude's pane adds the credential form (AC6: the value lands
// in the native env block of the interactive session and the checked
// Cells; 沿用交互会话的值 is a server-side copy, the value never passes
// through the browser) and the 「打开终端」 buttons (AC3): a declared login
// command runs in a one-shot terminal embedded here (Q5: not a workspace
// shell, never in the restore list, gone when it ends). No credential
// value is ever displayed: the route answers set/unset only.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "@/hooks/useI18n";
import type { AccountCellRow, AccountHarnessRow } from "@/lib/harness/account-rows";
import { accountTargetName } from "@/lib/settings-display-names";
import { serverErrorText } from "@/lib/server-error";
import type { HarnessLoginCommand } from "@/lib/harness/concepts";
import {
  ConfigButton,
  ConfigDetail,
  ConfigDetailActions,
  ConfigErrorNotice,
  ConfigDetailHeader,
  ConfigDetailHeaderInfo,
  ConfigDetailTitle,
  ConfigEmptyState,
  ConfigPanelShell,
  ConfigSkeleton,
} from "./SettingsUi";
import { TerminalPanel } from "./TerminalPanel";
import { credentialDraftKey, matchesCredentialDraft, retainCredentialDrafts, settleCredentialDrafts, type HeldCredentialDraft, type HeldCredentialDrafts } from "@/lib/credential-drafts";
import { isClaudeGatewayUrl } from "@/lib/claude-api-config";
import { useSettingsDraft } from "./SettingsDrafts";
import { useAccountLogin } from "@/hooks/useAccountLogin";
import { PiAccountLogin } from "./PiAccountLogin";

/** What GET /api/settings/account answers (the route's own shape). */
export interface AccountRead {
  claude: {
    available: boolean;
    loggedIn: boolean | null;
    authMethod: string | null;
    configDirectory: string | null;
    reason?: string;
    envCredentials: Record<string, boolean | null>;
    envCredentialsState: "observed" | "absent" | "read-failed";
    externalEnvCredentialKeys: string[];
    /** Q3: a Console login under ~/.config/anthropic, stat only. */
    consoleProfile?: "present" | "absent";
  } | null;
  cells: AccountCellRow[];
  doctorError: string | null;
}

export function hasClaudeCredential(claude: AccountRead["claude"] | undefined): boolean {
  return ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"].some((key) => claude?.envCredentials?.[key] === true);
}

/** The capability slice the account tab needs (GET /api/harness). */
export interface AccountHarnessCap {
  id: string;
  loginCommands: readonly HarnessLoginCommand[];
  credentialsFollowConfigRoot: boolean;
}

/** The credential write kinds of the claude form (Q3). */
const CREDENTIAL_KINDS = [
  { id: "oauth-token", labelKey: "settings.accountKindOauthToken" },
  { id: "api-key", labelKey: "settings.accountKindApiKey" },
  { id: "gateway", labelKey: "settings.accountKindGateway" },
] as const;
type CredentialKind = (typeof CREDENTIAL_KINDS)[number]["id"];

/** The source-kind label keys, one fact here. */
const SOURCE_KIND_KEYS: Record<string, string> = {
  operator: "settings.accountSourceOperator",
  "shared-default": "settings.accountSourceSharedDefault",
  directory: "settings.accountSourceDirectory",
  none: "settings.accountSourceNone",
};

/** The credential words the rows show, from the route's set/unset facts. */
function credentialText(t: (key: string) => string, row: AccountHarnessRow): string {
  if (row.credential === "set") return t("settings.accountCredentialSet");
  if (row.credential === "unset") return t("settings.accountCredentialUnset");
  return t("settings.accountCredentialUnknown");
}

/** One harness's interactive card + the per-Cell rows. */
export function AccountHarnessPane({ harness, read, onRefresh, loading = false }: {
  harness: "pi" | "claude";
  read: AccountRead | null;
  onRefresh?: () => void;
  loading?: boolean;
}) {
  const { t } = useI18n();
  const rows = useMemo(
    () => (read?.cells ?? []).map((cell) => ({ cell, row: cell.harnesses[harness] ?? null })),
    [read, harness],
  );
  return (
    <div data-account-pane={harness} className="settings-general-pane-host">
      <section className="settings-general-section" data-account-card="cells">
        <h3>{harness} · {t("cleanup.taskAuthFacts")}</h3>
        {loading && <p role="status">{t("i18n.loading")}</p>}
        {read?.doctorError && (
          <ConfigErrorNotice summary={t("settings.accountDoctorError", { error: read.doctorError })} />
        )}
        {read && read.cells.length === 0 && !read.doctorError && (
          <ConfigEmptyState action={onRefresh ? (
            <ConfigButton data-account-action="refresh-cells" onClick={onRefresh}>{t("settings.accountRefresh")}</ConfigButton>
          ) : undefined}>
            {t("settings.accountNoCells")}
          </ConfigEmptyState>
        )}
        {rows.map(({ cell, row }) => (
          row === null ? null : (
            <div key={cell.profile} data-account-cell={cell.profile} className="settings-account-cell-row">
              <div className="settings-account-cell-head">
                <strong>{cell.profile}</strong>
                {row.sourceKind && (
                  <span className="settings-account-cell-source" data-source-kind={row.sourceKind}>
                    {t("settings.accountSource")}: {t(SOURCE_KIND_KEYS[row.sourceKind] ?? "settings.accountSourceUnknown")}{row.source ? ` (${row.source})` : ""}
                  </span>
                )}
              </div>
              <dl className="settings-account-cell-facts">
                <div><dt>{t("settings.accountStatus")}</dt><dd data-account-field="status">{credentialText(t, row)}{row.authType ? ` · ${row.authType}` : ""}</dd></div>
                <div><dt>{t("settings.accountConfigRoot")}</dt><dd data-account-field="config-root"><details><summary>{t("i18n.detailsLabel")}</summary>{row.configRoot ?? "—"}</details></dd></div>
                <div><dt>{t("cleanup.modelVerification")}</dt><dd data-account-field="usable">{cell.executionDisabled ? t("cleanup.executionDisabled") : t("cleanup.modelNotVerified")}</dd></div>
                {harness === "pi" && <div><dt>{t("cleanup.providerCheck")}</dt><dd data-account-field="provider-check">{row.provider ?? t("cleanup.notConfirmed")} · {t(row.status === "ready" ? "cleanup.providerCheckReady" : row.status === "not_ready" ? "cleanup.providerCheckFailed" : "cleanup.notConfirmed")}</dd></div>}
                {row.checkedAt && <div><dt>{t("cleanup.checkedAt")}</dt><dd>{row.checkedAt}</dd></div>}
                <div><dt>{t("cleanup.checkBasis")}</dt><dd>{row.checkBasis ?? "not-checked"}</dd></div>
                {row.observation !== "observed" && <div><dt>{t("cleanup.observation")}</dt><dd>{t(row.observation === "read-failed" ? "cleanup.checkReadFailed" : "cleanup.notConfirmed")}</dd></div>}
                {(row.reason || cell.executionDisabled) && (
                  <div>
                    <dt>{t("settings.accountReason")}</dt>
                    <dd data-account-field="reason">
                      {/* The doctor's own English stays folded (polyforge#259 T4 AC2). */}
                      <details className="settings-reason-details">
                        <summary>{t("settings.accountReasonSeeDetails")}</summary>
                        <span className="settings-reason-original">{row.reason ?? cell.executionDisabled}</span>
                      </details>
                    </dd>
                  </div>
                )}
                {row.credentialsFile === "present" && (
                  <div><dt>{t("settings.accountResidualOauthTitle")}</dt><dd>{t("settings.accountResidualOauth")}</dd></div>
                )}
              </dl>
              {harness === "pi" && row.source && (row.sourceKind === "operator" || row.sourceKind === "shared-default") && (
                <p className="settings-account-pi-guidance" data-pi-step-guidance={row.sourceKind}>
                  {t("settings.accountPiStepGuidanceShared", { source: row.source })}
                </p>
              )}
              {harness === "pi" && row.source && row.sourceKind === "directory" && (
                <p className="settings-account-pi-guidance" data-pi-step-guidance="directory">
                  {t("settings.accountPiStepGuidanceOwn", { source: row.source })}
                </p>
              )}
              {harness === "claude" && row.credential === "unset" && read?.claude?.consoleProfile === "present" && (
                <p className="settings-account-console-fallback" data-console-fallback="true">{t("settings.accountConsoleFallback")}</p>
              )}
              {row.keychainHint && <p className="settings-account-keychain-hint" data-keychain-hint="true">{t("settings.accountKeychainHint")}</p>}
            </div>
          )
        ))}
      </section>
    </div>
  );
}

/** The interactive claude card: the auth status and the env credential kinds.
 *  `loaded` separates a read still in flight (「正在读取…」) from a finished
 *  read that answered no claude state (「已完成但不可用」) — polyforge#256 B3. */
export function AccountClaudeCard({ claude, loaded = true }: { claude: AccountRead["claude"]; loaded?: boolean }) {
  const { t } = useI18n();
  if (claude === null) {
    if (!loaded) {
      return (
        <div data-claude-status="loading">
          <ConfigSkeleton label={t("settings.accountLoading")} lines={2} />
        </div>
      );
    }
    return <p className="settings-general-description" data-claude-status="absent">{t("settings.accountClaudeAbsent")}</p>;
  }
  if (!claude.available) {
    return <p className="settings-general-description" data-claude-status="unavailable">{t("settings.accountClaudeUnavailable", { reason: claude.reason ?? "" })}</p>;
  }
  const kinds = claude.envCredentials ?? {};
  return (
    <dl className="settings-account-cell-facts" data-claude-status={claude.loggedIn ? "logged-in" : "logged-out"}>
      <div><dt>{t("settings.accountStatus")}</dt><dd>{t(claude.loggedIn === true ? "models.connectionSignedIn" : hasClaudeCredential(claude) ? "models.connectionKeySaved" : claude.loggedIn === false ? "models.connectionNotSignedIn" : "models.connectionUnknown")}</dd></div>
      <div><dt>{t("settings.accountAuthMethod")}</dt><dd>{claude.authMethod ?? "—"}</dd></div>
      <div><dt>{t("settings.accountConfigRoot")}</dt><dd><details><summary>{t("i18n.detailsLabel")}</summary>{claude.configDirectory ?? "—"}</details></dd></div>
      {claude.consoleProfile === "present" && (
        <div><dt>{t("settings.accountConsoleProfile")}</dt><dd data-console-profile="present">{t("settings.accountConsolePresent")}</dd></div>
      )}
      <div><dt>{t("settings.accountCredentialKinds")}</dt><dd data-account-field="credential-kinds"><details><summary>{t("i18n.detailsLabel")}</summary>
        {["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN"]
          .filter((key) => kinds[key])
          .map((key) => <code key={key}>{key}</code>)
          .concat(kinds && Object.values(kinds).some(Boolean) ? [] : [<span key="none">—</span>])}
      </details></dd></div>
    </dl>
  );
}

/** The claude credential form (AC6). Pure render: the shell posts. */
export function ClaudeCredentialForm({ cells, onSave, onReveal, purpose = "tasks", onSetupToken }: {
  purpose?: "interactive" | "tasks";
  onSetupToken?: () => void;
  onReveal?: () => void;
  cells: AccountCellRow[];
  onSave?: (body: Record<string, unknown>) => Promise<CredentialWriteResult[] | null> | void;
}) {
  const { t } = useI18n();
  const [kind, setKind] = useState<CredentialKind>("api-key");
  const [secrets, setSecrets] = useState<Record<CredentialKind, { value: string; baseUrl: string }>>({ "api-key": { value: "", baseUrl: "" }, gateway: { value: "", baseUrl: "" }, "oauth-token": { value: "", baseUrl: "" } });
  const { value, baseUrl } = secrets[kind];
  const setValue = (next: string) => setSecrets((current) => ({ ...current, [kind]: { ...current[kind], value: next } }));
  const setBaseUrl = (next: string) => setSecrets((current) => ({ ...current, [kind]: { ...current[kind], baseUrl: next } }));
  const [useInteractive, setUseInteractive] = useState(false);
  const [targets, setTargets] = useState<string[]>([]);
  const allTargets = useMemo(() => cells.map((cell) => cell.profile).filter((profile) => profile !== "interactive"), [cells]);
  const effective = purpose === "interactive" ? ["interactive"] : targets;
  const canSave = effective.length > 0 && (purpose === "interactive" || effective.every((target) => allTargets.includes(target))) && (useInteractive || Boolean(value.trim())) && (kind !== "gateway" || useInteractive || isClaudeGatewayUrl(baseUrl));
  const [held, setHeld] = useState<HeldCredentialDrafts>({});
  const [writeUnconfirmed, setWriteUnconfirmed] = useState(false);
  const savingRef = useRef(false);
  const [credentialSaving, setCredentialSaving] = useState(false);
  const attempt = { kind, targets: effective, value, baseUrl, useInteractive };
  const save = async () => {
    if (savingRef.current || !canSave) return;
    savingRef.current = true;
    setWriteUnconfirmed(false);
    const submitted = { ...attempt, targets: [...effective] };
    const body: Record<string, unknown> = { kind, targets: [...effective] };
    if (useInteractive) body.useInteractive = true;
    else body.value = value;
    if (kind === "gateway") body.baseUrl = baseUrl;
    setCredentialSaving(true);
    try {
      const results = await onSave?.(body);
      setHeld((current) => settleCredentialDrafts(current, submitted, results ?? []));
      if (results) {
        const failed = effective.filter((target) => !results.some((result) => result.target === target && result.status === "written"));
        if (purpose === "tasks") setTargets(failed);
        if (failed.length === 0) setSecrets((current) => ({ ...current, [kind]: { value: "", baseUrl: "" } }));
      }
    } catch {
      setHeld((current) => settleCredentialDrafts(current, submitted));
      setWriteUnconfirmed(true);
    } finally { savingRef.current = false; setCredentialSaving(false); }
  };
  const restoreHeld = (key: string, draft: HeldCredentialDraft) => {
    setHeld((current) => retainCredentialDrafts(current, attempt, key));
    setKind(draft.kind);
    setSecrets((current) => ({ ...current, [draft.kind]: { value: draft.value, baseUrl: draft.baseUrl } }));
    setUseInteractive(draft.useInteractive);
    if (purpose === "tasks") setTargets([draft.target]);
  };
  const discardHeld = (key: string, draft: HeldCredentialDraft) => {
    setHeld((current) => { const next = { ...current }; delete next[key]; return next; });
    if (matchesCredentialDraft(draft, attempt)) {
      const remaining = effective.filter((target) => credentialDraftKey(target, kind) !== key);
      if (purpose === "tasks") setTargets(remaining);
      if (remaining.length === 0) {
        setSecrets((current) => ({ ...current, [kind]: { value: "", baseUrl: "" } }));
        setUseInteractive(false);
      }
    }
  };
  const targetPicker = purpose === "tasks" ? <fieldset className="settings-account-targets">
    <legend>{t("cleanup.taskAuthTargets")}</legend><p className="settings-general-description">{t("settings.accountSharedDefaultNote")}</p>
    {allTargets.map((target) => { const name = accountTargetName(target); return <label key={target} className="settings-account-checkbox"><input type="checkbox" data-account-target={target} checked={targets.includes(target)} onChange={(event) => setTargets(event.target.checked ? [...targets, target] : targets.filter((entry) => entry !== target))} /><span>{name.labelKey ? t(name.labelKey, name.params) : name.id}</span></label>; })}
    {cells.some((cell) => cell.profile === "interactive") && <p role="note">{t("cleanup.reservedTaskTarget")}</p>}
    {allTargets.length === 0 && <p>{t("cleanup.noTaskTargets")}</p>}
  </fieldset> : null;
  const managed = useSettingsDraft({ label: t(purpose === "interactive" ? "cleanup.configureApi" : "cleanup.taskAuthTitle"), reveal: onReveal, count: Object.values(secrets).filter((entry) => entry.value || entry.baseUrl).length + (useInteractive && effective.length ? 1 : 0) + Object.values(held).filter((draft) => !matchesCredentialDraft(draft, attempt)).length,
    busy: credentialSaving, canSave,
    save: () => void save(), discard: () => { setSecrets({ "api-key": { value: "", baseUrl: "" }, gateway: { value: "", baseUrl: "" }, "oauth-token": { value: "", baseUrl: "" } }); setUseInteractive(false); setHeld({}); if (purpose === "tasks") setTargets([]); } });
  return (
    <section className="settings-general-section" data-account-card="credential-form" data-auth-purpose={purpose}>
      <h3 className="settings-general-heading">{t(purpose === "interactive" ? "cleanup.configureApi" : "cleanup.taskAuthTitle")}</h3>
      <p className="settings-general-description">{t(purpose === "interactive" ? "cleanup.apiScope" : "cleanup.taskAuthScope")}</p>
      <p className="settings-general-description">{t("cleanup.additiveAuth")}</p>
      {writeUnconfirmed && <ConfigErrorNotice summary={t("cleanup.credentialWriteUnconfirmed")} />}
      {purpose === "tasks" && effective.some((target) => !allTargets.includes(target)) && <ConfigErrorNotice summary={t("cleanup.credentialTargetMissing")} />}
      <fieldset disabled={credentialSaving} className="settings-account-form">
        {targetPicker}
        <label>
          <span>{t("settings.accountCredentialKind")}</span>
          <select data-account-field="kind" value={kind} onChange={(event) => setKind(event.target.value as CredentialKind)}>
            {CREDENTIAL_KINDS.filter((entry) => purpose === "tasks" || entry.id !== "oauth-token").map((entry) => <option key={entry.id} value={entry.id}>{t(entry.labelKey)}</option>)}
          </select>
        </label>
        <label data-account-value={useInteractive ? "hidden" : "required"}>
          <span>{t(kind === "gateway" ? "cleanup.serviceToken" : "settings.accountCredentialValue")}</span>
          <input type="password" data-account-field="value" value={value} onChange={(event) => setValue(event.target.value)} disabled={useInteractive} autoComplete="off" />
        </label>
        {kind === "gateway" && (
          <label>
            <span>{t("settings.accountBaseUrl")}</span>
            <input type="url" data-account-field="base-url" value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} aria-required="true" />
            {!isClaudeGatewayUrl(baseUrl) && !useInteractive && <small>{t("cleanup.gatewayAddressRequired")}</small>}
          </label>
        )}
        {purpose === "tasks" && <label className="settings-account-checkbox">
          <input type="checkbox" data-account-field="use-interactive" checked={useInteractive} onChange={(event) => setUseInteractive(event.target.checked)} />
          <span>{t("settings.accountUseInteractive")}</span>
        </label>}
        {purpose === "tasks" && kind === "oauth-token" && onSetupToken && <div className="settings-purpose-panel"><p>{t("cleanup.setupTokenScope")}</p><ConfigButton onClick={onSetupToken}>{t("settings.accountClaudeSetupToken")}</ConfigButton></div>}
        {!managed && <ConfigDetailActions>
          <ConfigButton data-account-action="save" disabled={!canSave || credentialSaving} onClick={save}>{t("cleanup.saveApiConfiguration")}</ConfigButton>
        </ConfigDetailActions>}
      </fieldset>
      {Object.keys(held).length > 0 && <div className="settings-purpose-panel" data-held-credential-drafts=""><h4>{t("cleanup.heldCredentialDrafts")}</h4>
        {Object.entries(held).map(([key, draft]) => <div key={key} className="settings-purpose-actions" data-held-target={draft.target} data-held-kind={draft.kind}>
          <span>{draft.target === "interactive" ? t("settings.accountTargetInteractive") : `Cell ${draft.target}`} · {t(CREDENTIAL_KINDS.find((entry) => entry.id === draft.kind)!.labelKey)}</span>
          <ConfigButton disabled={credentialSaving || matchesCredentialDraft(draft, attempt)} onClick={() => restoreHeld(key, draft)}>{t("cleanup.restoreCredentialDraft")}</ConfigButton>
          <ConfigButton disabled={credentialSaving} onClick={() => discardHeld(key, draft)}>{t("settings.discard")}</ConfigButton>
          {draft.useInteractive && <small>{t("cleanup.retryCopyCurrent")}</small>}
        </div>)}
      </div>}
    </section>
  );
}

/** One account write's outcome for one target, as the route answers it: a
 *  refused target carries its reason (no value — file names and refusal words
 *  only) and its code (polyforge#259 T4, owner 2A). */
export interface CredentialWriteResult {
  target: string;
  status: string;
  reason?: string;
  code?: string;
}

/** One write outcome per target (polyforge#259 T4 AC2): a refused target reads
 *  「未写入」 plus the Chinese line its route code names; the server's own
 *  reason stays in the 详情 fold. `t` overrides the page's translator (tests). */
export function CredentialWriteResults({ results, t: tOverride }: { results: CredentialWriteResult[]; t?: (key: string, params?: Record<string, string | number>) => string }) {
  const i18n = useI18n();
  const t = tOverride ?? i18n.t;
  if (results.length === 0) return null;
  return (
    <ul className="settings-account-write-results">
      {results.map((entry) => (
        <li key={entry.target} data-write-target={entry.target} data-write-status={entry.status}>
          <strong>{entry.target === "interactive" ? t("settings.accountTargetInteractive") : `Cell ${entry.target}`}</strong>
          <span>{entry.status === "written" ? t("settings.accountWriteOk") : t("settings.accountWriteNo")}</span>
          {entry.status !== "written" && (entry.reason || entry.code) && (
            <span>{t(serverErrorText({ code: entry.code ?? null, message: entry.reason ?? null }).summaryKey)}</span>
          )}
          {entry.status !== "written" && entry.reason && (
            <details className="settings-reason-details">
              <summary>{t("i18n.detailsLabel")}</summary>
              <span className="settings-reason-original">{entry.reason}</span>
            </details>
          )}
        </li>
      ))}
    </ul>
  );
}

/** The account read's visible state (polyforge#256 B3/r1#2): the pending
 *  status while a read is in flight, the retry line once it failed. A
 *  presentational component, so the shell's two visible states are pinned by
 *  rendering rather than by reading the source. */
export function AccountReadStatus({ loading, readError, hasData = false, t, onRetry }: {
  loading: boolean;
  readError: string | null;
  /** A previous good read is on screen; a refresh keeps it and says it is stale. */
  hasData?: boolean;
  t: (key: string, params?: Record<string, string | number>) => string;
  onRetry?: () => void;
}) {
  if (loading && !hasData) {
    return (
      <div data-account-loading="" className="settings-general-description">
        <ConfigSkeleton label={t("settings.accountLoading")} lines={3} />
      </div>
    );
  }
  if (readError) {
    return (
      <div data-account-read-error="">
        <ConfigErrorNotice
          summary={hasData ? t("settings.accountStale") : t(readError)}
          action={onRetry ? <ConfigButton size="small" data-account-retry="" onClick={onRetry}>{t("i18n.retry")}</ConfigButton> : undefined}
        />
      </div>
    );
  }
  if (loading) return <p role="status" data-account-loading="" className="settings-general-description">{t("settings.accountLoading")}</p>;
  return null;
}

/** The account read's state (polyforge#256 B3): an explicit pending state
 *  (first read and refresh alike), the last good read kept across a refresh,
 *  a visible error the refresh button retries, and a request sequence that
 *  drops a late answer so an older read never overwrites a newer one. */
export interface AccountReadState {
  read: AccountRead | null;
  loading: boolean;
  readError: string | null;
  capabilities: Record<string, AccountHarnessCap>;
  load: () => void;
}

export function useAccountRead(active = true, scope: "interactive" | "cells" | "full" = "interactive"): AccountReadState {
  const [read, setRead] = useState<AccountRead | null>(null);
  const [loading, setLoading] = useState(true);
  const [readError, setReadError] = useState<string | null>(null);
  const requestRef = useRef(0);
  const mountedRef = useRef(true);
  const [capabilities, setCapabilities] = useState<Record<string, AccountHarnessCap>>({});
  const load = useCallback(() => {
    if (!mountedRef.current) return;
    const seq = ++requestRef.current;
    setLoading(true);
    setReadError(null);
    void (async () => {
      try {
        const response = await fetch(`/api/settings/account?scope=${scope}`);
        if (seq !== requestRef.current) return;
        if (response.ok) {
          const data = await response.json() as AccountRead;
          if (seq !== requestRef.current) return;
          setRead(data);
        } else {
          setReadError("settings.accountReadFailed");
        }
      } catch {
        if (seq === requestRef.current) setReadError("settings.accountReadFailed");
      } finally {
        if (seq === requestRef.current) setLoading(false);
      }
      if (seq !== requestRef.current) return;
      try {
        const response = await fetch("/api/harness");
        if (seq !== requestRef.current || !response.ok) return;
        const answer = await response.json() as { harnesses?: { id: string; capabilities?: { loginCommands?: HarnessLoginCommand[]; credentialsFollowConfigRoot?: boolean } }[] };
        if (seq !== requestRef.current) return;
        const caps: Record<string, AccountHarnessCap> = {};
        for (const entry of answer.harnesses ?? []) {
          caps[entry.id] = {
            id: entry.id,
            loginCommands: entry.capabilities?.loginCommands ?? [],
            credentialsFollowConfigRoot: entry.capabilities?.credentialsFollowConfigRoot ?? true,
          };
        }
        setCapabilities(caps);
      } catch { /* the panes degrade without the capability slice */ }
    })();
  }, [scope]);
  useEffect(() => {
    mountedRef.current = true;
    if (active) load();
    return () => { mountedRef.current = false; requestRef.current++; };
  }, [active, load]);
  return { read, loading, readError, capabilities, load };
}

/** The shell: the two panes over the route's read, plus the one-shot login
 * terminal (Q5) a declared login command opens. */
export function AccountSettings({ embedded = true, onOpenModels, selectedHarness = "claude", selectedConnection, cwd = null, onRevealCredentials, accountState, connectionLabel, onOpenConnections, onRevealConnection, purpose = "interactive", taskProfile, onManageModels, onTaskAuthentication }: { onRevealConnection?: (connection: { kind: "oauth" | "apikey"; id: string; label: string }) => void; purpose?: "interactive" | "tasks"; taskProfile?: string; onManageModels?: () => void; onTaskAuthentication?: () => void; onOpenConnections?: () => void; accountState?: AccountReadState; connectionLabel?: string; onRevealCredentials?: () => void; cwd?: string | null; embedded?: boolean; onOpenModels?: () => void; selectedHarness?: "pi" | "claude"; selectedConnection?: { kind: "oauth" | "apikey"; id: string } | null }) {
  const { t } = useI18n();
  const ownRead = useAccountRead(!accountState, purpose === "tasks" ? "cells" : "interactive");
  const { read, loading, readError, capabilities, load } = accountState ?? ownRead;
  const pane = selectedHarness;
  const [piVisited, setPiVisited] = useState(pane === "pi");
  const [piRefresh, setPiRefresh] = useState(0);
  useEffect(() => { if (pane === "pi") setPiVisited(true); }, [pane]);
  const login = useAccountLogin(load);
  const [view, setView] = useState<"connection" | "api" | "diagnostics">("connection");
  const [apiVisited, setApiVisited] = useState(false);
  const revealApi = () => { setApiVisited(true); setView("api"); onRevealCredentials?.(); };
  const claude = read?.claude;
  const apiConfigured = claude?.envCredentials?.ANTHROPIC_API_KEY === true || claude?.envCredentials?.ANTHROPIC_AUTH_TOKEN === true;
  const authKinds = [claude?.loggedIn === true, claude?.envCredentials?.ANTHROPIC_API_KEY === true, claude?.envCredentials?.ANTHROPIC_AUTH_TOKEN === true, claude?.envCredentials?.CLAUDE_CODE_OAUTH_TOKEN === true].filter(Boolean).length;
  const envObserved = claude?.envCredentialsState === "observed" || claude?.envCredentialsState === "absent";
  const commands = capabilities.claude?.loginCommands ?? [];
  const canLogin = commands.some((command) => command.id === "auth-login");
  const canSetupToken = commands.some((command) => command.id === "setup-token");
  const taskRead = read ? { ...read, cells: taskProfile ? read.cells.filter((cell) => cell.profile === taskProfile) : read.cells } : null;

  const [writeResults, setWriteResults] = useState<CredentialWriteResult[] | null>(null);
  // review #10: the failure line carries its own split — a localized summary and
  // the server's words for the fold — never a translated string re-inspected.
  const [writeNote, setWriteNote] = useState<{ summary: string; details: string | null } | null>(null);
  const saveCredentials = useCallback(async (body: Record<string, unknown>): Promise<CredentialWriteResult[] | null> => {
      try {
        const response = await fetch("/api/settings/account", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify(body),
        });
        const answer = await response.json() as { results?: CredentialWriteResult[]; message?: string; code?: string; params?: Record<string, string | number> };
        if (response.ok && answer.results) {
          setWriteResults(answer.results);
          setWriteNote(null);
          // the rows and the card read again: what was just written shows now
          load();
          return answer.results;
        } else {
          setWriteResults(null);
          const failure = serverErrorText(answer.code ? { code: answer.code, params: answer.params, message: answer.message } : answer.message);
          setWriteNote({ summary: t(failure.summaryKey, failure.summaryParams), details: failure.details });
        }
      } catch {
        setWriteResults(null);
        setWriteNote({ summary: t(serverErrorText(null).summaryKey), details: null });
      }
      return null;
  }, [t, load]);

  return (
    <ConfigPanelShell embedded={embedded} title={t(purpose === "tasks" ? "cleanup.taskAuthTitle" : "settings.tabAccount")} onClose={() => {}}>
      <div className="settings-account-flat" data-auth-context={purpose}>
        <ConfigDetail>
          {onOpenConnections && <ConfigButton variant="ghost" data-account-back="" onClick={pane === "claude" && view !== "connection" ? () => setView("connection") : onOpenConnections}>← {t(pane === "claude" && view !== "connection" || purpose === "tasks" ? "cleanup.backToConnection" : "models.connectionsTitle")}</ConfigButton>}
          <ConfigDetailHeader>
            <ConfigDetailHeaderInfo><ConfigDetailTitle>{purpose === "tasks" ? `${t("cleanup.taskAuthTitle")}${taskProfile ? ` · ${taskProfile}` : ""}` : pane === "pi" ? connectionLabel ?? "pi" : "Claude Code"}</ConfigDetailTitle></ConfigDetailHeaderInfo>
            <ConfigDetailActions><ConfigButton data-account-action="refresh" disabled={pane === "claude" && loading} onClick={() => pane === "pi" && purpose === "interactive" ? setPiRefresh((value) => value + 1) : load()}>{t("settings.accountRefresh")}</ConfigButton></ConfigDetailActions>
          </ConfigDetailHeader>
          {(purpose === "tasks" || pane === "claude") && <AccountReadStatus loading={loading} readError={readError} hasData={read !== null} t={t} onRetry={load} />}
          {purpose === "interactive" && piVisited && <div hidden={pane !== "pi"}><PiAccountLogin onRevealConnection={onRevealConnection} refreshRequest={piRefresh} cwd={cwd} onBack={onOpenConnections} onOpenModels={onOpenModels} selectedConnection={selectedConnection} /></div>}
          {purpose === "interactive" && pane === "claude" && view === "connection" && <section className="settings-purpose-panel" data-claude-connection-home="">
            <p>{t("cleanup.currentClaudeScope")}</p>
            <dl className="settings-account-cell-facts">
              <div><dt>{t("cleanup.accountLoginState")}</dt><dd data-claude-login-fact="">{t(claude?.loggedIn === true ? "models.connectionSignedIn" : claude?.loggedIn === false ? "models.connectionNotSignedIn" : "cleanup.notConfirmed")}</dd></div>
              <div><dt>{t("cleanup.apiConfigurationState")}</dt><dd>{t(apiConfigured ? "models.connectionKeySaved" : envObserved ? "cleanup.confirmedNotConfigured" : "cleanup.notConfirmed")}</dd></div>
            </dl>
            {authKinds > 1 && <p role="status">{t("cleanup.multipleAuthConfigurations")}</p>}
            {Boolean(claude?.externalEnvCredentialKeys?.length) && <p role="note">{t("cleanup.externalAuthConfiguration")}</p>}
            <div className="settings-purpose-actions">
              <ConfigButton variant="primary" data-login-command="auth-login" disabled={!canLogin || login.opening || Boolean(login.terminal)} onClick={() => void login.open("auth-login")}>{t(login.opening ? "cleanup.openingLogin" : claude?.loggedIn === true ? "cleanup.reloginClaude" : "cleanup.loginClaude")}</ConfigButton>
              <ConfigButton data-configure-claude-api="" onClick={revealApi}>{t(apiConfigured ? "cleanup.changeApi" : "cleanup.configureApi")}</ConfigButton>
            </div>
            {!canLogin && <p className="settings-general-description">{t("cleanup.loginNotReported")}</p>}
          </section>}
          {purpose === "interactive" && apiVisited && <div hidden={pane !== "claude" || view !== "api"}>
            <ClaudeCredentialForm purpose="interactive" cells={[]} onSave={saveCredentials} onReveal={revealApi} />
          </div>}
          {purpose === "tasks" && <div hidden={view !== "connection"}>
            <p>{t("cleanup.taskIsolationBoundary")}</p>
            {read?.doctorError && <ConfigErrorNotice summary={t("cleanup.checkReadFailed")} details={read.doctorError} detailsLabel={t("i18n.detailsLabel")} />}
            <ClaudeCredentialForm purpose="tasks" cells={taskRead?.cells ?? []} onSave={saveCredentials} onReveal={() => { setView("connection"); onRevealCredentials?.(); }} onSetupToken={canSetupToken && !login.opening && !login.terminal ? () => void login.open("setup-token") : undefined} />
          </div>}
          {view === "diagnostics" && (purpose === "tasks" || pane === "claude") && <section className="settings-purpose-panel" data-account-troubleshooting="">
            <h3>{t("cleanup.connectionTroubleshooting")}</h3>
            {purpose === "interactive" ? <><AccountClaudeCard claude={claude ?? null} loaded={!loading} /><p>{t("cleanup.envObservation")}: {t(envObserved ? "cleanup.observed" : "cleanup.notConfirmed")}</p><p>{t("cleanup.externalAuthKeys")}: {claude?.externalEnvCredentialKeys?.join(", ") || "—"}</p>{claude?.reason && <pre>{claude.reason}</pre>}</> : <>{(["claude", "pi"] as const).map((harness) => <AccountHarnessPane key={harness} harness={harness} read={taskRead} loading={loading} onRefresh={load} />)}</>}
          </section>}
          <div className="settings-purpose-actions">
            {purpose === "interactive" && onManageModels && <ConfigButton data-manage-connection-models="" onClick={onManageModels}>{t(pane === "pi" ? "flow.connectionModels" : "cleanup.manageModels")}</ConfigButton>}
            {purpose === "interactive" && pane === "claude" && onTaskAuthentication && <ConfigButton data-open-task-auth="" onClick={onTaskAuthentication}>{t("cleanup.openTaskAuth")}</ConfigButton>}
            {(purpose === "tasks" || pane === "claude") && <ConfigButton data-account-diagnostics="" onClick={() => setView("diagnostics")}>{t("cleanup.connectionTroubleshooting")}</ConfigButton>}
          </div>
          <div hidden={purpose === "interactive" && pane !== "claude"}>
          {login.opening && <p role="status">{t("cleanup.openingLogin")}</p>}
          {login.error && <ConfigErrorNotice summary={t("cleanup.loginOpenFailed")} details={login.error.startsWith("cleanup.") ? t(login.error) : login.error} detailsLabel={t("i18n.detailsLabel")} action={<ConfigButton disabled={login.opening || Boolean(login.terminal)} onClick={() => void login.open(purpose === "tasks" ? "setup-token" : "auth-login")}>{t("i18n.retry")}</ConfigButton>} />}
          {login.checkedAfterExit && <p role="status">{t("cleanup.loginStatusReread")}</p>}
          {writeResults && <CredentialWriteResults results={writeResults} />}
          {writeNote && <ConfigErrorNotice summary={writeNote.summary} detailsLabel={t("i18n.detailsLabel")} details={writeNote.details} />}
          {login.terminal && <div data-account-login-terminal="" className="settings-account-login-flow">
            <div className="settings-account-login-terminal"><TerminalPanel tab={{ id: login.terminal.id, cwd: "", restored: true, ...(login.terminal.closing ? { closing: login.terminal.closing } : {}) }} active={pane === "claude"}
              onRestart={() => void login.restart()} onExited={() => login.completed(login.terminal!.id)} onClosed={() => login.closed(login.terminal!.id)} onCloseError={() => login.closeFailed(login.terminal!.id)} /></div>
            <ConfigButton onClick={() => { load(); }}>{t("cleanup.loginCompletedRead")}</ConfigButton><ConfigButton onClick={login.close}>{t("settings.accountCloseTerminal")}</ConfigButton>
          </div>}
          </div>
        </ConfigDetail>
      </div>
    </ConfigPanelShell>
  );
}
