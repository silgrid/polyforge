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
  ConfigSidebar,
  ConfigSidebarItem,
  ConfigSidebarList,
  ConfigSplitView,
} from "./SettingsUi";
import { TerminalPanel } from "./TerminalPanel";
import { PiAccountLogin } from "./PiAccountLogin";

/** What GET /api/settings/account answers (the route's own shape). */
export interface AccountRead {
  claude: {
    available: boolean;
    loggedIn: boolean | null;
    authMethod: string | null;
    configDirectory: string | null;
    reason?: string;
    envCredentials: Record<string, boolean>;
    /** Q3: a Console login under ~/.config/anthropic, stat only. */
    consoleProfile?: "present" | "absent";
  } | null;
  cells: AccountCellRow[];
  doctorError: string | null;
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
export function AccountHarnessPane({ harness, read, capabilities, onOpenLoginTerminal, onSaveCredentials, onRefresh, onOpenModels, loading = false }: {
  harness: "pi" | "claude";
  read: AccountRead | null;
  capabilities: AccountHarnessCap | null;
  onOpenLoginTerminal: (id: string) => void;
  onSaveCredentials?: (body: Record<string, unknown>) => void;
  /** The empty Cell list's refresh action (polyforge#271 AC1). */
  onRefresh?: () => void;
  /** The account page's way to the Models page for custom models.json sources (Q2 A). */
  onOpenModels?: () => void;
  /** The read is still in flight (polyforge#256 r1#1): a failed read is
   *  finished, never still "reading". */
  loading?: boolean;
}) {
  const { t } = useI18n();
  const rows = useMemo(
    () => (read?.cells ?? []).map((cell) => ({ cell, row: cell.harnesses[harness] ?? null })),
    [read, harness],
  );
  const commands = capabilities?.loginCommands ?? [];
  return (
    <div data-account-pane={harness} className="settings-general-pane-host">
      <section className="settings-general-section" data-account-card="interactive">
        <h3 className="settings-general-heading">{t("settings.accountInteractiveSession")}</h3>
        {harness === "claude" ? <AccountClaudeCard claude={read?.claude ?? null} loaded={!loading} /> : <PiAccountLogin onOpenModels={onOpenModels} />}
        {commands.length > 0 && (
          <ConfigDetailActions>
            {commands.map((command) => (
              <ConfigButton key={command.id} data-login-command={command.id} onClick={() => onOpenLoginTerminal(command.id)}>
                {command.id === "auth-login" ? t("settings.accountClaudeLogin") : t("settings.accountClaudeSetupToken")}
              </ConfigButton>
            ))}
          </ConfigDetailActions>
        )}
      </section>
      <section className="settings-general-section" data-account-card="cells">
        <h3 className="settings-general-heading">{t("settings.accountCellRows")}</h3>
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
                <div><dt>{t("settings.accountStatus")}</dt><dd data-account-field="status">{credentialText(t, row)}{row.status === "ready" ? ` · ${t("settings.accountCheckReady")}` : ""}{row.authType ? ` · ${row.authType}` : ""}</dd></div>
                <div><dt>{t("settings.accountConfigRoot")}</dt><dd data-account-field="config-root">{row.configRoot ?? "—"}</dd></div>
                <div><dt>{t("settings.accountUsable")}</dt><dd data-account-field="usable">{row.usable ? t("settings.accountUsableYes") : t("settings.accountUsableNo")}</dd></div>
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
      {harness === "claude" && <ClaudeCredentialForm cells={read?.cells ?? []} onSave={onSaveCredentials} />}
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
      <div><dt>{t("settings.accountStatus")}</dt><dd>{claude.loggedIn ? t("settings.accountCredentialSet") : t("settings.accountCredentialUnset")}</dd></div>
      <div><dt>{t("settings.accountAuthMethod")}</dt><dd>{claude.authMethod ?? "—"}</dd></div>
      <div><dt>{t("settings.accountConfigRoot")}</dt><dd>{claude.configDirectory ?? "—"}</dd></div>
      {claude.consoleProfile === "present" && (
        <div><dt>{t("settings.accountConsoleProfile")}</dt><dd data-console-profile="present">{t("settings.accountConsolePresent")}</dd></div>
      )}
      <div><dt>{t("settings.accountCredentialKinds")}</dt><dd data-account-field="credential-kinds">
        {["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN"]
          .filter((key) => kinds[key])
          .map((key) => <code key={key}>{key}</code>)
          .concat(kinds && Object.values(kinds).some(Boolean) ? [] : [<span key="none">—</span>])}
      </dd></div>
    </dl>
  );
}

/** The claude credential form (AC6). Pure render: the shell posts. */
export function ClaudeCredentialForm({ cells, onSave }: {
  cells: AccountCellRow[];
  onSave?: (body: Record<string, unknown>) => void;
}) {
  const { t } = useI18n();
  const [kind, setKind] = useState<CredentialKind>("api-key");
  const [value, setValue] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [useInteractive, setUseInteractive] = useState(false);
  const [targets, setTargets] = useState<string[]>([]);
  const allTargets = useMemo(() => ["interactive", ...cells.map((cell) => cell.profile)], [cells]);
  const effective = targets.length > 0 ? targets : allTargets;
  const save = useCallback(() => {
    const body: Record<string, unknown> = { kind, targets: effective };
    if (useInteractive) body.useInteractive = true;
    else body.value = value;
    if (kind === "gateway") body.baseUrl = baseUrl;
    onSave?.(body);
  }, [kind, effective, useInteractive, value, baseUrl, onSave]);
  return (
    <section className="settings-general-section" data-account-card="credential-form">
      <h3 className="settings-general-heading">{t("settings.accountCredentialForm")}</h3>
      <p className="settings-general-description">{t("settings.accountCredentialFormHint")}</p>
      <div className="settings-account-form">
        <label>
          <span>{t("settings.accountCredentialKind")}</span>
          <select data-account-field="kind" value={kind} onChange={(event) => setKind(event.target.value as CredentialKind)}>
            {CREDENTIAL_KINDS.map((entry) => <option key={entry.id} value={entry.id}>{t(entry.labelKey)}</option>)}
          </select>
        </label>
        <label data-account-value={useInteractive ? "hidden" : "required"}>
          <span>{t("settings.accountCredentialValue")}</span>
          <input type="password" data-account-field="value" value={value} onChange={(event) => setValue(event.target.value)} disabled={useInteractive} autoComplete="off" />
        </label>
        {kind === "gateway" && (
          <label>
            <span>{t("settings.accountBaseUrl")}</span>
            <input type="text" data-account-field="base-url" value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} />
          </label>
        )}
        <label className="settings-account-checkbox">
          <input type="checkbox" data-account-field="use-interactive" checked={useInteractive} onChange={(event) => setUseInteractive(event.target.checked)} />
          <span>{t("settings.accountUseInteractive")}</span>
        </label>
        <fieldset>
          <legend>{t("settings.accountTargets")}</legend>
          <p className="settings-general-description">{t("settings.accountSharedDefaultNote")}</p>
          {allTargets.map((target) => {
            // Display name first (`交互会话` / `Cell default`); the raw target id
            // stays as the second line (polyforge#259 T4 AC2).
            const name = accountTargetName(target);
            return (
              <label key={target} className="settings-account-checkbox">
                <input type="checkbox" data-account-target={target} checked={effective.includes(target)} onChange={(event) => {
                  setTargets(event.target.checked
                    ? [...effective, target]
                    : effective.filter((entry) => entry !== target));
                }} />
                <span className="settings-name-stack">
                  <span>{name.labelKey ? t(name.labelKey, name.params) : name.id}</span>
                  {name.labelKey && <code className="settings-secondary-id">{name.id}</code>}
                </span>
              </label>
            );
          })}
        </fieldset>
        <ConfigDetailActions>
          <ConfigButton data-account-action="save" onClick={save}>{t("settings.accountSaveCredential")}</ConfigButton>
        </ConfigDetailActions>
      </div>
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
          <strong>{entry.target}</strong>
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

export function useAccountRead(): AccountReadState {
  const [read, setRead] = useState<AccountRead | null>(null);
  const [loading, setLoading] = useState(true);
  const [readError, setReadError] = useState<string | null>(null);
  const requestRef = useRef(0);
  const [capabilities, setCapabilities] = useState<Record<string, AccountHarnessCap>>({});
  const load = useCallback(() => {
    const seq = ++requestRef.current;
    setLoading(true);
    setReadError(null);
    void (async () => {
      try {
        const response = await fetch("/api/settings/account");
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
  }, []);
  useEffect(() => { load(); }, [load]);
  return { read, loading, readError, capabilities, load };
}

/** The shell: the two panes over the route's read, plus the one-shot login
 * terminal (Q5) a declared login command opens. */
export function AccountSettings({ embedded = true, onOpenModels }: { embedded?: boolean; onOpenModels?: () => void }) {
  const { t } = useI18n();
  const { read, loading, readError, capabilities, load } = useAccountRead();
  const [pane, setPane] = useState<"pi" | "claude">("pi");
  const [loginTerminal, setLoginTerminal] = useState<{ id: string; commandId: string; closing?: "close" } | null>(null);

  const openLoginTerminal = useCallback((commandId: string) => {
    void (async () => {
      try {
        const response = await fetch("/api/terminal", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ login: { harness: pane, id: commandId } }),
        });
        if (response.ok) {
          const { id } = await response.json() as { id: string };
          setLoginTerminal({ id, commandId });
        }
      } catch { /* a failed open leaves the button as the retry */ }
    })();
  }, [pane]);

  const [writeResults, setWriteResults] = useState<CredentialWriteResult[] | null>(null);
  // review #10: the failure line carries its own split — a localized summary and
  // the server's words for the fold — never a translated string re-inspected.
  const [writeNote, setWriteNote] = useState<{ summary: string; details: string | null } | null>(null);
  const saveCredentials = useCallback((body: Record<string, unknown>) => {
    void (async () => {
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
        } else {
          setWriteResults(null);
          const failure = serverErrorText(answer.code ? { code: answer.code, params: answer.params, message: answer.message } : answer.message);
          setWriteNote({ summary: t(failure.summaryKey, failure.summaryParams), details: failure.details });
        }
      } catch {
        setWriteResults(null);
        setWriteNote({ summary: t(serverErrorText(null).summaryKey), details: null });
      }
    })();
  }, [t, load]);

  const panes = [
    { id: "pi" as const, labelKey: "settings.accountHarnessPi" },
    { id: "claude" as const, labelKey: "settings.accountHarnessClaude" },
  ];
  return (
    <ConfigPanelShell embedded={embedded} title={t("settings.tabAccount")} onClose={() => {}}>
      <ConfigSplitView>
        <ConfigSidebar>
          <ConfigSidebarList>
            {panes.map((item) => (
              <ConfigSidebarItem key={item.id} active={pane === item.id} onClick={() => setPane(item.id)}>
                <span className="config-sidebar-text">{t(item.labelKey)}</span>
              </ConfigSidebarItem>
            ))}
          </ConfigSidebarList>
        </ConfigSidebar>
        <ConfigDetail>
          <ConfigDetailHeader>
            <ConfigDetailHeaderInfo>
              <ConfigDetailTitle>{t(pane === "pi" ? "settings.accountHarnessPi" : "settings.accountHarnessClaude")}</ConfigDetailTitle>
            </ConfigDetailHeaderInfo>
            <ConfigDetailActions>
              <ConfigButton data-account-action="refresh" onClick={load}>{t("settings.accountRefresh")}</ConfigButton>
            </ConfigDetailActions>
          </ConfigDetailHeader>
          <AccountReadStatus loading={loading} readError={readError} hasData={read !== null} t={t} onRetry={load} />
          <AccountHarnessPane
            harness={pane}
            read={read}
            loading={loading}
            capabilities={capabilities[pane] ?? null}
            onOpenLoginTerminal={openLoginTerminal}
            onRefresh={load}
            onOpenModels={onOpenModels}
            onSaveCredentials={pane === "claude" ? saveCredentials : undefined}
          />
          {writeResults && <CredentialWriteResults results={writeResults} />}
          {writeNote && <ConfigErrorNotice summary={writeNote.summary} detailsLabel={t("i18n.detailsLabel")} details={writeNote.details} />}
          {loginTerminal && (
            <div data-account-login-terminal className="settings-account-login-terminal">
              {/* Attach, never create: the server already started this
                  one-shot terminal when the button was clicked, so the
                  panel connects to it (restored: true) — a create would be
                  refused by the route (the login terminal's id is not a
                  workspace id, and its cwd is the server's choice) and the
                  stream would never open (review r1#1) */}
              <TerminalPanel
                tab={{ id: loginTerminal.id, cwd: "", restored: true, ...(loginTerminal.closing ? { closing: loginTerminal.closing } : {}) }}
                active
                onRestart={() => {
                  // a one-shot login command: start a fresh one
                  setLoginTerminal(null);
                  openLoginTerminal(loginTerminal.commandId);
                }}
                onClosed={() => setLoginTerminal(null)}
                onCloseError={() => setLoginTerminal(null)}
              />
              <ConfigButton onClick={() => setLoginTerminal((current) => current ? { ...current, closing: "close" } : current)}>{t("settings.accountCloseTerminal")}</ConfigButton>
            </div>
          )}
        </ConfigDetail>
      </ConfigSplitView>
    </ConfigPanelShell>
  );
}
