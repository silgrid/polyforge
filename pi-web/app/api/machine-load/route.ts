import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { machineLoadSampler } from "@/lib/machine-load";
import type { MachineLoadMetrics } from "@/lib/machine-load-view";

export const dynamic = "force-dynamic";

/**
 * GET /api/machine-load — the host's aggregate load for the Debug panel
 * (its only consumer): exactly `{ metrics: { cpuPercent, coreCount,
 * loadavg, memoryUsedBytes, memoryTotalBytes } }`, sampled on demand and
 * shared across clients for 2 s (lib/machine-load.ts). Degradation-first like
 * /api/polyforge/status: a failed sample answers 200 `{ metrics: null }`;
 * the only non-2xx answer is the /api gate's 403. No 5xx path.
 */
export function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  let metrics: MachineLoadMetrics | null = null;
  try {
    const sample = machineLoadSampler.sample();
    // Whitelist copy: only these five fields ever leave the server.
    if (sample) {
      metrics = {
        cpuPercent: sample.cpuPercent,
        coreCount: sample.coreCount,
        loadavg: sample.loadavg,
        memoryUsedBytes: sample.memoryUsedBytes,
        memoryTotalBytes: sample.memoryTotalBytes,
      };
    }
  } catch {
    metrics = null;
  }
  return NextResponse.json({ metrics });
}
