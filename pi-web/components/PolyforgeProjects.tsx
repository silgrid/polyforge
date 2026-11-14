"use client";

import { useEffect, useRef, useState } from "react";
import { useI18n } from "@/hooks/useI18n";

/**
 * Work › 项目 (polyforge#30, AC7): the Cell's mapped projects with their
 * global states, from `GET /api/polyforge/projects` (one read per opening —
 * the shared ↻ discipline is a later concern, none here). Deactivated
 * projects are hidden by default; the「显示已停用」entry beside the list
 * reveals them with their marker, and the restore entry sits in the same
 * row — `POST /api/polyforge/projects {project, action: "restore"}` (the
 * route's one write; the server enforces who may), then the one re-read
 * settles the view on the answer. No timer, no polling, nothing
 * persisted.
 */
interface ProjectsRead {
  pfPresent: boolean;
  projects: { project: string; status: string; error?: string }[];
  error?: string;
}

const POLYFORGE_BLOCK_STYLE = { padding: 12, fontSize: 12, flexShrink: 0 } as const;

export function PolyforgeProjects() {
  const { t } = useI18n();
  const [list, setList] = useState<ProjectsRead | null>(null);
  const [listError, setListError] = useState("");
  const [showDeactivated, setShowDeactivated] = useState(false);
  const [actionError, setActionError] = useState("");
  const [restoring, setRestoring] = useState<string | null>(null);
  const listReadRef = useRef(false);

  const readList = async () => {
    try {
      const response = await fetch("/api/polyforge/projects", { cache: "no-store" });
      if (!response.ok) throw new Error(`status ${response.status}`);
      const body = await response.json();
      if (body && typeof body === "object" && Array.isArray(body.projects)) {
        setList(body as ProjectsRead);
        setListError("");
      } else {
        setListError(t("polyforge.projects.error"));
      }
    } catch {
      setListError(t("polyforge.projects.error"));
    }
  };

  // one list read per opening (the sub-tab unmounts on switch, so the ref
  // lives here alone).
  useEffect(() => {
    if (listReadRef.current) return;
    listReadRef.current = true;
    readList();
  });

  const restore = async (project: string) => {
    setRestoring(project);
    try {
      const response = await fetch("/api/polyforge/projects", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ project, action: "restore" }),
      });
      if (!response.ok) {
        const body = await response.json().catch(() => null);
        setActionError(body && typeof body.error === "string" ? body.error : t("polyforge.projects.error"));
      } else {
        setActionError("");
        readList();
      }
    } catch {
      setActionError(t("polyforge.projects.error"));
    } finally {
      setRestoring(null);
    }
  };

  const rows = (list?.projects ?? []).filter((p) => showDeactivated || p.status !== "deactivated");
  const dormant = (list?.projects ?? []).filter((p) => p.status === "deactivated").length;

  return (
    <div data-pf-projects="" style={{ flex: 1, minHeight: 0, overflowY: "auto", display: "flex", flexDirection: "column" }}>
      <div style={{ padding: "8px 12px", borderBottom: "1px solid var(--border)", display: "flex", alignItems: "center", gap: 8, flexShrink: 0 }}>
        <button type="button" data-testid="pf-projects-toggle"
          onClick={() => { setShowDeactivated(!showDeactivated); setActionError(""); }}
          style={{ background: "none", border: "1px solid var(--border)", borderRadius: 4, color: "var(--text-muted)", cursor: "pointer", fontSize: 11, padding: "2px 8px" }}>
          {showDeactivated ? t("polyforge.projects.hideDeactivated") : t("polyforge.projects.showDeactivated")}
        </button>
        {showDeactivated && dormant > 0 && (
          <span style={{ fontSize: 11, color: "var(--text-dim)" }}>{dormant}</span>
        )}
      </div>
      {listError && <div style={{ padding: "8px 12px", color: "#dc2626", fontSize: 12 }}>{listError}</div>}
      {actionError && <div style={{ padding: "8px 12px", color: "#dc2626", fontSize: 12 }}>{actionError}</div>}
      <div data-testid="pf-projects-list" style={{ padding: "8px 12px", display: "flex", flexDirection: "column", gap: 4 }}>
        {list === null ? (
          <div style={{ fontSize: 11, color: "var(--text-dim)" }}>{t("polyforge.loading")}</div>
        ) : rows.length === 0 ? (
          <div style={{ fontSize: 11, color: "var(--text-dim)" }}>{t("polyforge.projects.empty")}</div>
        ) : rows.map((p) => (
          <div key={p.project} data-pf-project-row=""
            style={{ ...POLYFORGE_BLOCK_STYLE, display: "flex", alignItems: "center", gap: 8, border: "1px solid var(--border)", borderRadius: 4, padding: "6px 10px" }}>
            <span style={{ fontFamily: "var(--font-mono)", color: "var(--text)", minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{p.project}</span>
            {p.error && <span style={{ fontSize: 11, color: "#d97706", overflowWrap: "anywhere" }}>{p.error}</span>}
            {p.status === "deactivated" && (
              <span data-pf-project-mark="" title={t("polyforge.projects.deactivatedTitle")}
                style={{ fontSize: 11, color: "var(--text-dim)", border: "1px solid var(--border)", borderRadius: 4, padding: "1px 6px", flexShrink: 0 }}>
                {t("polyforge.projects.deactivated")}
              </span>
            )}
            <span style={{ marginLeft: "auto", fontSize: 11, color: "var(--text-dim)", flexShrink: 0 }}>
              {p.status === "deactivated" ? t("polyforge.projects.deactivated") : t("polyforge.projects.active")}
            </span>
            {p.status === "deactivated" && (
              <button type="button" data-testid="pf-projects-restore" disabled={restoring === p.project}
                onClick={() => restore(p.project)}
                title={t("polyforge.projects.restoreTitle")}
                style={{ background: "none", border: "1px solid var(--border)", borderRadius: 4, color: "var(--text-muted)", cursor: "pointer", fontSize: 11, padding: "2px 8px", flexShrink: 0 }}>
                {t("polyforge.projects.restore")}
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
