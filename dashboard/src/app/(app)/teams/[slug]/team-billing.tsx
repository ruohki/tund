"use client";

import { useActionState, useState } from "react";
import { setSeatPacksAction, subscribeTeamAction } from "@/app/actions/billing";
import { cn, FormMessage, Input } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

export type TeamPrices = {
  currency: string;
  team: { month: number; year: number; seatsMonth: number; seatsYear: number };
  team_pro: { month: number; year: number; seatsMonth: number; seatsYear: number };
  seats: number;
  pack: number;
};

const money = (cents: number, currency: string) =>
  new Intl.NumberFormat("en-US", { style: "currency", currency: currency.toUpperCase(), minimumFractionDigits: cents % 100 ? 2 : 0 }).format(
    cents / 100,
  );

/** Team owners pick Team or Team Pro, monthly or yearly, and extra seats; Stripe Checkout does the rest. */
export function SubscribeTeam({ teamId, prices, members }: { teamId: string; prices: TeamPrices; members: number }) {
  const [plan, setPlan] = useState<"team" | "team_pro">("team");
  const [interval, setInterval] = useState<"month" | "year">("month");
  const minPacks = Math.max(0, Math.ceil((members - prices.seats) / prices.pack));
  const [packs, setPacks] = useState(minPacks);
  const p = prices[plan];
  const base = interval === "month" ? p.month : p.year;
  const seat = interval === "month" ? p.seatsMonth : p.seatsYear;
  const total = base + packs * seat;
  const option = (active: boolean) =>
    cn(
      "flex-1 cursor-pointer rounded-md border px-3 py-2.5 text-left text-[13px] transition-colors",
      active ? "border-ink bg-surface-2" : "border-line-strong hover:bg-surface-2",
    );
  return (
    <form action={subscribeTeamAction} className="flex flex-col gap-4">
      <input type="hidden" name="team_id" value={teamId} />
      <input type="hidden" name="plan" value={plan} />
      <input type="hidden" name="interval" value={interval} />
      <div className="flex flex-col gap-2 sm:flex-row" role="radiogroup" aria-label="Plan">
        <button type="button" role="radio" aria-checked={plan === "team"} onClick={() => setPlan("team")} className={option(plan === "team")}>
          <span className="block font-medium text-ink">Team</span>
          <span className="text-ink-2">
            {money(prices.team.month, prices.currency)} / month. Shared custom domain, Pro for you as the owner.
          </span>
        </button>
        <button type="button" role="radio" aria-checked={plan === "team_pro"} onClick={() => setPlan("team_pro")} className={option(plan === "team_pro")}>
          <span className="block font-medium text-ink">Team Pro</span>
          <span className="text-ink-2">{money(prices.team_pro.month, prices.currency)} / month. Like Team, and every member gets Pro.</span>
        </button>
      </div>
      <div className="flex flex-wrap items-end gap-4">
        <div role="radiogroup" aria-label="Billing period" className="inline-flex h-8.5 items-center rounded-[5px] border border-line-strong p-0.5">
          {(["month", "year"] as const).map((i) => (
            <button
              key={i}
              type="button"
              role="radio"
              aria-checked={interval === i}
              onClick={() => setInterval(i)}
              className={cn("h-full rounded-[3px] px-3 text-[13px]", interval === i ? "bg-surface-3 text-ink" : "text-muted hover:text-ink")}
            >
              {i === "month" ? "Monthly" : "Yearly"}
            </button>
          ))}
        </div>
        <label className="flex flex-col gap-1.5 text-[13px] font-medium text-ink">
          Extra seats, in packs of {prices.pack}
          <Input
            name="packs"
            type="number"
            min={minPacks}
            max={100}
            value={packs}
            onChange={(e) => setPacks(Math.max(minPacks, Math.min(100, Number(e.target.value) || 0)))}
            className="w-28"
          />
        </label>
      </div>
      <p className="text-[13px] text-ink-2">
        {prices.seats + packs * prices.pack} members for{" "}
        <span className="font-medium text-ink">
          {money(total, prices.currency)} / {interval}
        </span>
        {packs ? ` (${money(base, prices.currency)} plus ${packs} × ${money(seat, prices.currency)} for extra seats)` : ""}. Checkout,
        invoices and cancelling are handled by Stripe.
      </p>
      <div>
        <SubmitButton pendingText="Opening checkout…">Continue to checkout</SubmitButton>
      </div>
    </form>
  );
}

/** The paying owner changes the extra seat packs; billed pro rata right away. */
export function SeatPacks({
  teamId,
  packs,
  prices,
  members,
  interval,
  plan,
}: {
  teamId: string;
  packs: number;
  prices: TeamPrices;
  members: number;
  interval: "month" | "year";
  plan: "team" | "team_pro";
}) {
  const [state, action] = useActionState(setSeatPacksAction, null);
  const [value, setValue] = useState(packs);
  const seat = interval === "month" ? prices[plan].seatsMonth : prices[plan].seatsYear;
  return (
    <form action={action} className="flex flex-col gap-2">
      <input type="hidden" name="team_id" value={teamId} />
      <div className="flex flex-wrap items-end gap-2">
        <label className="flex flex-col gap-1.5 text-[13px] font-medium text-ink">
          Extra seat packs ({prices.pack} seats, {money(seat, prices.currency)} / {interval} each)
          <Input
            name="packs"
            type="number"
            min={0}
            max={100}
            value={value}
            onChange={(e) => setValue(Math.max(0, Math.min(100, Number(e.target.value) || 0)))}
            className="w-28"
          />
        </label>
        <SubmitButton variant="secondary" pendingText="Updating…" disabled={value === packs}>
          {value > packs ? "Buy seats" : "Update seats"}
        </SubmitButton>
      </div>
      <p className="text-[12px] text-muted">
        {prices.seats + value * prices.pack} seats for {members} {members === 1 ? "member" : "members"}. Changes are charged or credited
        pro rata right away.
      </p>
      <FormMessage state={state} />
    </form>
  );
}

export function PortalButton({ slug }: { slug: string }) {
  return (
    <>
      <input type="hidden" name="return" value="team" />
      <input type="hidden" name="slug" value={slug} />
      <SubmitButton variant="secondary" size="sm" pendingText="Opening…">
        Manage subscription
      </SubmitButton>
    </>
  );
}
