import "server-only";
import { PASSTHROUGH_OFF, passthroughEnabled } from "./abuse";
import type { User } from "./auth";
import { db, notify } from "./db";
import { edgeStatus } from "./admin-stats";
import { lockAndCheckLimit, type DomainOwner } from "./static-hostnames";

// Static TCP ports (tcp_reservations): personal or team-owned; together with
// static hostnames they count as "static addresses" (limit_pinned).

export type TcpConfig = { from: number; to: number; host: string } | null;

const g = globalThis as unknown as { __tundTcp?: { at: number; value: TcpConfig } };

/** The server's TCP port range and host from GET /internal/status; null = TCP tunnels are off. */
export async function tcpConfig(): Promise<TcpConfig> {
  if (g.__tundTcp && Date.now() - g.__tundTcp.at < 30_000) return g.__tundTcp.value;
  const { status } = await edgeStatus();
  const r = status?.tcp_ports;
  const value = r && r.from > 0 && r.to >= r.from ? { from: r.from, to: r.to, host: status?.tcp_host || status?.dashboard_host || "" } : null;
  // Don't cache a failed lookup for long; the server may just be restarting.
  g.__tundTcp = { at: status ? Date.now() : Date.now() - 25_000, value };
  return value;
}

export type TcpReservation = { port: number; online: boolean; createdAt: string; teamSlug: string | null };

export async function listTcpReservations(userId: string, owner: DomainOwner): Promise<TcpReservation[]> {
  const sql = db();
  const rows = await sql`
    select r.port, r.created_at, t.slug as team_slug,
      exists(select 1 from tunnels x where x.ended_at is null and x.proto = 'tcp' and x.remote_port = r.port) as online
    from tcp_reservations r left join teams t on t.id = r.team_id
    where ${owner.teamId ? sql`r.team_id = ${owner.teamId}` : sql`r.user_id = ${userId} and r.team_id is null`}
    order by r.port`;
  return rows.map((r) => ({
    port: r.port as number,
    online: Boolean(r.online),
    createdAt: (r.created_at as Date).toISOString(),
    teamSlug: (r.team_slug as string) ?? null,
  }));
}

export type ReserveResult = { ok: true; port: number } | { ok: false; error: string };

async function changed(port: number, owner: DomainOwner) {
  await notify("tund_config", { kind: "tcp_reservation", port });
  if (owner.teamId) await notify("tund_config", { kind: "team", id: owner.teamId });
}

/** Reserves `port` (or a random free one) in the server's range for the user or team. */
export async function reserveTcpPort(user: User, owner: DomainOwner, port: number | null): Promise<ReserveResult> {
  const cfg = await tcpConfig();
  if (!cfg) return { ok: false, error: "TCP tunnels are not enabled on this server." };
  if (!(await passthroughEnabled(user))) return { ok: false, error: PASSTHROUGH_OFF };
  if (port !== null && (!Number.isInteger(port) || port < cfg.from || port > cfg.to)) {
    return { ok: false, error: `Pick a port between ${cfg.from} and ${cfg.to}.` };
  }
  try {
    const res = await db().begin(async (tx): Promise<ReserveResult> => {
      const limitError = await lockAndCheckLimit(tx, user, "subdomain", owner);
      if (limitError) return { ok: false, error: limitError };
      let chosen = port;
      if (chosen === null) {
        const [free] = await tx`
          select p from generate_series(${cfg.from}::int, ${cfg.to}::int) p
          where not exists (select 1 from tcp_reservations r where r.port = p)
            and not exists (select 1 from tunnels x where x.ended_at is null and x.proto = 'tcp' and x.remote_port = p)
          order by random() limit 1`;
        if (!free) return { ok: false, error: "Every port in the server's range is taken." };
        chosen = free.p as number;
      } else {
        const [taken] = await tx`select user_id, team_id from tcp_reservations where port = ${chosen}`;
        if (taken) return { ok: false, error: `Port ${chosen} is already reserved.` };
        const [busy] = await tx`
          select 1 from tunnels where ended_at is null and proto = 'tcp' and remote_port = ${chosen} and user_id <> ${user.id}`;
        if (busy) return { ok: false, error: `Port ${chosen} is in use by another account's tunnel right now.` };
      }
      await tx`insert into tcp_reservations (port, user_id, team_id) values (${chosen}, ${user.id}, ${owner.teamId})`;
      return { ok: true, port: chosen };
    });
    if (res.ok) await changed(res.port, owner);
    return res;
  } catch (err) {
    if ((err as { code?: string }).code === "23505") return { ok: false, error: "That port was just taken. Try again." };
    throw err;
  }
}

/** Releases a reservation of the user (personal) or of the team (caller checked the team role). */
export async function releaseTcpPort(user: User, owner: DomainOwner, port: number): Promise<boolean> {
  const sql = db();
  const res = await sql`
    delete from tcp_reservations
    where port = ${port} and ${owner.teamId ? sql`team_id = ${owner.teamId}` : sql`user_id = ${user.id} and team_id is null`}`;
  if (res.count) await changed(port, owner);
  return res.count > 0;
}
