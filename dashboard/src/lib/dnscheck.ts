import "server-only";
import { Resolver } from "node:dns/promises";

export type RoutingStatus =
  | { state: "ok"; addresses: string[] }
  | { state: "elsewhere"; addresses: string[] }
  | { state: "missing" }
  | { state: "unknown"; reason: string };

function resolver() {
  // Ask public resolvers so a freshly created record is seen without waiting on local caches.
  const r = new Resolver({ timeout: 4000, tries: 2 });
  r.setServers(["1.1.1.1", "8.8.8.8", "9.9.9.9"]);
  return r;
}

/** Hostname that carries the TXT challenge for a (possibly wildcard) domain. */
export function challengeName(hostname: string) {
  return `_tund-challenge.${hostname.replace(/^\*\./, "")}`;
}

export async function checkTxt(hostname: string, token: string): Promise<{ found: boolean; values: string[]; error?: string }> {
  try {
    const records = await resolver().resolveTxt(challengeName(hostname));
    const values = records.map((chunks) => chunks.join(""));
    return { found: values.includes(`tund-verify=${token}`), values };
  } catch (err) {
    const code = (err as { code?: string }).code;
    if (code === "ENOTFOUND" || code === "ENODATA") return { found: false, values: [] };
    return { found: false, values: [], error: `DNS lookup failed (${code ?? String(err)}).` };
  }
}

/** Whether the domain currently resolves to this server (informational). */
export async function checkRouting(hostname: string, serverIp: string): Promise<RoutingStatus> {
  const probe = hostname.startsWith("*.") ? `tund-probe.${hostname.slice(2)}` : hostname;
  const r = resolver();
  try {
    const [v4, v6] = await Promise.all([r.resolve4(probe).catch(() => []), r.resolve6(probe).catch(() => [])]);
    const addresses = [...v4, ...v6];
    if (!addresses.length) return { state: "missing" };
    if (!serverIp) return { state: "unknown", reason: "TUND_SERVER_IP is not configured, so the address can't be compared." };
    return addresses.includes(serverIp) ? { state: "ok", addresses } : { state: "elsewhere", addresses };
  } catch (err) {
    return { state: "unknown", reason: err instanceof Error ? err.message : String(err) };
  }
}
