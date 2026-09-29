import type { NextRequest } from "next/server";
import { getCurrentUser } from "@/lib/auth";
import { listConnections } from "@/lib/connections";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest) {
  const user = await getCurrentUser();
  if (!user) return Response.json({ error: "Not signed in." }, { status: 401 });
  const p = request.nextUrl.searchParams;
  const limit = Number(p.get("limit") ?? 100) || 100;
  const items = await listConnections(
    user.id,
    {
      address: p.get("host") ?? undefined,
      tunnel: p.get("tunnel") ?? undefined,
      before: p.get("before") ?? undefined,
      beforeId: p.get("beforeId") ?? undefined,
    },
    limit,
  );
  return Response.json({ items, hasMore: items.length >= limit });
}
