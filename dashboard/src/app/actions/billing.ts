"use server";

import { refresh } from "next/cache";
import { redirect } from "next/navigation";
import { requireAdmin, requireUser } from "@/lib/auth";
import { audit } from "@/lib/audit";
import { db, notify } from "@/lib/db";
import {
  BillingError,
  checkoutUrl,
  ensurePortalConfiguration,
  ensureWebhook,
  portalUrl,
  proSubscription,
  setSeatPacks,
  syncCatalog,
  teamSubscription,
  type Interval,
} from "@/lib/billing";
import { billingEnabled, teamBilling, type TeamPlan } from "@/lib/plans";
import { encryptSecret, getSettings, invalidateSettings, PRICE_KEYS, writeSettings, type BillingSettings } from "@/lib/settings";
import { membershipById } from "@/lib/teams";
import { isUuid } from "@/lib/requests";
import type { FormState } from "./auth";

const str = (fd: FormData, k: string) => String(fd.get(k) ?? "").trim();
const interval = (fd: FormData): Interval => (str(fd, "interval") === "year" ? "year" : "month");

function billingProblem(err: unknown): string {
  if (err instanceof BillingError) return err.message;
  const msg = err instanceof Error ? err.message : String(err);
  console.error("tund: billing", err);
  return `Stripe refused the request: ${msg}`;
}

// --- subscribing -------------------------------------------------------------------

/** Sends the user to Stripe Checkout for Pro (or to the portal when they already have it). */
export async function subscribeProAction(fd: FormData) {
  const user = await requireUser();
  if (!(await billingEnabled())) redirect("/billing");
  const current = await proSubscription(user.id);
  let url: string;
  try {
    url = current?.active ? await portalUrl(user, "/billing") : await checkoutUrl({ user, plan: "pro", interval: interval(fd) });
  } catch (err) {
    redirect(`/billing?error=${encodeURIComponent(billingProblem(err))}`);
  }
  redirect(url);
}

/** Team owners subscribe their team to Team or Team Pro. */
export async function subscribeTeamAction(fd: FormData) {
  const user = await requireUser();
  const teamId = str(fd, "team_id");
  const m = isUuid(teamId) ? await membershipById(user.id, teamId) : null;
  if (!m) redirect("/teams");
  const back = `/teams/${m.team.slug}`;
  if (m.role !== "owner") redirect(`${back}?billing_error=${encodeURIComponent("Only owners can subscribe the team.")}`);
  if (!(await billingEnabled())) redirect(back);
  const plan: TeamPlan = str(fd, "plan") === "team_pro" ? "team_pro" : "team";
  const current = await teamSubscription(teamId);
  if (current?.active) redirect(`${back}?billing_error=${encodeURIComponent("This team already has a subscription.")}`);
  const packs = Math.max(0, Math.min(100, Number.parseInt(str(fd, "packs"), 10) || 0));
  let url: string;
  try {
    url = await checkoutUrl({ user, plan, interval: interval(fd), team: { id: teamId, slug: m.team.slug }, seatPacks: packs });
  } catch (err) {
    redirect(`${back}?billing_error=${encodeURIComponent(billingProblem(err))}`);
  }
  redirect(url);
}

/** Stripe's customer portal (payment methods, invoices, cancelling). */
export async function billingPortalAction(fd: FormData) {
  const user = await requireUser();
  const back = str(fd, "return") === "team" && /^[a-z0-9-]+$/.test(str(fd, "slug")) ? `/teams/${str(fd, "slug")}` : "/billing";
  let url: string;
  try {
    url = await portalUrl(user, back);
  } catch (err) {
    redirect(`${back}?${back === "/billing" ? "error" : "billing_error"}=${encodeURIComponent(billingProblem(err))}`);
  }
  redirect(url);
}

/** Buys or returns extra seat packs on the team's subscription (only its payer can). */
export async function setSeatPacksAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const teamId = str(fd, "team_id");
  const m = isUuid(teamId) ? await membershipById(user.id, teamId) : null;
  if (!m || m.role !== "owner") return { error: "Only owners can change the team's seats." };
  const sub = await teamSubscription(teamId);
  if (!sub?.active) return { error: "This team has no active subscription." };
  if (sub.payerId !== user.id) return { error: "Only the owner who pays for the team can change its seats." };
  const packs = Number.parseInt(str(fd, "packs"), 10);
  if (!Number.isInteger(packs) || packs < 0 || packs > 100) return { error: "Choose between 0 and 100 extra seat packs." };
  const [s, billing] = await Promise.all([getSettings(), teamBilling(teamId)]);
  const seatsAfter = s.team_seats + packs * s.team_seat_pack;
  if (billing.members > seatsAfter) {
    return { error: `The team has ${billing.members} members; ${seatsAfter} seats aren't enough. Remove members first.` };
  }
  try {
    await setSeatPacks(sub.id, packs);
  } catch (err) {
    return { error: billingProblem(err) };
  }
  await audit({ id: user.id, email: user.email }, "team.seats", m.team.slug, { packs });
  refresh();
  return { ok: `The team now has ${seatsAfter} seats.` };
}

// --- administration ----------------------------------------------------------------

/** Admin → Billing: keys, currency, tax and prices. */
export async function saveBillingAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const cur = (await getSettings()).billing;
  const key = str(fd, "secret_key");
  if (key && !/^(sk|rk)_(test|live)_[A-Za-z0-9]+$/.test(key)) return { error: "That doesn't look like a Stripe secret or restricted key (sk_… or rk_…)." };
  const whsec = str(fd, "webhook_secret");
  if (whsec && !/^whsec_[A-Za-z0-9]+$/.test(whsec)) return { error: "A webhook signing secret starts with whsec_." };
  const currency = str(fd, "currency").toLowerCase() || "usd";
  if (!/^[a-z]{3}$/.test(currency)) return { error: "Use a three-letter currency code like usd or eur." };
  const prices = { ...cur.prices };
  for (const k of PRICE_KEYS) {
    const raw = str(fd, `price_${k}`);
    if (!raw) continue;
    const n = Math.round(Number(raw.replace(",", ".")) * 100);
    if (!Number.isFinite(n) || n < 0 || n > 100_000_00) return { error: `Invalid price for ${k}.` };
    prices[k] = n;
  }
  const next: BillingSettings = {
    ...cur,
    enabled: fd.get("enabled") === "on",
    secret_key_enc: key ? encryptSecret(key) : cur.secret_key_enc,
    webhook_secret_enc: whsec ? encryptSecret(whsec) : cur.webhook_secret_enc,
    currency,
    automatic_tax: fd.get("automatic_tax") === "on",
    prices,
  };
  if (next.enabled && !next.secret_key_enc) return { error: "Add a Stripe secret key before turning billing on." };
  await writeSettings({ billing: next }, admin.id);
  invalidateSettings();
  await audit({ id: admin.id, email: admin.email }, "settings.billing", "", { enabled: next.enabled, currency, key_changed: Boolean(key) });
  await notify("tund_config", { kind: "plans" });
  refresh();
  return { ok: "Saved. Use “Set up Stripe” after changing keys or prices." };
}

export type SetupState = { error?: string | null; log?: string[] } | null;

/** Creates the products, prices and webhook endpoint in Stripe. */
export async function setupStripeAction(): Promise<SetupState> {
  const admin = await requireAdmin();
  const b = (await getSettings()).billing;
  if (!b.secret_key_enc) return { error: "Add a Stripe secret key first." };
  try {
    const log = await syncCatalog();
    const hook = await ensureWebhook();
    const portal = await ensurePortalConfiguration();
    const next = { ...b, webhook_id: hook.id, portal_configuration_id: portal.id };
    if (hook.secret) {
      next.webhook_secret_enc = encryptSecret(hook.secret);
      log.push("Created the webhook endpoint and saved its signing secret.");
    } else {
      log.push("The webhook endpoint points at this dashboard.");
    }
    log.push(portal.created ? "Created the customer portal settings." : "Updated the customer portal settings.");
    await writeSettings({ billing: next }, admin.id);
    invalidateSettings();
    await audit({ id: admin.id, email: admin.email }, "settings.billing_setup", "");
    refresh();
    return { log };
  } catch (err) {
    return { error: billingProblem(err) };
  }
}

/** Admin: Pro without paying (on/off) for one account. */
export async function setProGrantedAction(fd: FormData) {
  const admin = await requireAdmin();
  const id = str(fd, "id");
  if (!isUuid(id)) return;
  const on = str(fd, "on") === "1";
  const [u] = await db()`update users set pro_granted = ${on} where id = ${id} returning email`;
  if (!u) return;
  await audit({ id: admin.id, email: admin.email }, on ? "user.pro_grant" : "user.pro_revoke", u.email as string);
  await notify("tund_config", { kind: "plans", id });
  refresh();
}

/** Admin: exempt a team from billing with a plan, and/or set its seats. */
export async function setTeamPlanAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const id = str(fd, "team_id");
  if (!isUuid(id)) return { error: "Unknown team." };
  const planRaw = str(fd, "plan_granted");
  const plan = planRaw === "team" || planRaw === "team_pro" ? planRaw : null;
  const seatsRaw = str(fd, "seats_override");
  const seats = seatsRaw === "" ? null : Number.parseInt(seatsRaw, 10);
  if (seats !== null && (!Number.isInteger(seats) || seats < 1 || seats > 100000)) return { error: "Seats: a number from 1, or empty for the plan's." };
  const [t] = await db()`update teams set plan_granted = ${plan}, seats_override = ${seats} where id = ${id} returning slug`;
  if (!t) return { error: "Unknown team." };
  await audit({ id: admin.id, email: admin.email }, "team.plan", t.slug as string, { plan_granted: plan, seats_override: seats });
  await notify("tund_config", { kind: "plans" });
  await notify("tund_config", { kind: "team", id });
  refresh();
  return { ok: "Saved." };
}
