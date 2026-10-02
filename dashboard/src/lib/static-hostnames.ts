import "server-only";
import type { TransactionSql } from "postgres";
import type { User } from "./auth";
import { config } from "./config";
import { getSettings } from "./settings";
import { db, notify } from "./db";
import { randomLabel } from "./names";
import { checkSubdomain } from "./validate";
import { blockedHostReason, labelRefusal } from "./abuse";
import { accountPlan, planLimits, teamBilling } from "./plans";

// Static hostnames: base-domain subdomains pinned to an account or a team
// (domains rows with kind='subdomain'). See docs/SPEC.md "Static hostnames
// (migration 0003)" and "Teams (migration 0005)". The default static hostname
// is a personal concept only.

export type PinResult = { ok: true; id: string; hostname: string; isDefault: boolean } | { ok: false; error: string };

type Tx = TransactionSql;

/** Personal domains have no team; team domains are scoped by team id. */
export type DomainOwner = { teamId: string | null };
const PERSONAL: DomainOwner = { teamId: null };

export async function domainChanged(id: string, owner: DomainOwner = PERSONAL) {
  await notify("tund_config", { kind: "domain", id });
  if (owner.teamId) await notify("tund_config", { kind: "team", id: owner.teamId });
}

function isUniqueViolation(err: unknown) {
  return (err as { code?: string }).code === "23505";
}

function scope(sql: Tx | ReturnType<typeof db>, user: User, owner: DomainOwner) {
  return owner.teamId ? sql`team_id = ${owner.teamId}` : sql`user_id = ${user.id} and team_id is null`;
}

export const TEAM_DOMAINS_NEED_PLAN = "Custom domains for teams come with a Team plan. Subscribe on the team page.";

/**
 * The limit for `kind` (0 = unlimited) from the plan: the account's (Free or
 * Pro), or for team domains the team's. With billing on, team custom domains
 * need a team plan (a refusal string otherwise) and follow team_custom_domains.
 */
async function maxFor(user: User, kind: "subdomain" | "custom", owner: DomainOwner): Promise<number | string> {
  if (owner.teamId) {
    const b = await teamBilling(owner.teamId);
    if (kind === "custom" && b.customDomains !== null) {
      if (!b.plan) return TEAM_DOMAINS_NEED_PLAN;
      return b.customDomains;
    }
    const p = planLimits(await getSettings(), b.plan !== null);
    return kind === "subdomain" ? p.pinned : p.domains;
  }
  const p = await accountPlan(user.id);
  return kind === "subdomain" ? p.pinned : p.domains;
}

/**
 * Counts toward a limit. "subdomain" is the static-addresses budget: static
 * hostnames plus reserved TCP ports (docs/SPEC.md "TCP and TLS tunnels").
 */
async function countFor(q: Tx | ReturnType<typeof db>, user: User, kind: "subdomain" | "custom", owner: DomainOwner) {
  const [row] = await q`select count(*)::int as n from domains where ${scope(q, user, owner)} and kind = ${kind}`;
  let n = row.n as number;
  if (kind === "subdomain") {
    const [t] = await q`select count(*)::int as n from tcp_reservations where ${scope(q, user, owner)}`;
    n += t.n as number;
  }
  return n;
}

/**
 * Usage for `kind` (per account, or per team for team domains); limit null =
 * unlimited (0 or admin); locked = why none can be added at all.
 */
export async function domainUsage(
  user: User,
  kind: "subdomain" | "custom",
  owner: DomainOwner = PERSONAL,
): Promise<{ used: number; limit: number | null; locked: string | null }> {
  const max = await maxFor(user, kind, owner);
  const used = await countFor(db(), user, kind, owner);
  if (typeof max === "string") return { used, limit: 0, locked: max };
  return { used, limit: max > 0 && !user.isAdmin ? max : null, locked: null };
}

/**
 * Checks the limit inside a transaction that holds the owner's row lock (user or
 * team), so two concurrent requests can't both take the last slot.
 */
export async function lockAndCheckLimit(
  tx: Tx,
  user: User,
  kind: "subdomain" | "custom",
  owner: DomainOwner = PERSONAL,
): Promise<string | null> {
  if (owner.teamId) await tx`select 1 from teams where id = ${owner.teamId} for update`;
  else await tx`select 1 from users where id = ${user.id} for update`;
  const max = await maxFor(user, kind, owner);
  if (typeof max === "string") return max;
  if (max <= 0 || user.isAdmin) return null;
  if ((await countFor(tx, user, kind, owner)) < max) return null;
  const who = owner.teamId ? "this team" : "your account";
  return kind === "subdomain"
    ? `You're using all ${max} static ${max === 1 ? "address" : "addresses"} (static hostnames and TCP ports) ${who} can have. Release one to add another.`
    : `You're using all ${max} custom ${max === 1 ? "domain" : "domains"} ${who} can have. Remove one to add another.`;
}

/** Pins `label` under the base domain. A user's first personal static hostname becomes their default. */
export async function pinLabel(user: User, label: string, owner: DomainOwner = PERSONAL): Promise<PinResult> {
  const c = config();
  const invalid = checkSubdomain(label, c.dashboardHost);
  if (invalid) return { ok: false, error: invalid };
  const refusal = await labelRefusal(user, label);
  if (refusal) return { ok: false, error: refusal };
  const hostname = `${label}.${c.baseDomain}`;
  if (await blockedHostReason(hostname)) return { ok: false, error: `${hostname} has been blocked on this server.` };
  try {
    const res = await db().begin(async (tx): Promise<PinResult> => {
      const limitError = await lockAndCheckLimit(tx, user, "subdomain", owner);
      if (limitError) return { ok: false, error: limitError };
      const [existing] = await tx`select user_id, team_id from domains where hostname = ${hostname}`;
      if (existing) {
        const mine = owner.teamId ? existing.team_id === owner.teamId : existing.user_id === user.id && !existing.team_id;
        return { ok: false, error: mine ? `${hostname} is already pinned here.` : `${hostname} is taken by another account or team.` };
      }
      // The user's own online tunnel on this name is fine (that's how pinning from /tunnels works).
      const [busy] = await tx`select 1 from tunnels where hostname = ${hostname} and ended_at is null and user_id <> ${user.id}`;
      if (busy) return { ok: false, error: `${hostname} is in use by another account's tunnel right now.` };
      let isDefault = false;
      if (!owner.teamId) {
        const [hasDefault] = await tx`select 1 from domains where user_id = ${user.id} and team_id is null and is_default`;
        isDefault = !hasDefault;
      }
      const [row] = await tx`
        insert into domains (user_id, team_id, hostname, kind, verified_at, is_default)
        values (${user.id}, ${owner.teamId}, ${hostname}, 'subdomain', now(), ${isDefault})
        returning id, is_default`;
      return { ok: true, id: row.id as string, hostname, isDefault: row.is_default as boolean };
    });
    if (res.ok) await domainChanged(res.id, owner);
    return res;
  } catch (err) {
    if (isUniqueViolation(err)) return { ok: false, error: `${hostname} was just taken by another account.` };
    throw err;
  }
}

/** Pins a fresh random label, retrying if one happens to be taken. */
export async function pinRandom(user: User, owner: DomainOwner = PERSONAL): Promise<PinResult> {
  let last: PinResult = { ok: false, error: "Couldn't find a free name. Try again." };
  for (let i = 0; i < 6; i++) {
    last = await pinLabel(user, randomLabel(), owner);
    if (last.ok || !/taken|in use|already|not allowed/.test(last.error)) return last;
  }
  return last;
}

/** Makes one of the user's personal static hostnames the default (clear old + set new in one transaction). */
export async function makeDefault(user: User, id: string): Promise<boolean> {
  const changed = await db().begin(async (tx) => {
    await tx`select 1 from users where id = ${user.id} for update`;
    const [target] = await tx`
      select id from domains where id = ${id} and user_id = ${user.id} and team_id is null and kind = 'subdomain'`;
    if (!target) return [] as string[];
    const old = await tx`
      update domains set is_default = false where user_id = ${user.id} and is_default and id <> ${id} returning id`;
    await tx`update domains set is_default = true where id = ${id}`;
    return [id, ...old.map((r) => r.id as string)];
  });
  for (const d of changed) await domainChanged(d);
  return changed.length > 0;
}

/**
 * Deletes a domain of the owner. When the personal default static hostname goes,
 * the oldest remaining one takes over so `tund http <port>` keeps a stable URL.
 * Callers check team roles before passing a team owner.
 */
export async function removeDomain(user: User, id: string, owner: DomainOwner = PERSONAL): Promise<void> {
  const changed = await db().begin(async (tx) => {
    if (owner.teamId) await tx`select 1 from teams where id = ${owner.teamId} for update`;
    else await tx`select 1 from users where id = ${user.id} for update`;
    const [gone] = await tx`delete from domains where id = ${id} and ${scope(tx, user, owner)} returning id, kind, is_default`;
    if (!gone) return [] as string[];
    const ids = [id];
    if (gone.is_default) {
      const [next] = await tx`
        update domains set is_default = true
        where id = (select id from domains where user_id = ${user.id} and team_id is null and kind = 'subdomain'
                    order by created_at limit 1)
        returning id`;
      if (next) ids.push(next.id as string);
    }
    return ids;
  });
  for (const d of changed) await domainChanged(d, owner);
}

/** The account's default static hostname, if any. */
export async function defaultStaticHostname(userId: string): Promise<string | null> {
  const [row] = await db()`select hostname from domains where user_id = ${userId} and team_id is null and is_default limit 1`;
  return (row?.hostname as string) ?? null;
}
