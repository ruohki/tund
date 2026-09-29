"use server";

import { domainToASCII } from "node:url";
import { refresh } from "next/cache";
import { requireAdmin, type User } from "@/lib/auth";
import { audit } from "@/lib/audit";
import { fileReport, knownHostname, refreshDomainRisk, type DomainRisk } from "@/lib/abuse";
import { REPORT_CATEGORIES, type ReportCategory } from "@/lib/abuse-shared";
import { clientIp } from "@/lib/client-ip";
import { db, notify } from "@/lib/db";
import { rateLimited } from "@/lib/email-tokens";
import { isUuid } from "@/lib/requests";
import { verifyTurnstile } from "@/lib/turnstile";
import { checkEmail } from "@/lib/validate";
import type { FormState } from "./auth";

const str = (fd: FormData, k: string) => String(fd.get(k) ?? "").trim();
const actorOf = (u: User) => ({ id: u.id, email: u.email });

/** "https://Foo.example.com/path" → "foo.example.com"; keeps ":port" for TCP addresses. Null when it isn't a hostname. */
function reportHost(input: string): string | null {
  let s = input.trim().toLowerCase();
  s = s.replace(/^[a-z][a-z0-9+.-]*:\/\//, "").replace(/[/?#].*$/, "").replace(/^[^@]*@/, "");
  const m = /^(.+?)\.?(:\d{1,5})?$/.exec(s);
  if (!m) return null;
  const host = domainToASCII(m[1]);
  if (!host || host.length > 253 || !/^[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(host)) return null;
  return host + (m[2] ?? "");
}

// --- public report form ----------------------------------------------------------

export async function submitReportAction(_: FormState, fd: FormData): Promise<FormState> {
  const url = str(fd, "url").slice(0, 2000);
  if (url && !/^https?:\/\/\S+$/i.test(url)) return { error: "Enter the full address starting with https://, or leave it empty." };
  let hostname = reportHost(str(fd, "host"));
  if (!str(fd, "host") && url) hostname = reportHost(url);
  if (!hostname) return { error: "Enter the address of the tunnel, e.g. something.example.com." };
  const category = str(fd, "category") as ReportCategory;
  if (!REPORT_CATEGORIES.some((c) => c.value === category)) return { error: "Choose what kind of abuse it is." };
  const description = str(fd, "description").slice(0, 5000);
  if (description.length < 10) return { error: "Describe what you saw in a sentence or two." };
  const email = str(fd, "email").toLowerCase().slice(0, 200);
  if (email && checkEmail(email)) return { error: "Enter a valid email address, or leave it empty." };

  const captcha = await verifyTurnstile(fd);
  if (captcha) return { error: captcha };
  const ip = await clientIp();
  if (rateLimited(`report-ip:${ip || "unknown"}`, 5, 60 * 60_000)) {
    return { error: "You sent several reports in the last hour. Try again later, or contact the operators directly." };
  }
  if (!(await knownHostname(hostname))) {
    return { error: `${hostname} isn't an address on this server. Check the spelling, or report it to whoever hosts that site.` };
  }
  await fileReport({ hostname, url, category, description, reporterEmail: email, reporterIp: ip, source: "form" });
  return { ok: hostname };
}

// --- admin: reports ----------------------------------------------------------------

export async function setReportStatusAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const id = str(fd, "id");
  const status = str(fd, "status");
  const note = str(fd, "note").slice(0, 2000);
  if (!isUuid(id) || !["open", "resolved", "dismissed"].includes(status)) return { error: "Unknown report." };
  const [r] =
    status === "open"
      ? await db()`
          update abuse_reports set status = 'open', resolved_at = null, resolved_by = null, resolution = ''
          where id = ${id} returning hostname`
      : await db()`
          update abuse_reports set status = ${status}, resolved_at = now(), resolved_by = ${admin.id}, resolution = ${note}
          where id = ${id} returning hostname`;
  if (!r) return { error: "Unknown report." };
  const action = status === "open" ? "abuse.reopen" : status === "resolved" ? "abuse.resolve" : "abuse.dismiss";
  await audit(actorOf(admin), action, r.hostname as string, { report: id, ...(note && status !== "open" ? { note } : {}) });
  refresh();
  return { ok: status === "open" ? "Reopened." : status === "resolved" ? "Marked as resolved." : "Dismissed." };
}

// --- admin: blocked hostnames ---------------------------------------------------------

export async function blockHostAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const hostname = reportHost(str(fd, "hostname"));
  if (!hostname) return { error: "Enter a hostname." };
  const reason = str(fd, "reason").slice(0, 300) || "violates the acceptable use policy";
  await db()`
    insert into blocked_hosts (hostname, reason, created_by) values (${hostname}, ${reason}, ${admin.id})
    on conflict (hostname) do update set reason = excluded.reason`;
  // The edge reloads the list and stops a tunnel that is online on it.
  await notify("tund_config", { kind: "blocked_hosts" });
  await audit(actorOf(admin), "host.block", hostname, { reason, ...(isUuid(str(fd, "report")) ? { report: str(fd, "report") } : {}) });
  refresh();
  return { ok: `Blocked ${hostname}.` };
}

export async function unblockHostAction(fd: FormData) {
  const admin = await requireAdmin();
  const hostname = str(fd, "hostname");
  const [row] = await db()`delete from blocked_hosts where hostname = ${hostname} returning hostname`;
  if (!row) return;
  await notify("tund_config", { kind: "blocked_hosts" });
  await audit(actorOf(admin), "host.unblock", hostname);
  refresh();
}

// --- admin: custom domain review ------------------------------------------------------

export async function reviewDomainAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const id = str(fd, "id");
  const decision = str(fd, "decision");
  const reason = str(fd, "reason").slice(0, 300);
  if (!isUuid(id) || (decision !== "approved" && decision !== "rejected")) return { error: "Unknown domain." };
  if (decision === "rejected" && !reason) return { error: "Give a reason; the owner sees it." };
  const [d] = await db()`select hostname, team_id, risk from domains where id = ${id} and kind = 'custom'`;
  if (!d) return { error: "Unknown domain." };
  const review: DomainRisk["review"] = { decision, reason, by: admin.email, at: new Date().toISOString() };
  await db()`
    update domains set approval = ${decision}, risk = ${db().json({ ...((d.risk as DomainRisk) ?? {}), review } as never)}
    where id = ${id}`;
  await notify("tund_config", { kind: "domain", id });
  if (d.team_id) await notify("tund_config", { kind: "team", id: d.team_id });
  // The review request in the abuse queue is done with.
  await db()`
    update abuse_reports set status = 'resolved', resolved_at = now(), resolved_by = ${admin.id},
      resolution = ${decision === "approved" ? "domain approved" : `domain rejected: ${reason}`}
    where status = 'open' and details->>'kind' = 'domain_review' and details->>'domain_id' = ${id}`;
  await audit(actorOf(admin), decision === "approved" ? "domain.approve" : "domain.reject", d.hostname as string, reason ? { reason } : {});
  refresh();
  return { ok: decision === "approved" ? `Approved ${d.hostname}.` : `Rejected ${d.hostname}.` };
}

export async function recheckDomainAction(fd: FormData) {
  await requireAdmin();
  const id = str(fd, "id");
  if (isUuid(id)) await refreshDomainRisk(id);
  refresh();
}
