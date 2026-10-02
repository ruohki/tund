"use client";

import { useActionState } from "react";
import { saveBillingAction, setupStripeAction } from "@/app/actions/billing";
import { Field, FormMessage, inputClass, cn } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";
import type { PriceKey } from "@/lib/settings";

const PRICE_ROWS: { label: string; month: PriceKey; year: PriceKey }[] = [
  { label: "Pro", month: "pro_month", year: "pro_year" },
  { label: "Team", month: "team_month", year: "team_year" },
  { label: "Team: extra seat pack", month: "team_seats_month", year: "team_seats_year" },
  { label: "Team Pro", month: "team_pro_month", year: "team_pro_year" },
  { label: "Team Pro: extra seat pack", month: "team_pro_seats_month", year: "team_pro_seats_year" },
];

export type BillingFormView = {
  enabled: boolean;
  hasKey: boolean;
  keyMode: "test" | "live" | null;
  hasWebhookSecret: boolean;
  currency: string;
  automaticTax: boolean;
  prices: Record<PriceKey, number>;
  pack: number;
};

export function BillingForm({ view }: { view: BillingFormView }) {
  const [state, action] = useActionState(saveBillingAction, null);
  const dollars = (c: number) => (c / 100).toFixed(c % 100 ? 2 : 0);
  return (
    <form action={action} className="flex flex-col gap-4">
      <label className="flex items-start gap-2.5 text-[13.5px] text-ink">
        <input type="checkbox" name="enabled" defaultChecked={view.enabled} className="mt-0.5 h-4 w-4 accent-[var(--ink)]" />
        <span>
          <span className="font-medium">Billing on</span>
          <span className="block text-[12.5px] text-muted">
            Shows plans and checkout to everyone, and limits teams without a plan to their owner. Off: no paywall; admins can still
            grant Pro and team plans.
          </span>
        </span>
      </label>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label="Stripe secret key"
          htmlFor="billing-key"
          hint={view.hasKey ? `A ${view.keyMode ?? ""} key is stored encrypted. Leave empty to keep it.` : "sk_live_… or sk_test_… (or a restricted key with the same rights)."}
        >
          <input id="billing-key" name="secret_key" type="password" autoComplete="off" className={inputClass} placeholder={view.hasKey ? "••••••••" : "sk_…"} />
        </Field>
        <Field
          label="Webhook signing secret"
          htmlFor="billing-whsec"
          hint={view.hasWebhookSecret ? "Stored. “Set up Stripe” creates the endpoint and saves it for you." : "Filled in by “Set up Stripe”; or paste whsec_… of an endpoint you created."}
        >
          <input id="billing-whsec" name="webhook_secret" type="password" autoComplete="off" className={inputClass} placeholder={view.hasWebhookSecret ? "••••••••" : "whsec_…"} />
        </Field>
      </div>
      <div className="flex flex-wrap items-end gap-4">
        <Field label="Currency" htmlFor="billing-currency">
          <input id="billing-currency" name="currency" defaultValue={view.currency} maxLength={3} className={cn(inputClass, "w-24 uppercase")} />
        </Field>
        <label className="flex items-center gap-2.5 pb-2 text-[13.5px] text-ink">
          <input type="checkbox" name="automatic_tax" defaultChecked={view.automaticTax} className="h-4 w-4 accent-[var(--ink)]" />
          Stripe Tax (calculate VAT and sales tax; set it up in Stripe first)
        </label>
      </div>
      <div className="overflow-x-auto scroll-thin rounded-md border border-line">
        <table className="w-full min-w-[460px] text-[13px]">
          <thead>
            <tr className="border-b border-line text-left text-[12px] text-muted">
              <th className="px-3 py-2 font-medium">Price ({view.currency.toUpperCase()})</th>
              <th className="px-3 py-2 font-medium">Monthly</th>
              <th className="px-3 py-2 font-medium">Yearly</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {PRICE_ROWS.map((r) => (
              <tr key={r.month}>
                <th scope="row" className="px-3 py-1.5 text-left font-normal text-ink-2">
                  {r.label}
                  {r.label.includes("seat") ? <span className="text-muted"> ({view.pack} members)</span> : null}
                </th>
                {[r.month, r.year].map((k) => (
                  <td key={k} className="px-3 py-1.5">
                    <input
                      name={`price_${k}`}
                      aria-label={`${r.label}, ${k.endsWith("year") ? "yearly" : "monthly"}`}
                      defaultValue={dollars(view.prices[k])}
                      inputMode="decimal"
                      className={cn(inputClass, "h-7.5 w-28 tabular")}
                    />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <FormMessage state={state} />
      <div>
        <SubmitButton pendingText="Saving…">Save</SubmitButton>
      </div>
    </form>
  );
}

export function SetupStripe({ disabled }: { disabled: boolean }) {
  const [state, action] = useActionState(async () => setupStripeAction(), null);
  return (
    <form action={action} className="flex flex-col gap-3">
      <p className="text-[13px] text-ink-2">
        Creates the products and prices in Stripe (changed prices replace the old ones for new subscriptions) and a webhook endpoint
        that keeps subscriptions in sync. Run it again after changing keys or prices.
      </p>
      <div>
        <SubmitButton variant="secondary" pendingText="Setting up…" disabled={disabled}>
          Set up Stripe
        </SubmitButton>
      </div>
      {state?.error ? <p className="text-[13px] text-danger">{state.error}</p> : null}
      {state?.log ? (
        <ul className="list-disc space-y-0.5 pl-5 text-[13px] text-ok">
          {state.log.map((l) => (
            <li key={l}>{l}</li>
          ))}
        </ul>
      ) : null}
    </form>
  );
}
