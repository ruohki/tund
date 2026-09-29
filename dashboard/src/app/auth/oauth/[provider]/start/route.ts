import type { NextRequest } from "next/server";
import { redirect } from "next/navigation";
import { getCurrentUser, safeNext, withNext } from "@/lib/auth";
import { startOAuth } from "@/lib/oauth";
import { isOAuthProvider } from "@/lib/oauth-shared";

export const dynamic = "force-dynamic";

/** "Continue with Google/GitHub" (?next=…), or "Connect" in Settings (?intent=link). */
export async function GET(req: NextRequest, ctx: RouteContext<"/auth/oauth/[provider]/start">) {
  const { provider } = await ctx.params;
  const q = req.nextUrl.searchParams;
  const intent = q.get("intent") === "link" ? "link" : "login";
  const next = safeNext(q.get("next"));
  const user = await getCurrentUser();
  if (intent === "link" && !user) redirect(withNext("/login", "/settings"));
  if (intent === "login" && user) redirect(next);
  const target = isOAuthProvider(provider) ? await startOAuth(provider, intent, next) : null;
  redirect(target ?? (intent === "link" ? "/settings?oauth_error=unavailable" : "/login?error=unavailable"));
}
