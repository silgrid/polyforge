import { addSubscription, type PushSubscriptionRecord } from "@/lib/web-push";
import { checkPushEndpoint } from "@/lib/push-endpoint-guards";
import { isRecord } from "@/lib/type-guards";

export const dynamic = "force-dynamic";

interface SubscribeRequestBody {
  subscription?: Partial<PushSubscriptionRecord>;
  locale?: string;
}

function isSubscriptionShape(subscription: Partial<PushSubscriptionRecord> | undefined): subscription is PushSubscriptionRecord {
  if (typeof subscription !== "object" || subscription === null) return false;
  if (typeof subscription.endpoint !== "string" || !subscription.endpoint) return false;
  const keys = subscription.keys;
  if (typeof keys !== "object" || keys === null) return false;
  return typeof keys.p256dh === "string" && keys.p256dh.length > 0
    && typeof keys.auth === "string" && keys.auth.length > 0;
}

// POST /api/push/subscribe - register a browser push subscription. Upserts by
// endpoint, so the client can safely re-send its subscription on every load.
export async function POST(req: Request): Promise<Response> {
  let parsed: unknown;
  try {
    parsed = await req.json();
  } catch {
    return Response.json({ error: "invalidBody" }, { status: 400 });
  }
  // A syntactically valid JSON body that is not an object (null, a string, a
  // number, an array) must not reach a `.subscription` property read below.
  if (!isRecord(parsed)) {
    return Response.json({ error: "invalidBody" }, { status: 400 });
  }
  const body = parsed as SubscribeRequestBody;

  if (!isSubscriptionShape(body.subscription)) {
    return Response.json({ error: "invalidSubscription" }, { status: 400 });
  }

  // Audit S5: the endpoint is persisted and later sent to, so it must be an
  // https push-service destination (built-in browser push services plus
  // operator-configured suffixes), never an arbitrary host.
  const endpointCheck = checkPushEndpoint(body.subscription.endpoint);
  if (!endpointCheck.ok) {
    return Response.json({ error: endpointCheck.reason }, { status: 400 });
  }

  const locale = body.locale === "zh-CN" ? "zh-CN" : "en";
  await addSubscription({
    endpoint: body.subscription.endpoint,
    keys: body.subscription.keys,
    locale,
  });
  return Response.json({ ok: true });
}
