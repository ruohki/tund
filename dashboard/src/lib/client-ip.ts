import "server-only";
import { headers } from "next/headers";

/**
 * The visitor's address. The edge proxies the dashboard with X-Forwarded-For
 * set to the connecting client only (it drops incoming values), so the first
 * entry can be trusted.
 */
export async function clientIp(): Promise<string> {
  const h = await headers();
  return (h.get("x-forwarded-for") ?? "").split(",")[0].trim() || h.get("x-real-ip") || "";
}
