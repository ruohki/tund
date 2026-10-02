import "server-only";
import { domainToUnicode } from "node:url";
import type { User } from "./auth";
import { config } from "./config";
import { db, notify } from "./db";
import { getSettings } from "./settings";
import type { ReportCategory, ReportSource } from "./abuse-shared";

// Abuse protection (docs/SPEC.md "Abuse protection (migration 0009)").

/** "Trusted" = users.trusted or admin. */
export const isTrusted = (u: Pick<User, "isAdmin" | "trusted">) => u.isAdmin || u.trusted;

/**
 * Mirrors the edge's hostnameAllowed (internal/server/abuse.go): the first
 * blocked word a label contains, or null. Dashes are ignored; words starting
 * with "=" only match a whole dash-separated part.
 */
export function blockedWord(label: string, words: string[]): string | null {
  const lower = label.toLowerCase();
  const flat = lower.replaceAll("-", "");
  const parts = lower.split("-");
  for (const raw of words) {
    const w = raw.trim().toLowerCase();
    if (!w) continue;
    if (w.startsWith("=")) {
      if (parts.includes(w.slice(1))) return w.slice(1);
      continue;
    }
    if (flat.includes(w.replaceAll("-", ""))) return w;
  }
  return null;
}

/** Why a non-trusted account may not pin this base-domain label (same wording as the edge), or null. */
export async function labelRefusal(user: User, label: string): Promise<string | null> {
  if (isTrusted(user)) return null;
  const w = blockedWord(label, (await getSettings()).blocked_hostname_words);
  return w
    ? `The name “${label}” is not allowed on this server because it contains “${w}” (it looks like a brand or a login page). Choose another name.`
    : null;
}

/**
 * Whether the account may use custom domains: its override, else the instance
 * setting (admins don't need it). Mirrors customDomainsEnabled in the edge.
 */
export async function customDomainsEnabled(user: Pick<User, "isAdmin" | "customDomains">): Promise<boolean> {
  return user.customDomains ?? (user.isAdmin || (await getSettings()).custom_domains);
}

export const CUSTOM_DOMAINS_OFF = "Custom domains aren't enabled for your account on this server. Ask an administrator to turn them on.";

/**
 * Whether the account may open TCP and TLS tunnels: its override, else the
 * instance setting (admins don't need it). Mirrors passthroughAllowed in the edge.
 */
export async function passthroughEnabled(user: Pick<User, "isAdmin" | "passthrough">): Promise<boolean> {
  return user.passthrough ?? (user.isAdmin || (await getSettings()).passthrough);
}

export const PASSTHROUGH_OFF = "TCP and TLS tunnels aren't enabled for your account on this server. Ask an administrator to turn them on.";

/**
 * Why this account may not add this custom domain, or null. The edge refuses
 * to bind these even when approved, so they're refused up front.
 */
export async function customDomainRefusal(user: User, hostname: string): Promise<string | null> {
  if (!(await customDomainsEnabled(user))) return CUSTOM_DOMAINS_OFF;
  if (isTrusted(user)) return null;
  const s = await getSettings();
  if (s.untrusted_custom_domains === "deny") {
    return "Custom domains need a trusted account on this server. Ask an administrator to mark your account as trusted.";
  }
  const labels = hostname.replace(/^\*\./, "").split(".");
  if (labels.some((l) => l.startsWith("xn--"))) {
    return `${hostname} uses internationalized (punycode) labels, which need a trusted account on this server. Ask an administrator.`;
  }
  for (const l of labels) {
    const w = blockedWord(l, s.blocked_hostname_words);
    if (w) return `The domain ${hostname} is not allowed on this server because it contains “${w}”. Ask an administrator if this is a mistake.`;
  }
  return null;
}

/** The reason a hostname is blocked, or null. */
export async function blockedHostReason(hostname: string): Promise<string | null> {
  const [row] = await db()`select reason from blocked_hosts where hostname = ${hostname}`;
  return row ? (row.reason as string) || "blocked" : null;
}

// --- reports ---------------------------------------------------------------------

/** Who serves a hostname: the online tunnel, else the domain owner, else the latest tunnel. */
export async function ownerOfHostname(hostname: string): Promise<{ userId: string | null; tunnelId: string | null }> {
  const sql = db();
  const [online] = await sql`select id, user_id from tunnels where hostname = ${hostname} and ended_at is null limit 1`;
  if (online) return { userId: online.user_id as string, tunnelId: online.id as string };
  const [d] = await sql`select user_id from domains where hostname = ${hostname}`;
  if (d) return { userId: d.user_id as string, tunnelId: null };
  const [last] = await sql`select id, user_id from tunnels where hostname = ${hostname} order by started_at desc limit 1`;
  return last ? { userId: last.user_id as string, tunnelId: last.id as string } : { userId: null, tunnelId: null };
}

/** Whether this instance serves (or served) the hostname, so reports about unrelated sites can be refused. */
export async function knownHostname(hostname: string): Promise<boolean> {
  const base = config().baseDomain;
  const host = hostname.replace(/:\d+$/, "");
  if (host === base || host.endsWith(`.${base}`)) return true;
  const [row] = await db()`
    select exists(select 1 from domains where hostname = ${hostname})
        or exists(select 1 from tunnels where hostname = ${hostname}) as known`;
  return Boolean(row?.known);
}

export type NewReport = {
  hostname: string;
  url?: string;
  category: ReportCategory;
  description: string;
  source: ReportSource;
  reporterEmail?: string;
  reporterIp?: string;
  details?: Record<string, unknown>;
  userId?: string | null;
  tunnelId?: string | null;
};

/** Files a report and wakes the admin mailer (NOTIFY tund_abuse). Owner defaults to whoever serves the hostname. */
export async function fileReport(r: NewReport): Promise<string> {
  const owner = r.userId === undefined ? await ownerOfHostname(r.hostname) : { userId: r.userId, tunnelId: r.tunnelId ?? null };
  const [row] = await db()`
    insert into abuse_reports (hostname, url, category, description, reporter_email, reporter_ip, source, details, user_id, tunnel_id)
    values (${r.hostname}, ${r.url ?? ""}, ${r.category}, ${r.description}, ${r.reporterEmail ?? ""}, ${r.reporterIp ?? ""},
            ${r.source}, ${db().json((r.details ?? {}) as never)}, ${owner.userId}, ${owner.tunnelId})
    returning id`;
  const id = row.id as string;
  await notify("tund_abuse", { id, hostname: r.hostname, source: r.source });
  return id;
}

// --- custom domain review ----------------------------------------------------------

// Second-level labels that ccTLD registries sell under (example.co.uk); a hint, not the Public Suffix List.
const SECOND_LEVEL = new Set(["co", "com", "net", "org", "gov", "edu", "ac", "or", "ne", "go", "ltd", "plc", "nom", "sch"]);

/** example.co.uk for a.b.example.co.uk; what RDAP knows about. */
export function registrableDomain(hostname: string): string {
  const labels = hostname.replace(/^\*\./, "").split(".");
  if (labels.length <= 2) return labels.join(".");
  const [sld, tld] = labels.slice(-2);
  const n = tld.length === 2 && SECOND_LEVEL.has(sld) ? 3 : 2;
  return labels.slice(-n).join(".");
}

async function rdapRegistration(domain: string): Promise<{ date: Date | null; error?: string }> {
  try {
    const res = await fetch(`https://rdap.org/domain/${encodeURIComponent(domain)}`, {
      headers: { accept: "application/rdap+json, application/json" },
      signal: AbortSignal.timeout(8000),
      cache: "no-store",
    });
    if (res.status === 404) return { date: null, error: "No RDAP record (the registry may not publish RDAP data)." };
    if (!res.ok) return { date: null, error: `RDAP answered HTTP ${res.status}.` };
    const data = (await res.json()) as { events?: { eventAction?: string; eventDate?: string }[] };
    const ev = data.events?.find((e) => e.eventAction === "registration");
    const date = ev?.eventDate ? new Date(ev.eventDate) : null;
    if (!date || Number.isNaN(date.getTime())) return { date: null, error: "The RDAP record has no registration date." };
    return { date };
  } catch (err) {
    return { date: null, error: (err as Error).name === "TimeoutError" ? "The RDAP lookup timed out." : "The RDAP lookup failed." };
  }
}

/** Google Safe Browsing v4 lookup of one URL: the threat type, null when clean. */
async function safeBrowsing(key: string, url: string): Promise<{ threat: string | null; error?: string }> {
  try {
    const res = await fetch(`https://safebrowsing.googleapis.com/v4/threatMatches:find?key=${encodeURIComponent(key)}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        client: { clientId: "tund", clientVersion: "dashboard" },
        threatInfo: {
          threatTypes: ["MALWARE", "SOCIAL_ENGINEERING", "UNWANTED_SOFTWARE", "POTENTIALLY_HARMFUL_APPLICATION"],
          platformTypes: ["ANY_PLATFORM"],
          threatEntryTypes: ["URL"],
          threatEntries: [{ url }],
        },
      }),
      signal: AbortSignal.timeout(8000),
      cache: "no-store",
    });
    if (!res.ok) return { threat: null, error: `Safe Browsing answered HTTP ${res.status}.` };
    const data = (await res.json()) as { matches?: { threatType?: string }[] };
    return { threat: data.matches?.[0]?.threatType ?? null };
  } catch {
    return { threat: null, error: "The Safe Browsing lookup failed." };
  }
}

export type DomainRisk = {
  checked_at?: string;
  registrable?: string;
  registered_at?: string | null;
  age_days?: number | null;
  rdap_error?: string;
  new_domain?: boolean;
  deceptive_words?: string[];
  idn?: boolean;
  unicode?: string;
  safe_browsing?: string | null;
  safe_browsing_error?: string;
  /** The admin's decision, kept across re-checks. */
  review?: { decision: "approved" | "rejected"; reason: string; by: string; at: string };
};

/** Collects the review signals for a custom domain. */
export async function computeDomainRisk(hostname: string): Promise<{ risk: DomainRisk; registeredAt: string | null }> {
  const s = await getSettings();
  const host = hostname.replace(/^\*\./, "");
  const registrable = registrableDomain(host);
  const [rdap, sb] = await Promise.all([
    rdapRegistration(registrable),
    s.safe_browsing_api_key ? safeBrowsing(s.safe_browsing_api_key, `https://${host}/`) : Promise.resolve(null),
  ]);
  const labels = host.split(".");
  const words = [...new Set(labels.map((l) => blockedWord(l, s.blocked_hostname_words)).filter((w): w is string => !!w))];
  const idn = labels.some((l) => l.startsWith("xn--"));
  const ageDays = rdap.date ? Math.floor((Date.now() - rdap.date.getTime()) / 86_400_000) : null;
  const registeredAt = rdap.date ? rdap.date.toISOString().slice(0, 10) : null;
  const risk: DomainRisk = {
    checked_at: new Date().toISOString(),
    registrable,
    registered_at: registeredAt,
    age_days: ageDays,
    ...(rdap.error ? { rdap_error: rdap.error } : {}),
    new_domain: ageDays !== null && ageDays < s.custom_domain_min_age_days,
    deceptive_words: words,
    idn,
    ...(idn ? { unicode: domainToUnicode(host) } : {}),
    ...(sb ? { safe_browsing: sb.threat, ...(sb.error ? { safe_browsing_error: sb.error } : {}) } : {}),
  };
  return { risk, registeredAt };
}

/** Recomputes and stores a domain's risk signals, keeping an earlier review decision. */
export async function refreshDomainRisk(domainId: string): Promise<void> {
  const [d] = await db()`select hostname, risk from domains where id = ${domainId}`;
  if (!d) return;
  const { risk, registeredAt } = await computeDomainRisk(d.hostname as string);
  const review = (d.risk as DomainRisk | null)?.review;
  await db()`
    update domains set risk = ${db().json({ ...risk, ...(review ? { review } : {}) } as never)}, registered_at = ${registeredAt}
    where id = ${domainId}`;
}

/** Signals worth a warning in the review UI. */
export function riskFlags(r: DomainRisk | null | undefined): string[] {
  if (!r) return [];
  const out: string[] = [];
  if (r.safe_browsing) out.push(`Safe Browsing: ${r.safe_browsing}`);
  if (r.new_domain) out.push(`Registered ${r.age_days} ${r.age_days === 1 ? "day" : "days"} ago`);
  if (r.deceptive_words?.length) out.push(`Contains “${r.deceptive_words.join("”, “")}”`);
  if (r.idn) out.push(`Internationalized: ${r.unicode ?? "punycode"}`);
  return out;
}
