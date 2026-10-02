import "server-only";
import { config } from "./config";
import { db } from "./db";
import { domainVerificationLostEmail, smtpConfigured, trySendMail } from "./mail";
import { getSettings } from "./settings";

// Emails the owner when the server withdraws a custom domain's verification
// (NOTIFY tund_domain, see internal/server/domaincheck.go). Every dashboard
// hears it; the one that sets unverified_notified_at sends. Started once per
// process from instrumentation.ts, which also catches up on withdrawals no
// dashboard heard (all restarting at the time).

const g = globalThis as unknown as { __tundDomainMailer?: boolean };

async function notify(id: string | null) {
  try {
    if (!(await smtpConfigured())) return;
    const sql = db();
    const rows = await sql`
      update domains d set unverified_notified_at = now()
      from users u
      where u.id = d.user_id and d.verified_at is null and d.unverified_at is not null and d.unverified_notified_at is null
        and ${id ? sql`d.id = ${id}` : sql`d.unverified_at > now() - interval '1 day'`}
      returning d.hostname, d.unverified_reason, u.email, u.disabled_at,
        (select slug from teams t where t.id = d.team_id) as team_slug`;
    if (!rows.length) return;
    const instance = (await getSettings()).instance_name;
    const base = config().dashboardUrl;
    for (const r of rows) {
      if (r.disabled_at) continue;
      const url = r.team_slug ? `${base}/teams/${r.team_slug}` : `${base}/domains`;
      await trySendMail(
        r.email as string,
        domainVerificationLostEmail(instance, r.hostname as string, (r.unverified_reason as string) || "the TXT record is gone", url),
      );
    }
  } catch (err) {
    console.error("tund: domain verification email failed", err);
  }
}

export async function startDomainMailer() {
  if (g.__tundDomainMailer) return;
  g.__tundDomainMailer = true;
  try {
    await db().listen("tund_domain", (payload) => {
      try {
        const { id } = JSON.parse(payload) as { id?: string };
        if (typeof id === "string" && /^[0-9a-f-]{36}$/i.test(id)) void notify(id);
      } catch {
        /* malformed payload */
      }
    });
    void notify(null);
  } catch (err) {
    console.error("tund: couldn't listen for domain events; retrying in 30s", err);
    g.__tundDomainMailer = false;
    setTimeout(() => void startDomainMailer(), 30_000);
  }
}
