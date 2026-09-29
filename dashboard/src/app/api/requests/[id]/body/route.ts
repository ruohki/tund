import type { NextRequest } from "next/server";
import { getCurrentUser } from "@/lib/auth";
import { decodeBody, getRawBody } from "@/lib/requests";

export const dynamic = "force-dynamic";

// Captured bodies are untrusted content from the internet: never let them run
// on the dashboard origin.
const SAFE_HEADERS = {
  "Content-Security-Policy": "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox",
  "X-Content-Type-Options": "nosniff",
  "Cache-Control": "private, no-store",
};

export async function GET(request: NextRequest, ctx: RouteContext<"/api/requests/[id]/body">) {
  const user = await getCurrentUser();
  if (!user) return Response.json({ error: "Not signed in." }, { status: 401 });
  const { id } = await ctx.params;
  const p = request.nextUrl.searchParams;
  const part = p.get("part") === "req" ? "req" : "resp";
  const body = await getRawBody(user.id, id, part);
  if (!body) return Response.json({ error: "Request not found." }, { status: 404 });

  const find = (name: string) => {
    const k = Object.keys(body.headers).find((h) => h.toLowerCase() === name);
    return k ? (body.headers[k]?.[0] ?? "") : "";
  };
  const encoding = find("content-encoding");
  const raw = p.get("raw") === "1";
  const data = raw ? body.data : decodeBody(body.data, encoding).data;
  const contentType = find("content-type") || "application/octet-stream";
  const inline = p.get("inline") === "1" && /^image\/(png|jpe?g|gif|webp|avif|svg\+xml|x-icon|vnd\.microsoft\.icon)$/i.test(contentType.split(";")[0].trim());
  const ext = contentType.includes("json") ? "json" : contentType.startsWith("text/") ? "txt" : "bin";

  return new Response(new Uint8Array(data), {
    headers: {
      ...SAFE_HEADERS,
      "Content-Type": inline ? contentType : raw || ext === "bin" ? "application/octet-stream" : `${contentType.split(";")[0]}; charset=utf-8`,
      "Content-Disposition": inline ? "inline" : `attachment; filename="${part === "req" ? "request" : "response"}-${id.slice(0, 8)}${raw && encoding ? `.${encoding}` : ""}.${ext}"`,
      "X-Tund-Truncated": body.truncated ? "1" : "0",
    },
  });
}
