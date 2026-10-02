import "server-only";
import Stripe from "stripe";
import { config } from "./config";
import { db, notify } from "./db";
import type { User } from "./auth";
import { decryptSecret, getSettings, PRICE_KEYS, type BillingSettings, type PriceKey } from "./settings";
import type { TeamPlan } from "./plans";

// Stripe billing (docs/SPEC.md "Plans and billing"). Products and prices are
// created by the dashboard (Admin → Billing → Set up Stripe) and found by
// lookup key; subscriptions are mirrored into the subscriptions table by
// webhooks, which is what the plans read.

export type Plan = "pro" | TeamPlan;
export type Interval = "month" | "year";

const g = globalThis as unknown as { __tundStripe?: { key: string; client: Stripe } };

export class BillingError extends Error {}

export function stripeClient(b: BillingSettings): Stripe {
  const key = decryptSecret(b.secret_key_enc);
  if (!key) throw new BillingError("Stripe isn't set up (Admin → Billing).");
  if (g.__tundStripe?.key !== key) g.__tundStripe = { key, client: new Stripe(key, { appInfo: { name: "tund" } }) };
  return g.__tundStripe.client;
}

export async function stripe(): Promise<Stripe> {
  return stripeClient((await getSettings()).billing);
}

const lookupKey = (k: PriceKey) => `tund_${k}`;
export const priceKey = (plan: Plan, interval: Interval): PriceKey => `${plan}_${interval}` as PriceKey;
export const seatPriceKey = (plan: TeamPlan, interval: Interval): PriceKey => `${plan}_seats_${interval}` as PriceKey;

type ProductId = "pro" | "team" | "team_pro" | "team_seats" | "team_pro_seats";

const PRODUCTS: Record<ProductId, { name: (instance: string) => string; description: string }> = {
  pro: { name: (i) => `${i} Pro`, description: "Custom domains, TCP and TLS tunnels and higher limits." },
  team: { name: (i) => `${i} Team`, description: "A team with shared domains and identity providers; Pro for the owner." },
  team_pro: { name: (i) => `${i} Team Pro`, description: "A team where every member gets Pro." },
  team_seats: { name: (i) => `${i} Team seats`, description: "More members for a Team." },
  team_pro_seats: { name: (i) => `${i} Team Pro seats`, description: "More members for a Team Pro." },
};

function productOf(k: PriceKey): ProductId {
  return k.replace(/_(month|year)$/, "") as ProductId;
}

/**
 * Creates or updates the products and prices in Stripe to match the prices in
 * the settings. Changed amounts get a new price that takes over the lookup
 * key; existing subscriptions keep their old price until they change.
 */
export async function syncCatalog(): Promise<string[]> {
  const s = await getSettings();
  const b = s.billing;
  const client = stripeClient(b);
  const log: string[] = [];
  const instance = s.instance_name || "tund";

  const products = new Map<ProductId, Stripe.Product>();
  for await (const p of client.products.list({ active: true, limit: 100 })) {
    const id = p.metadata?.tund_product as ProductId | undefined;
    if (id && PRODUCTS[id] && !products.has(id)) products.set(id, p);
  }
  for (const id of Object.keys(PRODUCTS) as ProductId[]) {
    const want = { name: PRODUCTS[id].name(instance), description: PRODUCTS[id].description };
    const cur = products.get(id);
    if (!cur) {
      products.set(id, await client.products.create({ ...want, metadata: { tund_product: id } }));
      log.push(`Created product “${want.name}”.`);
    } else if (cur.name !== want.name || cur.description !== want.description) {
      products.set(id, await client.products.update(cur.id, want));
      log.push(`Updated product “${want.name}”.`);
    }
  }

  const existing = await client.prices.list({ lookup_keys: PRICE_KEYS.map(lookupKey), limit: 100 });
  for (const k of PRICE_KEYS) {
    const product = products.get(productOf(k))!;
    const interval: Interval = k.endsWith("_year") ? "year" : "month";
    const amount = b.prices[k];
    const cur = existing.data.find((p) => p.lookup_key === lookupKey(k));
    const ok =
      cur &&
      cur.active &&
      cur.unit_amount === amount &&
      cur.currency === b.currency &&
      cur.recurring?.interval === interval &&
      (typeof cur.product === "string" ? cur.product : cur.product.id) === product.id;
    if (ok) continue;
    await client.prices.create({
      product: product.id,
      currency: b.currency,
      unit_amount: amount,
      recurring: { interval },
      lookup_key: lookupKey(k),
      transfer_lookup_key: true,
      tax_behavior: b.automatic_tax ? "exclusive" : "unspecified",
      metadata: { tund_price: k },
    });
    if (cur?.active) await client.prices.update(cur.id, { active: false });
    log.push(`${cur ? "Replaced" : "Created"} price ${k}: ${(amount / 100).toFixed(2)} ${b.currency.toUpperCase()}/${interval}.`);
  }
  if (!log.length) log.push("Products and prices were already up to date.");
  return log;
}

const WEBHOOK_EVENTS: Stripe.WebhookEndpointCreateParams.EnabledEvent[] = [
  "checkout.session.completed",
  "customer.subscription.created",
  "customer.subscription.updated",
  "customer.subscription.deleted",
  "customer.subscription.paused",
  "customer.subscription.resumed",
];

export const webhookUrl = () => `${config().dashboardUrl}/api/billing/webhook`;

/** Creates (or points at this dashboard) the webhook endpoint; returns its id and, when new, its signing secret. */
export async function ensureWebhook(): Promise<{ id: string; secret: string | null }> {
  const b = (await getSettings()).billing;
  const client = stripeClient(b);
  if (b.webhook_id) {
    try {
      const cur = await client.webhookEndpoints.retrieve(b.webhook_id);
      if (cur.status !== "disabled") {
        await client.webhookEndpoints.update(cur.id, { url: webhookUrl(), enabled_events: WEBHOOK_EVENTS });
        return { id: cur.id, secret: null };
      }
    } catch {
      /* gone: create a new one */
    }
  }
  const ep = await client.webhookEndpoints.create({
    url: webhookUrl(),
    enabled_events: WEBHOOK_EVENTS,
    description: `${(await getSettings()).instance_name} dashboard`,
  });
  return { id: ep.id, secret: ep.secret ?? null };
}

/**
 * The customer portal configuration (payment methods, invoices, cancelling at
 * the end of the period): updated when it exists, created otherwise.
 */
export async function ensurePortalConfiguration(): Promise<{ id: string; created: boolean }> {
  const s = await getSettings();
  const client = stripeClient(s.billing);
  const params = {
    business_profile: { headline: `${s.instance_name} subscriptions`, terms_of_service_url: `${config().dashboardUrl}/terms` },
    default_return_url: `${config().dashboardUrl}/billing`,
    features: {
      customer_update: { enabled: true, allowed_updates: ["email", "address", "name", "tax_id"] as Stripe.BillingPortal.ConfigurationCreateParams.Features.CustomerUpdate.AllowedUpdate[] },
      invoice_history: { enabled: true },
      payment_method_update: { enabled: true },
      subscription_cancel: {
        enabled: true,
        mode: "at_period_end" as const,
        proration_behavior: "none" as const,
        cancellation_reason: {
          enabled: true,
          options: ["too_expensive", "missing_features", "switched_service", "unused", "other"] as Stripe.BillingPortal.ConfigurationCreateParams.Features.SubscriptionCancel.CancellationReason.Option[],
        },
      },
      // Seats and plan changes happen in the dashboard.
      subscription_update: { enabled: false },
    },
    metadata: { tund_portal: "1" },
  };
  if (s.billing.portal_configuration_id) {
    try {
      const cur = await client.billingPortal.configurations.retrieve(s.billing.portal_configuration_id);
      if (cur.active) {
        await client.billingPortal.configurations.update(cur.id, params);
        return { id: cur.id, created: false };
      }
    } catch {
      /* gone: create a new one */
    }
  }
  const cfg = await client.billingPortal.configurations.create(params);
  return { id: cfg.id, created: true };
}

/** The Stripe customer of a user, created on first use. */
export async function customerFor(user: Pick<User, "id" | "email" | "name">): Promise<string> {
  const [row] = await db()`select stripe_customer_id from users where id = ${user.id}`;
  if (row?.stripe_customer_id) return row.stripe_customer_id as string;
  const client = await stripe();
  const c = await client.customers.create(
    { email: user.email, name: user.name || undefined, metadata: { tund_user: user.id } },
    { idempotencyKey: `tund-customer-${user.id}` },
  );
  await db()`update users set stripe_customer_id = ${c.id} where id = ${user.id} and stripe_customer_id is null`;
  return c.id;
}

async function priceIds(keys: PriceKey[]): Promise<Map<PriceKey, string>> {
  const client = await stripe();
  const res = await client.prices.list({ lookup_keys: keys.map(lookupKey), active: true, limit: 100 });
  const out = new Map<PriceKey, string>();
  for (const p of res.data) out.set(p.lookup_key!.replace(/^tund_/, "") as PriceKey, p.id);
  for (const k of keys) if (!out.has(k)) throw new BillingError(`The price ${k} isn't set up in Stripe yet (Admin → Billing → Set up Stripe).`);
  return out;
}

/** A Stripe Checkout page for a plan; the subscription arrives through the webhook. */
export async function checkoutUrl(opts: {
  user: User;
  plan: Plan;
  interval: Interval;
  team?: { id: string; slug: string };
  seatPacks?: number;
}): Promise<string> {
  const b = (await getSettings()).billing;
  const client = stripeClient(b);
  const keys: PriceKey[] = [priceKey(opts.plan, opts.interval)];
  const packs = opts.plan !== "pro" ? Math.max(0, Math.min(100, opts.seatPacks ?? 0)) : 0;
  if (packs && opts.plan !== "pro") keys.push(seatPriceKey(opts.plan, opts.interval));
  const prices = await priceIds(keys);
  const back = opts.team ? `${config().dashboardUrl}/teams/${opts.team.slug}` : `${config().dashboardUrl}/billing`;
  const metadata = { tund_plan: opts.plan, tund_user: opts.user.id, tund_team: opts.team?.id ?? "" };
  const session = await client.checkout.sessions.create({
    mode: "subscription",
    customer: await customerFor(opts.user),
    client_reference_id: opts.user.id,
    line_items: [
      { price: prices.get(keys[0])!, quantity: 1 },
      ...(packs ? [{ price: prices.get(keys[1])!, quantity: packs }] : []),
    ],
    metadata,
    subscription_data: { metadata },
    // Name and address are what make a paying account trusted.
    billing_address_collection: "required",
    customer_update: { address: "auto", name: "auto" },
    allow_promotion_codes: true,
    automatic_tax: { enabled: b.automatic_tax },
    tax_id_collection: { enabled: b.automatic_tax },
    success_url: `${back}?checkout=done`,
    cancel_url: `${back}?checkout=cancelled`,
  });
  if (!session.url) throw new BillingError("Stripe didn't return a checkout page.");
  return session.url;
}

/** Stripe's customer portal: payment methods, invoices, cancelling. */
export async function portalUrl(user: User, returnPath: string): Promise<string> {
  const b = (await getSettings()).billing;
  const client = stripeClient(b);
  const s = await client.billingPortal.sessions.create({
    customer: await customerFor(user),
    return_url: `${config().dashboardUrl}${returnPath}`,
    ...(b.portal_configuration_id ? { configuration: b.portal_configuration_id } : {}),
  });
  return s.url;
}

/** Sets the number of extra seat packs on a team subscription (prorated, charged right away). */
export async function setSeatPacks(subscriptionId: string, packs: number) {
  const client = await stripe();
  const sub = await client.subscriptions.retrieve(subscriptionId, { expand: ["items.data.price"] });
  const plan = sub.metadata?.tund_plan as TeamPlan;
  const base = sub.items.data.find((i) => !String(i.price.lookup_key ?? "").includes("_seats_"));
  const interval = (base?.price.recurring?.interval ?? "month") as Interval;
  const seatItem = sub.items.data.find((i) => String(i.price.lookup_key ?? "").includes("_seats_"));
  const items: Stripe.SubscriptionUpdateParams.Item[] = [];
  if (seatItem && packs === 0) items.push({ id: seatItem.id, deleted: true });
  else if (seatItem) items.push({ id: seatItem.id, quantity: packs });
  else if (packs > 0) items.push({ price: (await priceIds([seatPriceKey(plan, interval)])).get(seatPriceKey(plan, interval))!, quantity: packs });
  if (!items.length) return;
  const updated = await client.subscriptions.update(subscriptionId, { items, proration_behavior: "always_invoice" });
  await syncSubscription(updated);
}

/** Mirrors a Stripe subscription into the subscriptions table and tells the edges. */
export async function syncSubscription(input: Stripe.Subscription | string) {
  const client = await stripe();
  const sub =
    typeof input === "string" || !input.items.data.every((i) => typeof i.price === "object" && i.price.lookup_key !== undefined)
      ? await client.subscriptions.retrieve(typeof input === "string" ? input : input.id, { expand: ["items.data.price"] })
      : input;
  const keys = sub.items.data.map((i) => String(i.price.lookup_key ?? "").replace(/^tund_/, ""));
  const base = sub.items.data.find((i) => !String(i.price.lookup_key ?? "").includes("_seats_"));
  const seats = sub.items.data.find((i) => String(i.price.lookup_key ?? "").includes("_seats_"));
  const plan = ((sub.metadata?.tund_plan as Plan | undefined) ??
    (keys.find((k) => /^(pro|team|team_pro)_(month|year)$/.test(k))?.replace(/_(month|year)$/, "") as Plan | undefined)) as Plan | undefined;
  if (!plan || !["pro", "team", "team_pro"].includes(plan)) return; // not one of ours
  const customer = typeof sub.customer === "string" ? sub.customer : sub.customer.id;
  const userId = (sub.metadata?.tund_user as string | undefined) || null;
  const teamId = (sub.metadata?.tund_team as string | undefined) || null;
  const periodEnd = base?.current_period_end ?? sub.items.data[0]?.current_period_end ?? null;
  await db()`
    insert into subscriptions (id, customer_id, user_id, team_id, plan, billing_interval, status, seat_packs,
      current_period_end, cancel_at_period_end, updated_at)
    values (${sub.id}, ${customer},
      (select id from users where id = ${userId}::uuid), (select id from teams where id = ${teamId}::uuid),
      ${plan}, ${base?.price.recurring?.interval ?? "month"}, ${sub.status}, ${seats?.quantity ?? 0},
      ${periodEnd ? new Date(periodEnd * 1000) : null}, ${sub.cancel_at_period_end || sub.cancel_at !== null}, now())
    on conflict (id) do update set status = excluded.status, plan = excluded.plan, billing_interval = excluded.billing_interval,
      seat_packs = excluded.seat_packs, current_period_end = excluded.current_period_end,
      cancel_at_period_end = excluded.cancel_at_period_end, updated_at = now()`;
  await notify("tund_config", { kind: "plans" });
}

export type SubscriptionRow = {
  id: string;
  plan: Plan;
  interval: Interval;
  status: string;
  active: boolean;
  seatPacks: number;
  periodEnd: string | null;
  cancelAtPeriodEnd: boolean;
};

function toRow(r: Record<string, unknown>): SubscriptionRow {
  return {
    id: r.id as string,
    plan: r.plan as Plan,
    interval: r.billing_interval as Interval,
    status: r.status as string,
    active: Boolean(r.active),
    seatPacks: Number(r.seat_packs),
    periodEnd: r.current_period_end ? (r.current_period_end as Date).toISOString() : null,
    cancelAtPeriodEnd: Boolean(r.cancel_at_period_end),
  };
}

/** The user's own Pro subscription (active first). */
export async function proSubscription(userId: string): Promise<SubscriptionRow | null> {
  const [r] = await db()`
    select *, subscription_active(status) as active from subscriptions
    where user_id = ${userId} and plan = 'pro' order by subscription_active(status) desc, updated_at desc limit 1`;
  return r ? toRow(r) : null;
}

/** The team's subscription (active first). */
export async function teamSubscription(teamId: string): Promise<(SubscriptionRow & { payerId: string | null }) | null> {
  const [r] = await db()`
    select *, subscription_active(status) as active from subscriptions
    where team_id = ${teamId} order by subscription_active(status) desc, updated_at desc limit 1`;
  return r ? { ...toRow(r), payerId: (r.user_id as string) ?? null } : null;
}

/** Price in the configured currency, e.g. "$5" or "€50". */
export function formatPrice(cents: number, currency: string): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: currency.toUpperCase(),
    minimumFractionDigits: cents % 100 ? 2 : 0,
  }).format(cents / 100);
}
