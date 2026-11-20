import { NextResponse } from "next/server";
import { trimSessionListFirstMessages } from "@/lib/session-list-payload";
import {
  attachSessionProjectInfo,
  getSessionListVersion,
  listAllSessions,
  listSessionSummaries,
  mergeSessionLists,
} from "@/lib/session-reader";
import {
  getAwaitingRpcSessionIds,
  getCompletionNotificationSuppressedRpcSessionIds,
  getRpcSessionInfos,
  getRunningRpcSessionIds,
  withSubagentDisplayStatus,
} from "@/lib/rpc-manager";
import { startServerPerf } from "@/lib/perf";
// The claude rows come from the cached, worker-parsed list projection (polyforge#140).
import { getAwaitingClaudeSessionIds, getClaudeSessionInfos, getRunningClaudeSessionIds } from "@/lib/claude-session";

export const dynamic = "force-dynamic";

export async function GET(req: Request) {
  const perf = startServerPerf("GET /api/sessions");
  try {
    const searchParams = new URL(req.url).searchParams;
    const force = searchParams.get("force") === "1";
    // `summary=1` serves header/stat metadata so the sidebar can paint without
    // waiting for every session transcript to be parsed.
    const summary = searchParams.get("summary") === "1";
    perf?.span("start");
    const persistedSessionsPromise = summary
      ? listSessionSummaries()
      : listAllSessions({ force });
    // Capture before awaiting: mutations during the scan still require a later refresh.
    const sessionListVersion = getSessionListVersion();
    // claude rows come from pi-web's own index, beside the live pi rows.
    const [persistedSessions, runtimeSessions] = await Promise.all([
      persistedSessionsPromise,
      attachSessionProjectInfo([...getRpcSessionInfos(), ...await getClaudeSessionInfos({ summary })]),
    ]);
    perf?.span("scan+projects");
    // Trim at the RESPONSE layer only — the reader's view cache and the
    // detail endpoint keep full text.
    const sessions = withSubagentDisplayStatus(trimSessionListFirstMessages(mergeSessionLists(persistedSessions, runtimeSessions)));
    return perf?.attach(NextResponse.json(
      {
        sessions,
        sessionListVersion,
        runningSessionIds: [...getRunningRpcSessionIds(), ...getRunningClaudeSessionIds()],
        awaitingSessionIds: [...getAwaitingRpcSessionIds(), ...getAwaitingClaudeSessionIds()],
        completionNotificationSuppressedSessionIds: getCompletionNotificationSuppressedRpcSessionIds(),
      },
      { headers: { "Cache-Control": "no-store" } },
    )) ?? NextResponse.json(
      {
        sessions,
        sessionListVersion,
        runningSessionIds: [...getRunningRpcSessionIds(), ...getRunningClaudeSessionIds()],
        awaitingSessionIds: [...getAwaitingRpcSessionIds(), ...getAwaitingClaudeSessionIds()],
        completionNotificationSuppressedSessionIds: getCompletionNotificationSuppressedRpcSessionIds(),
      },
      { headers: { "Cache-Control": "no-store" } },
    );
  } catch (error) {
    return NextResponse.json(
      { error: String(error) },
      { status: 500, headers: { "Cache-Control": "no-store" } },
    );
  }
}
