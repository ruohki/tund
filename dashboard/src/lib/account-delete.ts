import "server-only";
import type { TransactionSql } from "postgres";
import { db, notify } from "./db";

export type DeletedAccount = { email: string; teamsDeleted: string[]; teamsHandedOver: string[] };

/**
 * Deletes an account and everything it owns (the foreign keys cascade) while
 * its teams keep working. What the account created for a team (domains,
 * single sign-on providers, TCP ports) passes to the member who takes over:
 * the longest-standing other owner, else admin, else member, who becomes the
 * owner when there's none left. Teams without another member go with the
 * account. `still` runs on the locked user row and can call the deletion off
 * (the inactive-account cleanup re-checks that nobody signed in meanwhile).
 * Returns null when the account is gone already or `still` said no.
 */
export async function deleteAccount(
  userId: string,
  still?: (tx: TransactionSql) => Promise<boolean>,
): Promise<DeletedAccount | null> {
  const res = await db().begin(async (tx) => {
    const [u] = await tx`select email from users where id = ${userId} for update`;
    if (!u || (still && !(await still(tx)))) return null;
    const teams: string[] = [];
    const teamsDeleted: string[] = [];
    const teamsHandedOver: string[] = [];
    const domains: string[] = [];
    for (const m of await tx`
      select t.id, t.slug from team_members m join teams t on t.id = m.team_id where m.user_id = ${userId}`) {
      const team = m.id as string;
      teams.push(team);
      const [heir] = await tx`
        select user_id, role from team_members where team_id = ${team} and user_id <> ${userId}
        order by case role when 'owner' then 0 when 'admin' then 1 else 2 end, created_at
        limit 1`;
      if (!heir) {
        domains.push(...(await tx`select id from domains where team_id = ${team}`).map((d) => d.id as string));
        await tx`delete from teams where id = ${team}`;
        teamsDeleted.push(m.slug as string);
        continue;
      }
      await tx`update domains set user_id = ${heir.user_id} where team_id = ${team} and user_id = ${userId}`;
      await tx`update oidc_providers set user_id = ${heir.user_id} where team_id = ${team} and user_id = ${userId}`;
      await tx`update tcp_reservations set user_id = ${heir.user_id} where team_id = ${team} and user_id = ${userId}`;
      // Owners sort first, so an heir who isn't one means no owner is left.
      if (heir.role !== "owner") {
        await tx`update team_members set role = 'owner' where team_id = ${team} and user_id = ${heir.user_id}`;
        teamsHandedOver.push(m.slug as string);
      }
    }
    domains.push(...(await tx`select id from domains where user_id = ${userId}`).map((d) => d.id as string));
    await tx`delete from users where id = ${userId}`;
    return { email: u.email as string, teams, teamsDeleted, teamsHandedOver, domains };
  });
  if (!res) return null;
  // The edge disconnects the clients and forgets the hostnames and team policies.
  await notify("tund_config", { kind: "user_disabled", id: userId });
  for (const id of res.teams) await notify("tund_config", { kind: "team", id });
  for (const id of res.domains) await notify("tund_config", { kind: "domain", id });
  return { email: res.email, teamsDeleted: res.teamsDeleted, teamsHandedOver: res.teamsHandedOver };
}
