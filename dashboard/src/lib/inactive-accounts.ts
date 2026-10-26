import "server-only";
import type { Fragment, Sql, TransactionSql } from "postgres";
import { deleteAccount } from "./account-delete";
import { systemAudit } from "./audit";
import { config } from "./config";
import { db } from "./db";
import { inactiveAccountEmail, smtpConfigured, trySendMail, verificationRequired } from "./mail";
import { getSettings } from "./settings";

// Deletes accounts nobody uses any more (docs/SPEC.md "Inactive accounts"),
// once an admin turned it on under Admin → Settings:
// * inactive_account_months: no dashboard visit, token use or connected
//   client (users.last_seen_at) for that long. The owner is emailed
//   NOTICE_DAYS before; any activity after the notice keeps the account.
//   Without email nothing is deleted, so nobody loses an account unwarned.
// * unverified_account_days: never confirmed the address, counted from the
//   last confirmation link (only while verification is required).
// Admins, disabled accounts (kept as the record of why), accounts with a
// subscription that isn't over and accounts with Pro granted are never
// deleted this way. Every dashboard runs it hourly; row locks keep two of
// them from mailing or deleting twice. The same run applies the audit log
// retention (audit_retention_days).

export const NOTICE_DAYS = 30;
const FIRST_RUN_MS = 5 * 60_000;
const EVERY_MS = 60 * 60_000;
const BATCH = 50;

const g = globalThis as unknown as { __tundAccountCleanup?: boolean };

type Q = Sql | TransactionSql;

/** Accounts the cleanup may touch at all: none with a subscription Stripe could still charge. */
const deletable = (q: Q) => q`
  not x.is_admin and x.disabled_at is null and not x.pro_granted
  and not exists (select 1 from subscriptions s where s.user_id = x.id and s.status not in ('canceled', 'incomplete_expired'))`;

/** Unused since `months` ago (minus `daysEarly`), with no client connected right now. */
const idle = (q: Q, months: number, daysEarly = 0) => q`
  x.last_seen_at < now() - make_interval(months => ${months}) + make_interval(days => ${daysEarly})
  and not exists (select 1 from agent_sessions a where a.user_id = x.id and a.disconnected_at is null)`;

/** Warned at least NOTICE_DAYS ago, and nothing happened since. */
const warned = (q: Q) => q`
  x.inactivity_notice_at >= x.last_seen_at and x.inactivity_notice_at < now() - make_interval(days => ${NOTICE_DAYS})`;

/** Unconfirmed, and the last confirmation link went out `days` ago or earlier (links last 24 h). */
const unconfirmed = (q: Q, days: number) => q`
  x.email_verified_at is null
  and not exists (select 1 from user_identities i where i.user_id = x.id)
  and exists (select 1 from email_tokens t where t.user_id = x.id and t.kind = 'verify')
  and not exists (select 1 from email_tokens t where t.user_id = x.id and t.kind = 'verify'
    and t.expires_at - interval '24 hours' > now() - make_interval(days => ${days}))`;

const day = (d: Date) => d.toLocaleDateString("en-GB", { timeZone: "UTC", day: "numeric", month: "long", year: "numeric" });

/** Emails the owners of accounts that reach the limit within NOTICE_DAYS. */
async function sendNotices(months: number) {
  const sql = db();
  // Claims the rows first, so another dashboard running at the same time skips them.
  const rows = await sql`
    update users u set inactivity_notice_at = now()
    where u.id in (
      select x.id from users x
      where ${deletable(sql)} and ${idle(sql, months, NOTICE_DAYS)}
        and (x.inactivity_notice_at is null or x.inactivity_notice_at < x.last_seen_at)
      order by x.last_seen_at
      limit ${BATCH}
      for update skip locked)
    returning u.id, u.email, u.last_seen_at`;
  if (!rows.length) return;
  const instance = (await getSettings()).instance_name;
  const deleteOn = day(new Date(Date.now() + NOTICE_DAYS * 86_400_000));
  for (const r of rows) {
    const mail = inactiveAccountEmail(instance, {
      email: r.email as string,
      lastSeen: day(r.last_seen_at as Date),
      months,
      deleteOn,
      url: `${config().dashboardUrl}/login`,
    });
    if (await trySendMail(r.email as string, mail)) {
      await systemAudit("user.inactivity_notice", r.email as string, { last_seen: r.last_seen_at, delete_on: deleteOn });
    } else {
      // Try again next run: the deletion waits for a notice that went out.
      await sql`update users set inactivity_notice_at = null where id = ${r.id}`;
    }
  }
}

async function deleteWhere(action: string, find: (q: Q) => Fragment) {
  const sql = db();
  const rows = await sql`select x.id, x.email, x.last_seen_at from users x where ${find(sql)} order by x.created_at limit ${BATCH}`;
  for (const r of rows) {
    // Checked again on the locked row: someone may have signed in meanwhile.
    const gone = await deleteAccount(r.id as string, async (tx) => {
      const [ok] = await tx`select 1 from users x where x.id = ${r.id} and ${find(tx)}`;
      return Boolean(ok);
    });
    if (gone) {
      await systemAudit(action, gone.email, {
        last_seen: r.last_seen_at,
        teams_deleted: gone.teamsDeleted,
        teams_handed_over: gone.teamsHandedOver,
      });
    }
  }
}

/** Deletes audit entries, and the reporter details of abuse reports closed, `days` ago. */
async function pruneAuditLog(days: number) {
  const sql = db();
  await sql`delete from audit_log where created_at < now() - make_interval(days => ${days})`;
  await sql`
    update abuse_reports set reporter_email = '', reporter_ip = ''
    where status <> 'open' and resolved_at < now() - make_interval(days => ${days})
      and (reporter_email <> '' or reporter_ip <> '')`;
}

/**
 * Invites stop working 7 days after they're sent; 30 days later they're
 * deleted, used or not. Until then the invite page can still say "already
 * used" or "expired".
 */
async function pruneInvites() {
  await db()`delete from team_invites where expires_at < now() - interval '30 days'`;
}

/** One pass of the cleanup. */
export async function cleanUpAccounts() {
  const s = await getSettings();
  await pruneInvites();
  if (s.audit_retention_days > 0) await pruneAuditLog(s.audit_retention_days);
  const months = s.inactive_account_months;
  if (months > 0 && (await smtpConfigured())) {
    await sendNotices(months);
    await deleteWhere("user.delete_inactive", (q) => q`${deletable(q)} and ${idle(q, months)} and ${warned(q)}`);
  }
  const days = s.unverified_account_days;
  if (days > 0 && (await verificationRequired())) {
    await deleteWhere("user.delete_unconfirmed", (q) => q`${deletable(q)} and ${unconfirmed(q, days)}`);
  }
}

export function startAccountCleanup() {
  if (g.__tundAccountCleanup) return;
  g.__tundAccountCleanup = true;
  const run = () =>
    cleanUpAccounts().catch((err) => {
      console.error("tund: inactive account cleanup failed", err);
    });
  setTimeout(() => {
    void run();
    setInterval(() => void run(), EVERY_MS);
  }, FIRST_RUN_MS);
}
