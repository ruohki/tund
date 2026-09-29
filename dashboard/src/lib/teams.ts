import "server-only";
import { db, notify } from "./db";
import type { User } from "./auth";

// Teams share OIDC providers and domains (docs/SPEC.md "Teams (migration 0005)").

export type TeamRole = "owner" | "admin" | "member";

const RANK: Record<TeamRole, number> = { member: 1, admin: 2, owner: 3 };

export function atLeast(role: TeamRole | null | undefined, min: TeamRole): boolean {
  return !!role && RANK[role] >= RANK[min];
}

export type Team = { id: string; name: string; slug: string; createdAt: string };
export type Membership = { team: Team; role: TeamRole };

export const TEAM_SLUG_RE = /^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/;

export const teamChanged = (id: string) => notify("tund_config", { kind: "team", id });

type Row = Record<string, unknown>;
const toTeam = (r: Row): Team => ({
  id: r.id as string,
  name: r.name as string,
  slug: r.slug as string,
  createdAt: (r.created_at as Date).toISOString(),
});

/** The user's membership in the team with this slug, or null (also for non-members). */
export async function membershipBySlug(userId: string, slug: string): Promise<Membership | null> {
  const [r] = await db()`
    select t.*, m.role from teams t join team_members m on m.team_id = t.id and m.user_id = ${userId}
    where t.slug = ${slug}`;
  return r ? { team: toTeam(r), role: r.role as TeamRole } : null;
}

export async function membershipById(userId: string, teamId: string): Promise<Membership | null> {
  const [r] = await db()`
    select t.*, m.role from teams t join team_members m on m.team_id = t.id and m.user_id = ${userId}
    where t.id = ${teamId}`;
  return r ? { team: toTeam(r), role: r.role as TeamRole } : null;
}

/** Throws a friendly error string when the user lacks `min` in the team; returns the membership otherwise. */
export async function requireTeamRole(user: User, teamId: string, min: TeamRole): Promise<Membership | string> {
  if (!teamId) return "Unknown team.";
  const m = await membershipById(user.id, teamId);
  if (!m) return "You're not a member of this team.";
  if (!atLeast(m.role, min)) return min === "owner" ? "Only team owners can do that." : "Only team owners and admins can do that.";
  return m;
}

export type TeamListItem = Team & { role: TeamRole; members: number; domains: number; providers: number };

export async function listMyTeams(userId: string): Promise<TeamListItem[]> {
  const rows = await db()`
    select t.*, m.role,
      (select count(*)::int from team_members x where x.team_id = t.id) as members,
      (select count(*)::int from domains d where d.team_id = t.id) as domains,
      (select count(*)::int from oidc_providers p where p.team_id = t.id) as providers
    from teams t join team_members m on m.team_id = t.id and m.user_id = ${userId}
    order by t.name`;
  return rows.map((r) => ({
    ...toTeam(r),
    role: r.role as TeamRole,
    members: r.members as number,
    domains: r.domains as number,
    providers: r.providers as number,
  }));
}

// Team-owned addresses: team domains (exact, or one label under a team wildcard)
// and team-reserved TCP ports. TCP tunnels and connections use "host:port" as
// their hostname/address, which HTTP hostnames never contain.
function hostnameMatchesDomain(sql: ReturnType<typeof db>, h: ReturnType<ReturnType<typeof db>["unsafe"]>) {
  return sql`(td.hostname = ${h}
      or (td.hostname like '*.%' and ${h} = split_part(${h}, '.', 1) || substr(td.hostname, 2)))`;
}

/**
 * SQL predicate: `hostnameExpr` is a team-owned address of a team `userId`
 * belongs to. Used to show team traffic.
 */
export function teamHostnameMatch(sql: ReturnType<typeof db>, hostnameExpr: string, userId: string) {
  const h = sql.unsafe(hostnameExpr);
  return sql`(exists (
    select 1 from domains td join team_members tm on tm.team_id = td.team_id and tm.user_id = ${userId}
    where td.team_id is not null and ${hostnameMatchesDomain(sql, h)}
  ) or exists (
    select 1 from tcp_reservations tr join team_members tm on tm.team_id = tr.team_id and tm.user_id = ${userId}
    where tr.team_id is not null and ${h} like '%:' || tr.port::text
  ))`;
}

/** Slug of the team owning an address, for badges. */
export function teamSlugFor(sql: ReturnType<typeof db>, hostnameExpr: string) {
  const h = sql.unsafe(hostnameExpr);
  return sql`coalesce(
    (select tt.slug from domains td join teams tt on tt.id = td.team_id
     where td.team_id is not null and ${hostnameMatchesDomain(sql, h)} limit 1),
    (select tt.slug from tcp_reservations tr join teams tt on tt.id = tr.team_id
     where ${h} like '%:' || tr.port::text limit 1)
  )`;
}

/** Members of the team owning `hostname` (a domain, or "host:port" of a team TCP port), for the live stream. */
export async function teamMembersForHostname(hostname: string): Promise<{ userIds: string[]; slug: string | null }> {
  const sql = db();
  const port = /:(\d{1,5})$/.exec(hostname)?.[1];
  const rows = port
    ? await sql`
        select tm.user_id, tt.slug from tcp_reservations tr
          join teams tt on tt.id = tr.team_id join team_members tm on tm.team_id = tr.team_id
        where tr.port = ${Number(port)}`
    : await sql`
        select tm.user_id, tt.slug from domains td
          join teams tt on tt.id = td.team_id join team_members tm on tm.team_id = td.team_id
        where td.team_id is not null and (
          td.hostname = ${hostname}
          or (td.hostname like '*.%' and ${hostname} = split_part(${hostname}, '.', 1) || substr(td.hostname, 2))
        )`;
  return { userIds: rows.map((r) => r.user_id as string), slug: (rows[0]?.slug as string) ?? null };
}

export type ProviderOption = { id: string; name: string; slug: string; ref: string; team: string | null };

/**
 * Providers a domain's owner may use in its access policy: for a team domain only
 * that team's providers; for a personal domain the user's own plus those of all
 * teams they belong to.
 */
export async function providerOptions(userId: string, teamId: string | null): Promise<ProviderOption[]> {
  const rows = teamId
    ? await db()`
        select p.id, p.name, p.slug, t.slug as team_slug from oidc_providers p join teams t on t.id = p.team_id
        where p.team_id = ${teamId} order by p.name`
    : await db()`
        select p.id, p.name, p.slug, null as team_slug from oidc_providers p
        where p.user_id = ${userId} and p.team_id is null
        union all
        select p.id, p.name, p.slug, t.slug as team_slug from oidc_providers p
          join teams t on t.id = p.team_id
          join team_members m on m.team_id = p.team_id and m.user_id = ${userId}
        order by team_slug nulls first, name`;
  return rows.map((r) => ({
    id: r.id as string,
    name: r.name as string,
    slug: r.slug as string,
    team: (r.team_slug as string) ?? null,
    ref: r.team_slug ? `${r.team_slug}/${r.slug}` : (r.slug as string),
  }));
}
