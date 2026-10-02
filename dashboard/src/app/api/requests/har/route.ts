import type { NextRequest } from "next/server";
import { getCurrentUser } from "@/lib/auth";
import { requestsAsHar } from "@/lib/har";
import { isUuid } from "@/lib/requests";

export const dynamic = "force-dynamic";

/** GET /api/requests/har?id=<uuid> or ?host=&method=&status=&q=&body=1&tunnel= → HAR 1.2 download. */
export async function GET(request: NextRequest) {
  const user = await getCurrentUser();
  if (!user) return Response.json({ error: "Not signed in." }, { status: 401 });
  const p = request.nextUrl.searchParams;
  const ids = p.getAll("id").filter(isUuid);
  const host = p.get("host") ?? "";
  const { har, count } = await requestsAsHar(
    user.id,
    ids.length
      ? { ids }
      : {
          filters: {
            host: host || undefined,
            method: p.get("method") ?? undefined,
            status: p.get("status") ?? undefined,
            q: p.get("q") ?? undefined,
            body: p.get("body") === "1",
            tunnel: p.get("tunnel") ?? undefined,
          },
        },
  );
  if (ids.length && !count) return Response.json({ error: "Request not found." }, { status: 404 });
  const stamp = new Date().toISOString().slice(0, 16).replace(/[-:]/g, "").replace("T", "-");
  const name = ids.length === 1 ? `request-${ids[0].slice(0, 8)}` : `requests-${host.replace(/[^a-z0-9.-]/gi, "") || "all"}-${stamp}`;
  return new Response(JSON.stringify(har, null, 2), {
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Content-Disposition": `attachment; filename="${name}.har"`,
      "Cache-Control": "private, no-store",
      "X-Content-Type-Options": "nosniff",
    },
  });
}
