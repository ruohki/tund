import { getCurrentUser } from "@/lib/auth";
import { getRequestDetail } from "@/lib/requests";

export const dynamic = "force-dynamic";

export async function GET(_: Request, ctx: RouteContext<"/api/requests/[id]">) {
  const user = await getCurrentUser();
  if (!user) return Response.json({ error: "Not signed in." }, { status: 401 });
  const { id } = await ctx.params;
  const detail = await getRequestDetail(user.id, id);
  if (!detail) return Response.json({ error: "Request not found." }, { status: 404 });
  return Response.json(detail);
}
