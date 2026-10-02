import "server-only";
import { db } from "./db";
import { getSettings } from "./settings";

// Monthly transfer and throughput limits (docs/SPEC.md "Bandwidth limits").
// Quotas are decimal: 1 GB = 10^9 bytes, counting both directions.

export const GB = 1_000_000_000;

export type MonthUsage = {
  bytesIn: number;
  bytesOut: number;
  requests: number;
  connections: number;
  periodStart: string;
  periodEnd: string;
};

/** First day of this month and of the next one, in UTC. */
export function monthPeriod(now = new Date()) {
  const start = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1));
  const end = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + 1, 1));
  return { start, end };
}

export async function monthUsage(userId: string): Promise<MonthUsage> {
  const { start, end } = monthPeriod();
  const [r] = await db()`
    select coalesce(sum(bytes_in), 0)::bigint as bytes_in, coalesce(sum(bytes_out), 0)::bigint as bytes_out,
      coalesce(sum(requests), 0)::bigint as requests, coalesce(sum(connections), 0)::bigint as connections
    from usage_daily where user_id = ${userId} and day >= ${start} and day < ${end}`;
  return {
    bytesIn: Number(r.bytes_in),
    bytesOut: Number(r.bytes_out),
    requests: Number(r.requests),
    connections: Number(r.connections),
    periodStart: start.toISOString(),
    periodEnd: end.toISOString(),
  };
}

export type DayUsage = { day: string; bytesIn: number; bytesOut: number };

/** Every day of the current month (UTC), zero-filled, for the usage chart. */
export async function dailyUsage(userId: string | null): Promise<DayUsage[]> {
  const { start, end } = monthPeriod();
  const sql = db();
  const rows = await sql`
    select to_char(d, 'YYYY-MM-DD') as day,
      coalesce(sum(u.bytes_in), 0)::bigint as bytes_in, coalesce(sum(u.bytes_out), 0)::bigint as bytes_out
    from generate_series(${start}::date, (${end}::date - 1), interval '1 day') d
    left join usage_daily u on u.day = d::date ${userId ? sql`and u.user_id = ${userId}` : sql``}
    group by d order by d`;
  return rows.map((r) => ({ day: r.day as string, bytesIn: Number(r.bytes_in), bytesOut: Number(r.bytes_out) }));
}

export type Limits = {
  /** kbit/s per direction; 0 = unlimited. */
  bandwidthKbps: number;
  /** GB per month; 0 = unlimited. */
  transferGb: number;
  /** Minutes a tunnel may live; 0 = unlimited. */
  lifetimeMinutes: number;
  bandwidthOverride: number | null;
  transferOverride: number | null;
  lifetimeOverride: number | null;
};

/** Per-user overrides win; otherwise instance defaults, which admins are exempt from. */
export async function effectiveLimits(user: { id: string; isAdmin: boolean }): Promise<Limits> {
  const [s, [u]] = await Promise.all([
    getSettings(),
    db()`select bandwidth_kbps, transfer_quota_gb, tunnel_lifetime_minutes from users where id = ${user.id}`,
  ]);
  const bo = (u?.bandwidth_kbps as number | null) ?? null;
  const to = (u?.transfer_quota_gb as number | null) ?? null;
  const lo = (u?.tunnel_lifetime_minutes as number | null) ?? null;
  return {
    bandwidthKbps: bo ?? (user.isAdmin ? 0 : s.limit_bandwidth_kbps),
    transferGb: to ?? (user.isAdmin ? 0 : s.limit_transfer_gb),
    lifetimeMinutes: lo ?? (user.isAdmin ? 0 : s.limit_tunnel_lifetime),
    bandwidthOverride: bo,
    transferOverride: to,
    lifetimeOverride: lo,
  };
}
