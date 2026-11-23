"use client";

import type { PolyforgeDecision } from "@/lib/polyforge-decisions-pure";

/** The one-time in-app explanation a browser without notification permission
 *  shows when an owner decision would have notified (polyforge#240 Q4). */
export function PolyforgeNotifications(_props: {
  decisions: readonly PolyforgeDecision[] | null;
  onOpenDecision: (decision: PolyforgeDecision) => void;
}) {
  return null;
}