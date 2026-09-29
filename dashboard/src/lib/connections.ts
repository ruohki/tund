import "server-only";
import { db } from "./db";
import { isUuid } from "./requests";
import { teamHostnameMatch, teamSlugFor } from "./teams";

// Finished TCP/TLS connections (docs/SPEC.md "Connection records"), the raw
// tunnels' counterpart of captured HTTP requests.

export type ConnectionSummary = {
  id: string;
  tunnelId: string | null;
  proto: "tcp" | "tls";
  address: string;
  remoteAddr: string;
  bytesIn: number;
  bytesOut: number;
  durationMs: number;
  error: string;
  startedAt: string;
  teamSlug: string | null;
};

type Row = Record<string, unknown>;

function toSummary(r: Row): ConnectionSummary {
  return {
    id: r.id as string,
    tunnelId: (r.tunnel_id as string) ?? null,
    proto: r.proto as "tcp" | "tls",
    address: r.address as string,
    remoteAddr: r.remote_addr as string,
    bytesIn: Number(r.bytes_in),
    bytesOut: Number(r.bytes_out),
    durationMs: r.duration_ms as number,
    error: r.error as string,
    startedAt: (r.started_at as Date).toISOString(),
    teamSlug: (r.team_slug as string) ?? null,
  };
}

/** Own connections plus those on addresses owned by the user's teams. */
function visible(userId: string) {
  const sql = db();
  return sql`(c.user_id = ${userId} or ${teamHostnameMatch(sql, "c.address", userId)})`;
}

export async function listConnections(
  userId: string,
  f: { address?: string; tunnel?: string; before?: string; beforeId?: string },
  limit = 100,
): Promise<ConnectionSummary[]> {
  const sql = db();
  const before = f.before && !Number.isNaN(Date.parse(f.before)) ? new Date(f.before) : null;
  const rows = await sql`
    select c.*, ${teamSlugFor(sql, "c.address")} as team_slug from connections c
    where ${visible(userId)}
      ${f.address ? sql`and c.address = ${f.address.toLowerCase()}` : sql``}
      ${f.tunnel && isUuid(f.tunnel) ? sql`and c.tunnel_id = ${f.tunnel}` : sql``}
      ${before && isUuid(f.beforeId) ? sql`and (c.started_at, c.id) < (${before}, ${f.beforeId})` : before ? sql`and c.started_at < ${before}` : sql``}
    order by c.started_at desc, c.id desc
    limit ${Math.min(Math.max(limit, 1), 500)}`;
  return rows.map(toSummary);
}

export async function getConnection(userId: string, id: string): Promise<ConnectionSummary | null> {
  if (!isUuid(id)) return null;
  const sql = db();
  const [r] = await sql`select c.*, ${teamSlugFor(sql, "c.address")} as team_slug from connections c where c.id = ${id} and ${visible(userId)}`;
  return r ? toSummary(r) : null;
}

/** Addresses of the user's TCP/TLS tunnels and connections, for the inspector's filter. */
export async function connectionAddresses(userId: string): Promise<string[]> {
  const sql = db();
  const rows = await sql`
    select address from (
      select c.address, max(c.started_at) as last from connections c where ${visible(userId)} group by c.address
      union all
      select t.hostname as address, max(t.started_at) as last from tunnels t
        where t.proto <> 'http' and (t.user_id = ${userId} or ${teamHostnameMatch(sql, "t.hostname", userId)})
        group by t.hostname
    ) a group by address order by max(last) desc limit 100`;
  return rows.map((r) => r.address as string);
}

export async function deleteConnections(userId: string, address?: string): Promise<number> {
  const res = await db()`
    delete from connections where user_id = ${userId} ${address ? db()`and address = ${address.toLowerCase()}` : db()``}`;
  return res.count;
}
