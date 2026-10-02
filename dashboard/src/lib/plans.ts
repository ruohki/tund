import "server-only";
import { db } from "./db";
import { getSettings, type Settings } from "./settings";

// Plans (docs/SPEC.md "Plans and billing"). Free accounts get the instance
// limits, Pro accounts the pro_* settings plus custom domains and TCP/TLS
// tunnels. Whether an account is Pro is decided in Postgres (user_is_pro, the
// same function the edge uses). Per-account overrides still win over the plan.

export type TeamPlan = "team" | "team_pro";

export const PLAN_LABEL: Record<"free" | "pro" | TeamPlan, string> = {
  free: "Free",
  pro: "Pro",
  team: "Team",
  team_pro: "Team Pro",
};

/** Limits before per-account overrides; 0 = unlimited. */
export type AccountPlan = {
  pro: boolean;
  tunnels: number;
  pinned: number;
  domains: number;
  teams: number;
  bandwidthKbps: number;
  transferGb: number;
  lifetimeMinutes: number;
  customDomains: boolean;
  passthrough: boolean;
};

/** A Pro limit is never below the Free one (0 = unlimited). Mirrors noLower in the edge. */
const noLower = (free: number, pro: number) => (free === 0 || pro === 0 ? 0 : Math.max(free, pro));

export function planLimits(s: Settings, pro: boolean): AccountPlan {
  return pro
    ? {
        pro,
        tunnels: noLower(s.limit_tunnels, s.pro_limit_tunnels),
        pinned: noLower(s.limit_pinned, s.pro_limit_pinned),
        // Free's domain count only matters when Free has custom domains at all.
        domains: s.custom_domains ? noLower(s.limit_domains, s.pro_limit_domains) : s.pro_limit_domains,
        teams: noLower(s.limit_teams, s.pro_limit_teams),
        bandwidthKbps: noLower(s.limit_bandwidth_kbps, s.pro_limit_bandwidth_kbps),
        transferGb: noLower(s.limit_transfer_gb, s.pro_limit_transfer_gb),
        lifetimeMinutes: noLower(s.limit_tunnel_lifetime, s.pro_limit_tunnel_lifetime),
        customDomains: true,
        passthrough: true,
      }
    : {
        pro,
        tunnels: s.limit_tunnels,
        pinned: s.limit_pinned,
        domains: s.limit_domains,
        teams: s.limit_teams,
        bandwidthKbps: s.limit_bandwidth_kbps,
        transferGb: s.limit_transfer_gb,
        lifetimeMinutes: s.limit_tunnel_lifetime,
        customDomains: s.custom_domains,
        passthrough: s.passthrough,
      };
}

export async function isPro(userId: string): Promise<boolean> {
  const [row] = await db()`select user_is_pro(${userId}::uuid) as pro`;
  return Boolean(row?.pro);
}

export async function accountPlan(userId: string): Promise<AccountPlan> {
  const [s, pro] = await Promise.all([getSettings(), isPro(userId)]);
  return planLimits(s, pro);
}

/** Billing is on when an admin enabled it and a Stripe key is set (Admin → Billing). */
export async function billingEnabled(): Promise<boolean> {
  const b = (await getSettings()).billing;
  return b.enabled && Boolean(b.secret_key_enc);
}

export async function teamPlan(teamId: string): Promise<TeamPlan | null> {
  const [row] = await db()`select team_plan(${teamId}::uuid) as plan`;
  return (row?.plan as TeamPlan | null) ?? null;
}

export type TeamBilling = {
  plan: TeamPlan | null;
  /** Granted by an administrator (billing exempt). */
  granted: boolean;
  /** Members allowed; null = unlimited (billing off). */
  seats: number | null;
  members: number;
  /** Extra seat packs on the subscription. */
  seatPacks: number;
  /** Custom domains the team may have; null = the per-account rules (billing off). */
  customDomains: number | null;
};

/**
 * What a team may do. With billing off, teams work as before (no seat or
 * domain limits from plans). With billing on, a team without a plan is just
 * its owner; Team and Team Pro include team_seats members plus the packs
 * bought, and team_custom_domains custom domains.
 */
export async function teamBilling(teamId: string): Promise<TeamBilling> {
  const [s, on, [row]] = await Promise.all([
    getSettings(),
    billingEnabled(),
    db()`
      select team_plan(t.id) as plan, t.plan_granted, t.seats_override,
        (select count(*)::int from team_members m where m.team_id = t.id) as members,
        coalesce((select max(seat_packs) from subscriptions s
          where s.team_id = t.id and subscription_active(s.status)), 0) as packs
      from teams t where t.id = ${teamId}`,
  ]);
  const plan = (row?.plan as TeamPlan | null) ?? null;
  const packs = Number(row?.packs ?? 0);
  const override = row?.seats_override as number | null;
  const seats = !on && override === null ? null : (override ?? (plan ? s.team_seats + packs * s.team_seat_pack : 1));
  return {
    plan,
    granted: Boolean(row?.plan_granted),
    seats,
    members: Number(row?.members ?? 0),
    seatPacks: packs,
    customDomains: on ? (plan ? s.team_custom_domains : 0) : null,
  };
}

/** Why the team can't take another member, or null. */
export async function seatRefusal(teamId: string): Promise<string | null> {
  const b = await teamBilling(teamId);
  if (b.seats === null || b.members < b.seats) return null;
  return b.plan
    ? `This team uses all ${b.seats} of its seats. Add more seats on the team page.`
    : "Teams need a Team plan to add members. Subscribe on the team page.";
}

/** Where an account's Pro comes from (the same rules as user_is_pro). */
export async function proSources(userId: string): Promise<{
  granted: boolean;
  subscription: boolean;
  teams: { slug: string; name: string; plan: TeamPlan }[];
}> {
  const sql = db();
  const [[u], teams] = await Promise.all([
    sql`
      select pro_granted, exists (select 1 from subscriptions s
        where s.user_id = ${userId} and s.plan = 'pro' and subscription_active(s.status)) as sub
      from users where id = ${userId}`,
    sql`
      select t.slug, t.name, team_plan(t.id) as plan from teams t join team_members m on m.team_id = t.id
      where m.user_id = ${userId} and (
        team_plan(t.id) = 'team_pro'
        or (m.role = 'owner' and t.plan_granted = 'team')
        or exists (select 1 from subscriptions s where s.team_id = t.id and s.user_id = ${userId}
          and s.plan = 'team' and subscription_active(s.status) and m.role = 'owner'))
      order by t.name`,
  ]);
  return {
    granted: Boolean(u?.pro_granted),
    subscription: Boolean(u?.sub),
    teams: teams.map((t) => ({ slug: t.slug as string, name: t.name as string, plan: t.plan as TeamPlan })),
  };
}
