"use client";

import { useMachineLoad } from "@/hooks/useMachineLoad";
import { machineLoadRows } from "@/lib/machine-load-view";

/**
 * The machine-load section: CPU, cores, load average and
 * memory as the HOST kernel sees them (the badge says so; no cgroup/container
 * quota is implied). One mount: the Debug panel (DebugPanelView),
 * as the rightmost column of its `.debug-cols` grid (under the
 * sessions' column on a middle-width panel, the foot of the phone stack; the
 * Session-info popover no longer carries it). Its row separator there is the
 * panel's CSS, not this markup. It is rendered only while that panel is open,
 * so the 2 s poll and the values live exactly as long as the open panel. It
 * performs no I/O itself (useMachineLoad does).
 */
export function MachineLoadSection({ translate }: { translate: (key: string) => string }) {
  const metrics = useMachineLoad();
  const rows = machineLoadRows(metrics, {
    cpu: translate("session.machineCpu"),
    cores: translate("session.machineCores"),
    loadavg: translate("session.machineLoadavg"),
    memory: translate("session.machineMemory"),
    unavailable: translate("session.machineUnavailable"),
  });

  return (
    <div
      className="machine-load-section"
      style={{
        minWidth: 0,
        fontSize: 12,
        lineHeight: 1.5,
        fontFamily: "var(--font-mono)",
      }}
    >
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8, marginBottom: 6 }}>
        <span style={{ fontSize: 11, fontWeight: 700, color: "var(--text)" }}>{translate("session.machineLoad")}</span>
        <span style={{
          fontSize: 10,
          lineHeight: "16px",
          padding: "0 6px",
          color: "var(--text-dim)",
          border: "1px solid var(--border)",
          borderRadius: 4,
        }}>{translate("session.machineHost")}</span>
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "minmax(0, max-content) minmax(max-content, 1fr)", columnGap: 12, rowGap: 4 }}>
        {rows.map((row) => (
          <div key={row.key} style={{ display: "contents" }}>
            <div style={{ color: "var(--text-dim)", minWidth: 0, overflowWrap: "anywhere" }}>{row.label}</div>
            <div style={{ color: "var(--text-muted)", minWidth: 0, overflowWrap: "anywhere", fontVariantNumeric: "tabular-nums" }}>
              {row.value}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
