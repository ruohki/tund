import type { Metadata } from "next";
import { db } from "@/lib/db";
import { decryptSecret, getSettings } from "@/lib/settings";
import { formatPrice, webhookUrl } from "@/lib/billing";
import { PLAN_LABEL } from "@/lib/plans";
import { Panel } from "@/components/ui";
import { BillingForm, SetupStripe } from "./billing-forms";

export const metadata: Metadata = { title: "Billing" };

export default async function AdminBillingPage() {
  const s = await getSettings();
  const b = s.billing;
  const key = decryptSecret(b.secret_key_enc);
  const [subs] = await Promise.all([
    db()`
      select plan, billing_interval, count(*)::int as n, coalesce(sum(seat_packs), 0)::int as packs
      from subscriptions where subscription_active(status) group by plan, billing_interval order by plan, billing_interval`,
  ]);
  const [granted] = await db()`
    select (select count(*)::int from users where pro_granted) as users,
      (select count(*)::int from teams where plan_granted is not null) as teams`;
  // Monthly recurring revenue from the configured prices (yearly counted as /12).
  const mrr = subs.reduce((sum, r) => {
    const per = r.billing_interval === "year" ? 12 : 1;
    const plan = r.plan as "pro" | "team" | "team_pro";
    const base = b.prices[`${plan}_${r.billing_interval}` as keyof typeof b.prices] ?? 0;
    const seat = plan === "pro" ? 0 : (b.prices[`${plan}_seats_${r.billing_interval}` as keyof typeof b.prices] ?? 0);
    return sum + (Number(r.n) * base + Number(r.packs) * seat) / per;
  }, 0);

  return (
    <>
      <div className="mb-6 grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]">
        <Panel title="Stripe" description="Subscriptions for Pro, Team and Team Pro. Free accounts keep the limits under Settings → Limits." bodyClassName="p-4">
          {/* Keyed on the stored values: React resets the form after saving, so it remounts with them. */}
          <BillingForm
            key={JSON.stringify([b.enabled, b.currency, b.automatic_tax, b.prices, Boolean(key)])}
            view={{
              enabled: b.enabled,
              hasKey: Boolean(key),
              keyMode: key?.includes("_live_") ? "live" : key ? "test" : null,
              hasWebhookSecret: Boolean(decryptSecret(b.webhook_secret_enc)),
              currency: b.currency,
              automaticTax: b.automatic_tax,
              prices: b.prices,
              pack: s.team_seat_pack,
            }}
          />
        </Panel>
        <div className="flex flex-col gap-6">
          <Panel title="Set up Stripe" bodyClassName="p-4">
            <SetupStripe disabled={!key} />
            <p className="mt-3 text-[12px] text-muted">
              Webhook URL: <span className="font-mono text-ink-2">{webhookUrl()}</span>
              {b.webhook_id ? <span className="block">Endpoint {b.webhook_id}</span> : null}
            </p>
          </Panel>
          <Panel title="Subscriptions" bodyClassName="p-4">
            {subs.length ? (
              <ul className="space-y-1 text-[13px] text-ink-2">
                {subs.map((r) => (
                  <li key={`${r.plan}-${r.billing_interval}`}>
                    <span className="font-medium text-ink">{r.n}</span> {PLAN_LABEL[r.plan as "pro"]}, {r.billing_interval === "year" ? "yearly" : "monthly"}
                    {Number(r.packs) ? `, ${r.packs} extra seat packs` : ""}
                  </li>
                ))}
                <li className="pt-1 text-ink">About {formatPrice(Math.round(mrr), b.currency)} per month</li>
              </ul>
            ) : (
              <p className="text-[13px] text-muted">No active subscriptions.</p>
            )}
            <p className="mt-3 text-[12.5px] text-ink-2">
              Granted without paying: {granted.users} {granted.users === 1 ? "account" : "accounts"} with Pro, {granted.teams}{" "}
              {granted.teams === 1 ? "team" : "teams"} (Admin → Users and Teams).
            </p>
          </Panel>
        </div>
      </div>
    </>
  );
}
