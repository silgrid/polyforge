import { NextResponse } from "next/server";
import { isImageManaged } from "@/lib/polyforge-detect";
import { hasJsonContentType, isApiRequestAllowed } from "@/lib/request-security";
import {
  ONBOARDING_STEPS,
  applyOnboardingAction,
  onboardingView,
  readFirstBootConnection,
  readOnboardingState,
  type OnboardingStep,
} from "@/lib/onboarding-state";

export const dynamic = "force-dynamic";

/**
 * GET /api/onboarding — the first-open onboarding wizard's state (polyforge#167,
 * 91-onboarding): the four steps' completion, the next step to show,
 * whether the popup was dismissed, and the AIHub step's connection read of
 * #162's first-boot record.
 *
 * Image-only (Q7): outside the workstation image the answer is the one
 * boolean `{imageManaged:false}` — no step, version, connection or error
 * fact, so a host-mode Pi Web cannot leak the wizard's existence. Behind the
 * same authenticated-surface gate as every /api route; the 403 carries no
 * machine fact either.
 *
 * The body is a closed whitelist (`lib/onboarding-state.ts`): no key, token,
 * hash or raw doctor/first-boot text ever reaches the browser — only the
 * published first-boot fields through their closed vocabularies.
 */
export async function GET(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!isImageManaged()) {
    return NextResponse.json({ imageManaged: false });
  }
  return NextResponse.json({ imageManaged: true, ...onboardingView(readOnboardingState(), readFirstBootConnection()) });
}

/** The actions the wizard writes; `skip` records no completion (the step
 *  stays 「未完成」) but names the step it advances past. */
const ACTIONS = new Set(["complete", "skip", "dismiss", "reopen"]);

function isStep(value: unknown): value is OnboardingStep {
  return typeof value === "string" && (ONBOARDING_STEPS as readonly string[]).includes(value);
}

/**
 * POST /api/onboarding {action, step?} — one wizard write. The gate (403,
 * image-only), JSON-only (415), an unparsable body (400), an unknown action
 * or a missing step (400) all answer before any write. The write is one
 * read-merge-write per file, so two tabs completing two steps keep both.
 * The answer is the fresh GET-shaped view, never an echo of the request.
 */
export async function POST(request: Request) {
  if (!isApiRequestAllowed(request)) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!isImageManaged()) {
    return NextResponse.json({ error: "Forbidden" }, { status: 403 });
  }
  if (!hasJsonContentType(request)) {
    return NextResponse.json({ error: "Content-Type must be application/json" }, { status: 415 });
  }
  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  const record = typeof body === "object" && body !== null && !Array.isArray(body) ? body as Record<string, unknown> : {};
  const action = typeof record.action === "string" ? record.action : "";
  if (!ACTIONS.has(action)) {
    return NextResponse.json({ error: "action must be one of complete, skip, dismiss, reopen" }, { status: 400 });
  }
  const needsStep = action === "complete" || action === "skip";
  if (needsStep && !isStep(record.step)) {
    return NextResponse.json({ error: "step must name one of the four onboarding steps" }, { status: 400 });
  }
  const state = await applyOnboardingAction(
    action as "complete" | "skip" | "dismiss" | "reopen",
    isStep(record.step) ? record.step : null,
  );
  return NextResponse.json({ imageManaged: true, ...onboardingView(state, readFirstBootConnection()) });
}
