"use client";

// The read-only Claude Code report (polyforge#150 85a4, owner ruling: the
// block the retired Claude page showed is taken over here, on the permission
// page's approval pane): claude's version and capabilities as it reports
// them, beside Pi Web's three claude features with their basis. Presentational
// like SettingsUi.tsx: the page fetches GET /api/claude and passes the answer,
// every word comes from the locale tables.
import { useI18n } from "@/hooks/useI18n";
import { CLAUDE_FEATURE_MIN_VERSION, type ClaudeFeature } from "@/lib/claude-shared";
import { ConfigDetailGrid, ConfigDetailGridRow, ConfigNotice } from "./SettingsUi";

/** What the page reads of GET /api/claude: the claude on PATH as it reports itself. */
export interface ClaudeReport {
  available?: boolean;
  version?: string | null;
  versionRecorded?: boolean | null;
  recordedVersions?: string[];
  capabilities?: string[];
  features?: ClaudeFeature[];
  probeError?: string;
}

type Translate = (key: string, params?: Record<string, string | number>) => string;

function featureName(feature: ClaudeFeature, t: Translate): string {
  if (feature.id === "approvals") return t("settings.claudeFeatureApprovals");
  if (feature.id === "backgroundTasks") return t("settings.claudeFeatureBackgroundTasks");
  return t("settings.claudeFeatureSubagentText");
}

function featureStatus(feature: ClaudeFeature, t: Translate): string {
  if (feature.available === null) return t("settings.claudeFeatureUnknown");
  return feature.available ? t("settings.claudeFeatureAvailable") : t("settings.claudeFeatureUnavailable");
}

/** Why a feature is judged so (GET /api/claude's `basis` code), as text. */
function featureBasis(feature: ClaudeFeature, t: Translate): string {
  const min = feature.id === "approvals" ? "" : CLAUDE_FEATURE_MIN_VERSION[feature.id];
  switch (feature.basis) {
    case "no-host-prompts": return t("settings.claudeBasisNoHostPrompts");
    case "host-prompts": return t("settings.claudeBasisHostPrompts");
    case "recorded": return t("settings.claudeBasisRecorded", { min });
    case "below-recorded": return t("settings.claudeBasisBelowRecorded", { min });
    case "env-disabled": return t("settings.claudeBasisEnvDisabled");
    default: return t("settings.claudeBasisVersionUnknown");
  }
}

const cellStyle = { padding: "6px 8px", textAlign: "left", verticalAlign: "top", borderTop: "1px solid var(--border)" } as const;

/** The read-only block: claude's version and capabilities as it reports them, Pi Web's three features. */
export function ClaudeCodeReport({ report, error }: { report: ClaudeReport | null; error: string | null }) {
  const { t } = useI18n();
  const version = report?.version ?? null;
  return (
    <section className="settings-general-section" data-permission-zone="claude-report">
      <h3 className="settings-general-heading">{t("settings.claudeCode")}</h3>
      <p className="settings-general-description">{t("settings.claudeCodeDescription")}</p>
      {error && <p role="alert" className="settings-general-error">{t("settings.claudeReportFailed", { error })}</p>}
      {report === null && !error && <p className="settings-general-description">{t("settings.claudeReading")}</p>}
      {report && (
        <>
          {report.available === false && <p className="settings-general-error">{t("claude.notOnPath")}</p>}
          {report.probeError && <p className="settings-general-error">{t("settings.claudeProbeError", { error: report.probeError })}</p>}
          {version && report.versionRecorded === false && (
            <ConfigNotice>
              {t("settings.claudeVersionNotRecorded", { version, versions: (report.recordedVersions ?? []).join(", ") })}
            </ConfigNotice>
          )}
          <ConfigDetailGrid>
            <ConfigDetailGridRow label={t("settings.claudeVersion")} tone="plain" mono>
              {version ?? t("settings.claudeVersionUnknown")}
            </ConfigDetailGridRow>
            <ConfigDetailGridRow label={t("settings.claudeCapabilities")} tone="plain">
              {(report.capabilities ?? []).length === 0
                ? <span className="settings-general-description" style={{ margin: 0 }}>{t("settings.claudeCapabilitiesNone")}</span>
                : (
                  <ul aria-label={t("settings.claudeCapabilities")} style={{ margin: 0, padding: 0, listStyle: "none", display: "flex", flexWrap: "wrap", gap: "2px 10px" }}>
                    {(report.capabilities ?? []).map((capability) => <li key={capability}><code>{capability}</code></li>)}
                  </ul>
                )}
            </ConfigDetailGridRow>
          </ConfigDetailGrid>
          <table aria-label={t("settings.claudeFeatures")} style={{ width: "100%", borderCollapse: "collapse", fontSize: 13, marginTop: 8 }}>
            <thead>
              <tr>
                <th scope="col" style={{ ...cellStyle, borderTop: "none" }}>{t("settings.claudeFeature")}</th>
                <th scope="col" style={{ ...cellStyle, borderTop: "none" }}>{t("settings.claudeFeatureStatus")}</th>
                <th scope="col" style={{ ...cellStyle, borderTop: "none" }}>{t("settings.claudeFeatureBasis")}</th>
              </tr>
            </thead>
            <tbody>
              {(report.features ?? []).map((feature) => (
                <tr key={feature.id}>
                  <td style={cellStyle}>{featureName(feature, t)}</td>
                  <td style={{ ...cellStyle, whiteSpace: "nowrap", color: feature.available ? "var(--accent)" : "var(--text-muted)" }}>{featureStatus(feature, t)}</td>
                  <td style={{ ...cellStyle, color: "var(--text-muted)" }}>{featureBasis(feature, t)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}
