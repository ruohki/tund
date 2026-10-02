import "server-only";
import { Resolver } from "node:dns/promises";

export type RoutingStatus =
  /** via "cname": follows every server; via "address": `missing` lists servers the A records leave out. */
  | { state: "ok"; addresses: string[]; via: "cname" | "address"; missing: string[] }
  | { state: "elsewhere"; addresses: string[]; foreign: string[] }
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

/**
 * Whether the domain reaches this instance (informational): through a CNAME to
 * one of `hosts` (the dashboard host or a name under the base domain), or with
 * addresses that all belong to the instance.
 */
export async function checkRouting(
  hostname: string,
  target: { addresses: string[]; hosts: string[] },
): Promise<RoutingStatus> {
  const probe = hostname.startsWith("*.") ? `tund-probe.${hostname.slice(2)}` : hostname;
  const r = resolver();
  try {
    const [v4, v6, cname] = await Promise.all([
      r.resolve4(probe).catch(() => []),
      r.resolve6(probe).catch(() => []),
      r.resolveCname(probe).catch(() => []),
    ]);
    const addresses = [...v4, ...v6];
    const to = cname.map((h) => h.toLowerCase().replace(/\.$/, ""));
    if (to.some((h) => target.hosts.some((ours) => h === ours || h.endsWith(`.${ours}`)))) {
      return { state: "ok", addresses, via: "cname", missing: [] };
    }
    if (!addresses.length) return { state: "missing" };
    if (!target.addresses.length) {
      return { state: "unknown", reason: "No address of this server is known (set TUND_SERVER_IP), so the records can't be compared." };
    }
    const foreign = addresses.filter((a) => !target.addresses.includes(a));
    if (foreign.length) return { state: "elsewhere", addresses, foreign };
    return { state: "ok", addresses, via: "address", missing: target.addresses.filter((a) => !addresses.includes(a)) };
  } catch (err) {
    return { state: "unknown", reason: err instanceof Error ? err.message : String(err) };
  }
}
