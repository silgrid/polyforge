/**
 * Pure typed guard for browser push subscription endpoints (audit S5).
 *
 * `/api/push/subscribe` persists a request-supplied endpoint that
 * `lib/web-push.ts` later sends to (VAPID-signed, but still an outbound
 * request). Without a destination check any authenticated client can turn
 * pi-web into a request sender to an arbitrary host (SSRF-class). The guard
 * allows only https endpoints whose host is one of the standard browser
 * push services, plus operator-configured suffixes via
 * `PI_WEB_PUSH_ALLOWED_ENDPOINT_SUFFIXES` (comma-separated host suffixes,
 * added to the built-ins — extending the list never disables the defaults).
 *
 * Typed codes only — never English prose (repo convention, see
 * lib/fs-manage-guards.ts).
 */

/** Host suffixes of the push services real browsers hand out. */
export const DEFAULT_PUSH_ENDPOINT_HOST_SUFFIXES: readonly string[] = [
  // Chrome, Edge, and Chromium-on-Android subscriptions (FCM).
  "fcm.googleapis.com",
  // Firefox (Mozilla autopush).
  "updates.push.services.mozilla.com",
  // Safari 16.4+ Web Push on macOS and iOS.
  "web.push.apple.com",
];

export type PushEndpointRefusalReason =
  | "invalidEndpointUrl"
  | "pushEndpointScheme"
  | "pushEndpointHostNotAllowed";

export type PushEndpointCheck =
  | { ok: true; endpoint: string; host: string }
  | { ok: false; reason: PushEndpointRefusalReason };

/** Env seam, injectable for tests; an index signature keeps Node's ProcessEnv assignable. */
export type PushEndpointEnv = {
  PI_WEB_PUSH_ALLOWED_ENDPOINT_SUFFIXES?: string;
} & Record<string, string | undefined>;

export function allowedPushEndpointHostSuffixes(
  env: PushEndpointEnv = process.env,
  defaults: readonly string[] = DEFAULT_PUSH_ENDPOINT_HOST_SUFFIXES,
): readonly string[] {
  const extra = (env.PI_WEB_PUSH_ALLOWED_ENDPOINT_SUFFIXES ?? "")
    .split(",")
    .map((value) => value.trim().toLowerCase())
    .filter(Boolean);
  return [...defaults, ...extra];
}

/** `host === suffix` or `host` is a subdomain of `suffix`. */
export function hostMatchesSuffix(host: string, suffix: string): boolean {
  return host === suffix || host.endsWith(`.${suffix}`);
}

/**
 * The endpoint check. `endpoint` must already be a non-empty https string
 * (the route's shape validation runs first); this guard classifies every
 * other refusal with a typed code.
 */
export function checkPushEndpoint(
  endpoint: string,
  hostSuffixes: readonly string[] = allowedPushEndpointHostSuffixes(),
): PushEndpointCheck {
  let parsed: URL;
  try {
    parsed = new URL(endpoint);
  } catch {
    return { ok: false, reason: "invalidEndpointUrl" };
  }
  if (parsed.protocol !== "https:") return { ok: false, reason: "pushEndpointScheme" };
  const host = parsed.hostname.toLowerCase();
  if (!host || !hostSuffixes.some((suffix) => hostMatchesSuffix(host, suffix.toLowerCase()))) {
    return { ok: false, reason: "pushEndpointHostNotAllowed" };
  }
  return { ok: true, endpoint, host };
}
