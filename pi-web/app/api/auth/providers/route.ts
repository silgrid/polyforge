import { buildApiKeyProviderList, offerableOAuthProviders } from "@/lib/provider-listing";
import { collectProviderListingInputs } from "@/lib/provider-listing-runtime";
import { getModelRuntimeWithExtensions } from "@/lib/model-runtime";

export const dynamic = "force-dynamic";

// Providers that declare an OAuth login method, offered with pi-web's one
// exception (polyforge#142, 85a2 AC8, owner ruling Q10): pi's Anthropic
// subscription (Claude Pro/Max) login is never offered — Anthropic's terms
// do not permit a third-party application to offer Claude.ai login — so it
// is filtered unless a stored credential exists (status and logout only).
// Claude models go through the claude harness.
export async function GET() {
  const modelRuntime = await getModelRuntimeWithExtensions();
  const inputs = await collectProviderListingInputs(modelRuntime);
  const oauthProviders = offerableOAuthProviders(inputs);
  const apiKeyProviders = buildApiKeyProviderList(inputs);
  return Response.json({ providers: oauthProviders, oauthProviders, apiKeyProviders });
}
