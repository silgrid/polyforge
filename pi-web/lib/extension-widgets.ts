import type { ExtensionWidgetItem } from "@/lib/types";

export function updateExtensionWidgets(
  widgets: ExtensionWidgetItem[],
  key: string,
  lines: string[] | undefined,
  placement: ExtensionWidgetItem["placement"] = "aboveEditor",
): ExtensionWidgetItem[] {
  if (lines === undefined) return widgets.filter((widget) => widget.key !== key);

  const updatedWidget = { key, lines, placement };
  const existingIndex = widgets.findIndex((widget) => widget.key === key);
  if (existingIndex === -1) return [...widgets, updatedWidget];

  return widgets.map((widget, index) => index === existingIndex ? updatedWidget : widget);
}

/**
 * A state snapshot (`GET /api/sessions/[id]/state`) is read while the event
 * stream keeps delivering `setStatus`/`setWidget`: an event can land before a
 * snapshot that the server read earlier. Keys an event touched since the read
 * began keep their live value (or stay cleared); every other key takes the
 * snapshot's, in the snapshot's order.
 */
export function mergeExtensionSnapshot<T extends { key: string }>(
  snapshot: T[],
  live: T[],
  touched: ReadonlySet<string>,
): T[] {
  if (touched.size === 0) return snapshot;
  const liveByKey = new Map(live.map((item) => [item.key, item]));
  const merged: T[] = [];
  for (const item of snapshot) {
    if (!touched.has(item.key)) merged.push(item);
    else if (liveByKey.has(item.key)) merged.push(liveByKey.get(item.key)!);
  }
  const snapshotKeys = new Set(snapshot.map((item) => item.key));
  for (const item of live) {
    if (touched.has(item.key) && !snapshotKeys.has(item.key)) merged.push(item);
  }
  return merged;
}
