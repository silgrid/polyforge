/**
 * The one projection of a settings read's state (polyforge#271 AC1, Q5 A):
 * a first read with no value is a skeleton, a successful read with no rows is
 * the empty state *with* its action, a failed read with no rows is the alert
 * state — never the empty one — and a failed read that still has rows is a
 * partial success that keeps them and shows the failure beside them.
 */
export interface ReadView {
  kind: "loading" | "error" | "empty" | "ready";
  /** The rows on screen are still valid; the failure is shown beside them. */
  staleError: boolean;
}

export function readView({ loading, error, count }: { loading: boolean; error: boolean; count: number }): ReadView {
  if (loading) return { kind: "loading", staleError: false };
  if (error && count === 0) return { kind: "error", staleError: false };
  if (error) return { kind: "ready", staleError: true };
  if (count === 0) return { kind: "empty", staleError: false };
  return { kind: "ready", staleError: false };
}
