import "server-only";
import { db } from "./db";
import type { DomainItem } from "@/app/(app)/domains/domain-forms";

/** Domains shown on /domains (personal, teamId null) or on a team page (that team's domains). */
export async function loadDomainItems(userId: string, teamId: string | null): Promise<DomainItem[]> {
  const sql = db();
  const rows = await sql`
    select d.*, p.name as provider_name,
      exists(select 1 from tunnels t where t.ended_at is null
               ${teamId ? sql`` : sql`and t.user_id = d.user_id`}
               and (t.hostname = d.hostname
                    or (d.hostname like '*.%' and t.hostname = split_part(t.hostname, '.', 1) || substr(d.hostname, 2)))) as online
    from domains d left join oidc_providers p on p.id = d.auth_oidc_provider_id
    where ${teamId ? sql`d.team_id = ${teamId}` : sql`d.user_id = ${userId} and d.team_id is null`}
    order by d.is_default desc, d.created_at desc`;
  return rows.map((r) => ({
    id: r.id,
    hostname: r.hostname,
    kind: r.kind,
    verified: Boolean(r.verified_at),
    token: r.verification_token,
    authMode: r.auth_mode,
    hasPassword: Boolean(r.auth_password_hash),
    providerId: r.auth_oidc_provider_id ?? "",
    providerName: r.provider_name ?? "",
    allow: r.auth_oidc_allow ?? [],
    online: Boolean(r.online),
    isDefault: Boolean(r.is_default),
    teamId: r.team_id ?? null,
    createdAt: (r.created_at as Date).toISOString(),
  }));
}

export type TeamDomainSummary = { hostname: string; kind: string; teamSlug: string; teamName: string; online: boolean };

/** Domains owned by the user's teams, for the "Team domains" list on /domains. */
export async function myTeamDomains(userId: string): Promise<TeamDomainSummary[]> {
  const rows = await db()`
    select d.hostname, d.kind, t.slug, t.name,
      exists(select 1 from tunnels x where x.ended_at is null and x.hostname = d.hostname) as online
    from domains d join teams t on t.id = d.team_id join team_members m on m.team_id = t.id and m.user_id = ${userId}
    order by t.name, d.kind desc, d.hostname`;
  return rows.map((r) => ({
    hostname: r.hostname as string,
    kind: r.kind as string,
    teamSlug: r.slug as string,
    teamName: r.name as string,
    online: Boolean(r.online),
  }));
}
