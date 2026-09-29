import type { NextRequest } from "next/server";
import { notFound, redirect } from "next/navigation";
import { getCurrentUser } from "@/lib/auth";
import { finishOAuth } from "@/lib/oauth";
import { isOAuthProvider } from "@/lib/oauth-shared";

export const dynamic = "force-dynamic";

/** The redirect URI registered with Google / GitHub (Admin → Sign-in shows it). */
export async function GET(req: NextRequest, ctx: RouteContext<"/auth/oauth/[provider]/callback">) {
  const { provider } = await ctx.params;
  if (!isOAuthProvider(provider)) notFound();
  redirect(await finishOAuth(provider, req.nextUrl.searchParams, await getCurrentUser()));
}
