import "server-only";
import { categoryLabel, sourceLabel } from "./abuse-shared";
import { config } from "./config";
import { db } from "./db";
import { abuseReportsEmail, smtpConfigured, trySendMail } from "./mail";
import { getSettings } from "./settings";

// Emails admins about new abuse reports (NOTIFY tund_abuse from the edge and
// the dashboard), batched to at most one email a minute. Started once per
// process from instrumentation.ts.

const MIN_GAP = 60_000;
const SETTLE = 5_000; // collect a burst before sending

type State = { pending: Set<string>; timer: ReturnType<typeof setTimeout> | null; lastSent: number };
const g = globalThis as unknown as { __tundAbuseMailer?: State };

async function flush(state: State) {
  state.timer = null;
  const ids = [...state.pending];
  state.pending.clear();
  if (!ids.length) return;
  state.lastSent = Date.now();
  try {
    if (!(await smtpConfigured())) return;
    const sql = db();
    const [reports, admins, [{ open }], s] = await Promise.all([
      sql`select id, hostname, category, source, details from abuse_reports where id in ${sql(ids)} order by created_at`,
      sql`select email from users where is_admin and disabled_at is null`,
      sql`select count(*)::int as open from abuse_reports where status = 'open'`,
      getSettings(),
    ]);
    if (!reports.length || !admins.length) return;
    const base = config().dashboardUrl;
    const mail = abuseReportsEmail(
      s.instance_name,
      reports.map((r) => ({
        hostname: r.hostname as string,
        what:
          r.details?.kind === "domain_review"
            ? "custom domain awaiting review"
            : `${categoryLabel(r.category as string)} (${sourceLabel(r.source as string)})`,
        url: `${base}/admin/abuse/${r.id}`,
      })),
      open as number,
      `${base}/admin/abuse`,
    );
    await Promise.all(admins.map((a) => trySendMail(a.email as string, mail)));
  } catch (err) {
    console.error("tund: abuse report email failed", err);
  }
}

function schedule(state: State) {
  if (state.timer) return;
  const wait = Math.max(SETTLE, state.lastSent + MIN_GAP - Date.now());
  state.timer = setTimeout(() => void flush(state), wait);
}

export async function startAbuseMailer() {
  if (g.__tundAbuseMailer) return;
  const state: State = { pending: new Set(), timer: null, lastSent: 0 };
  g.__tundAbuseMailer = state;
  try {
    await db().listen("tund_abuse", (payload) => {
      try {
        const { id } = JSON.parse(payload) as { id?: string };
        if (typeof id !== "string" || !/^[0-9a-f-]{36}$/i.test(id)) return;
        state.pending.add(id);
        schedule(state);
      } catch {
        /* malformed payload */
      }
    });
  } catch (err) {
    console.error("tund: couldn't listen for abuse reports; retrying in 30s", err);
    g.__tundAbuseMailer = undefined;
    setTimeout(() => void startAbuseMailer(), 30_000);
  }
}
