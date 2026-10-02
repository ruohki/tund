import "server-only";
import { config } from "./config";
import { db } from "./db";

/**
 * Public addresses of this instance: every node that reported one in the last
 * day (cluster mode, see docs/SPEC.md "Edge nodes") plus TUND_SERVER_IP.
 * Custom domains can point A records at any of them; a CNAME to the dashboard
 * host covers all of them, including nodes added later.
 */
export async function serverAddresses(): Promise<string[]> {
  const fixed = config().serverIp;
  const rows = await db()`
    select public_ip from nodes where public_ip <> '' and last_seen > now() - interval '1 day' order by name`;
  return [...new Set([...rows.map((r) => r.public_ip as string), ...(fixed ? [fixed] : [])])];
}
