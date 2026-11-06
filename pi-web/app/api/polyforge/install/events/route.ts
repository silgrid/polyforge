import { NextResponse } from "next/server";
import { isApiRequestAllowed } from "@/lib/request-security";
import { polyforgeInstallRun, subscribePolyforgeInstall, type PolyforgeInstallEvent } from "@/lib/polyforge-install";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

/**
 * GET /api/polyforge/install/events?after=<n> — the current install run's
 * output as SSE: every kept line after line `n` (or the
 * Last-Event-ID), then each new line as it is printed, then one `end`
 * event carrying the final run, after which the stream closes. A finished
 * run replays its lines and ends at once, so a page opened later shows the
 * result. Closing the stream never touches the run. 404 before any run.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!polyforgeInstallRun()) {
    return NextResponse.json({ error: "no install has run" }, { status: 404 });
  }
  const cursor = request.headers.get("last-event-id") ?? new URL(request.url).searchParams.get("after");
  const after = cursor !== null && /^\d+$/.test(cursor) && Number.isSafeInteger(Number(cursor)) ? Number(cursor) : 0;

  let release: (closeController: boolean) => void = () => {};
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      const encoder = new TextEncoder();
      let closed = false;
      let heartbeat: ReturnType<typeof setInterval> | null = null;
      let unsubscribe: (() => void) | null = null;
      const cleanup = (closeController: boolean) => {
        if (closed) return;
        closed = true;
        if (heartbeat) clearInterval(heartbeat);
        unsubscribe?.();
        request.signal.removeEventListener("abort", abort);
        if (closeController) {
          try { controller.close(); } catch { /* already closed */ }
        }
      };
      const abort = () => cleanup(true);
      const send = (event: PolyforgeInstallEvent) => {
        if (closed) return;
        try {
          const id = event.type === "line" ? `id: ${event.n}\n` : "";
          controller.enqueue(encoder.encode(`${id}data: ${JSON.stringify(event)}\n\n`));
          if (event.type === "end") cleanup(true);
        } catch {
          cleanup(false);
        }
      };
      release = cleanup;
      const subscription = subscribePolyforgeInstall(after, send);
      if (!subscription) {
        cleanup(true);
        return;
      }
      unsubscribe = subscription.unsubscribe;
      controller.enqueue(encoder.encode(":\n\n"));
      for (const line of subscription.lines) send({ type: "line", ...line });
      if (subscription.run.state !== "running") send({ type: "end", run: subscription.run });
      if (closed) return;
      request.signal.addEventListener("abort", abort, { once: true });
      if (request.signal.aborted) {
        abort();
        return;
      }
      heartbeat = setInterval(() => {
        try { if (!closed) controller.enqueue(encoder.encode(":\n\n")); } catch { cleanup(false); }
      }, 30_000);
    },
    cancel() {
      release(false);
    },
  });

  return new Response(stream, {
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache, no-transform",
      Connection: "keep-alive",
    },
  });
}
