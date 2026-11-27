"use client";

import { useI18n } from "@/hooks/useI18n";
import type { ModelConnection, ModelConnectionCatalog } from "./ModelsConfig";
import { hasClaudeCredential, type AccountReadState } from "./AccountSettings";
import { ConfigButton, ConfigEmptyState, ConfigErrorNotice, ConfigSkeleton } from "./SettingsUi";

/** Safe overview facts only. A saved definition or credential is not a connectivity probe. */
export function ConnectionOverview({ connections, catalog, account, claudeModelCount, onAdd, onSelect, onClaude, onModels }: {
  connections: ModelConnection[];
  catalog: ModelConnectionCatalog;
  account: AccountReadState;
  claudeModelCount: number | null;
  onAdd: () => void;
  onSelect: (connection: ModelConnection) => void;
  onClaude: () => void;
  onModels: () => void;
}) {
  const { t } = useI18n();
  const claude = account.read?.claude;
  const claudeKey = hasClaudeCredential(claude);
  const showClaude = claude?.loggedIn === true || claude?.loggedIn === null || claude?.envCredentialsState === "read-failed" || claudeKey || (claude?.available === true && claude.loggedIn !== false);
  const count = connections.length + (showClaude ? 1 : 0);
  const loading = catalog.loading || account.loading;
  const error = catalog.error ?? account.readError;
  const headings = [t("models.connectionName"), t("models.connectionAuth"), t("settings.accountStatus"), t("common.models"), t("models.connectionActions")];
  const rows = connections.map((connection) => ({
    key: `${connection.kind}:${connection.id}`,
    id: connection.id,
    label: connection.label,
    source: t(connection.kind === "provider" ? "models.sourceCustom" : "models.sourceNative"),
    auth: connection.kind === "oauth" ? "OAuth" : connection.kind === "apikey" ? "API Key" : t("models.customGateway"),
    status: t(connection.draft ? "models.connectionDraft" : connection.kind === "oauth" ? "models.connectionAuthorized" : connection.kind === "apikey" ? "models.connectionKeySaved" : "models.connectionDefined"),
    tone: connection.draft ? "pending" : connection.kind === "oauth" ? "ready" : "configured",
    modelCount: connection.modelCount ?? null,
    open: () => onSelect(connection),
  }));
  if (showClaude) rows.push({
    key: "harness:claude", id: "anthropic", label: "Claude Code", source: t("models.sourceClaude"),
    auth: t("models.claudeNativeAuth"),
    status: t(claude?.loggedIn === true ? "models.connectionSignedIn" : claudeKey ? "models.connectionKeySaved" : claude?.loggedIn === false ? "models.connectionNotSignedIn" : "models.connectionUnknown"),
    tone: claude?.loggedIn === true ? "ready" : claudeKey ? "configured" : "pending",
    modelCount: claudeModelCount, open: onClaude,
  });
  return <section className="settings-connections-overview" data-connections-overview="">
    <header className="settings-content-heading">
      <div><h2>{t("models.connectionsTitle")}</h2><p>{t("models.connectionsIntro")}</p></div>
      <ConfigButton variant="primary" data-account-action="add-provider" disabled={catalog.loading && catalog.oauthProviders.length + catalog.apiKeyProviders.length === 0} onClick={onAdd}><span aria-hidden="true">＋</span>{t("models.addConnection")}</ConfigButton>
    </header>
    {error && <ConfigErrorNotice summary={t(error)} />}
    {count === 0 && loading ? <ConfigSkeleton label={t("i18n.loading")} lines={4} /> : count === 0 ? (
      !error && <ConfigEmptyState>{t("models.connectionsEmpty")}</ConfigEmptyState>
    ) : <div className="settings-data-panel">
      <div className="settings-panel-heading"><h3>{t("settings.connections")} <span className="settings-count-badge">{count}</span></h3><span>{t("models.connectionScope")}</span></div>
      <table className="settings-connections-table">
        <thead><tr>{headings.map((heading) => <th key={heading}>{heading}</th>)}</tr></thead>
        <tbody>{rows.map((row) => <tr key={row.key} data-connection-row={row.key}>
          <td data-label={headings[0]}><div className="settings-connection-name"><span className="settings-connection-avatar" aria-hidden="true">{row.key === "harness:claude" ? "C" : row.id === "anthropic" ? "A" : row.id === "openai-codex" ? "O" : "↗"}</span><div><button type="button" className="settings-connection-title" onClick={row.open}>{row.label}</button><small>{row.source}</small></div></div></td>
          <td data-label={headings[1]}>{row.auth}</td>
          <td data-label={headings[2]}><span className={row.tone === "ready" ? "settings-state-badge is-ready" : row.tone === "configured" ? "settings-state-badge is-configured" : "settings-state-badge is-pending"}><span aria-hidden="true">●</span>{row.status}</span></td>
          <td data-label={headings[3]}>{row.modelCount === null ? <span className="settings-muted">{t("models.countUnknown")}</span> : t("models.connectionModelCount", { count: row.modelCount })}</td>
          <td data-label={headings[4]}><ConfigButton size="small" variant="ghost" onClick={row.open} aria-label={t("models.connectionOpenNamed", { name: row.label })}>{t("models.connectionOpen")} <span aria-hidden="true">›</span></ConfigButton></td>
        </tr>)}</tbody>
      </table>
      <div className="settings-panel-footer"><span>{t("models.connectionTotal", { count })}</span><button type="button" onClick={onModels}>{t("models.listHeading")} <span aria-hidden="true">›</span></button></div>
    </div>}
    <p className="settings-content-note"><span aria-hidden="true">ⓘ</span>{t("models.connectionsStatusNote")}</p>
  </section>;
}
