import type { NextRequest } from "next/server";
import { getCurrentUser } from "@/lib/auth";
import { listRequests } from "@/lib/requests";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest) {
  const user = await getCurrentUser();
  if (!user) return Response.json({ error: "Not signed in." }, { status: 401 });
  const p = request.nextUrl.searchParams;
  const limit = Number(p.get("limit") ?? 100) || 100;
  const items = await listRequests(
    user.id,
    {
      host: p.get("host") ?? undefined,
      method: p.get("method") ?? undefined,
      status: p.get("status") ?? undefined,
      q: p.get("q") ?? undefined,
      body: p.get("body") === "1",
      tunnel: p.get("tunnel") ?? undefined,
    },
    { before: p.get("before") ?? undefined, beforeId: p.get("beforeId") ?? undefined },
    limit,
  );
  return Response.json({ items, hasMore: items.length >= limit });
}
