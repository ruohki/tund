import type { Metadata } from "next";
import Link from "next/link";
import { Check, Minus } from "lucide-react";
import { requireUser } from "@/lib/auth";
import { getSettings } from "@/lib/settings";
import { formatDateTime, formatKbps, formatLifetime } from "@/lib/format";
import { billingEnabled, PLAN_LABEL, planLimits, proSources, teamBilling, type AccountPlan } from "@/lib/plans";
import { formatPrice, proSubscription } from "@/lib/billing";
import { listMyTeams } from "@/lib/teams";
import { db } from "@/lib/db";
import { Badge, buttonClass, PageHeader, Panel } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";
import { billingPortalAction, subscribeProAction } from "@/app/actions/billing";

export const metadata: Metadata = { title: "Billing" };

const count = (n: number) => (n ? n.toLocaleString("en") : "Unlimited");

function rows(p: AccountPlan): [string, React.ReactNode][] {
  return [
    ["Online tunnels", count(p.tunnels)],
    ["Static hostnames and TCP ports", count(p.pinned)],
    ["Custom domains", p.customDomains ? count(p.domains) : <Minus key="no" size={14} className="text-muted" aria-label="No" />],
    ["TCP and TLS tunnels", p.passthrough ? <Check key="yes" size={14} className="text-ok" aria-label="Yes" /> : <Minus key="no" size={14} className="text-muted" aria-label="No" />],
    ["Transfer per month", p.transferGb ? `${p.transferGb.toLocaleString("en")} GB` : "Unlimited"],
    ["Speed", formatKbps(p.bandwidthKbps).replace("unlimited", "Unlimited")],
    ["Tunnel lifetime", formatLifetime(p.lifetimeMinutes).replace("unlimited", "Unlimited")],
    ["Teams you can own", count(p.teams)],
  ];
}

export default async function BillingPage({ searchParams }: PageProps<"/billing">) {
  const user = await requireUser();
  const { checkout, error } = await searchParams;
  const [s, on, sources, sub, myTeams, [cust]] = await Promise.all([
    getSettings(),
    billingEnabled(),
    proSources(user.id),
    proSubscription(user.id),
    listMyTeams(user.id),
    db()`select stripe_customer_id from users where id = ${user.id}`,
  ]);
  const free = planLimits(s, false);
  const pro = planLimits(s, true);
  const isPro = sources.granted || sources.subscription || sources.teams.length > 0;
  const prices = s.billing.prices;
  const cur = s.billing.currency;
  const yearlySaving = Math.round(100 - (prices.pro_year / (prices.pro_month * 12)) * 100);
  const teams = await Promise.all(myTeams.map(async (t) => ({ ...t, billing: await teamBilling(t.id) })));

  const status = sub?.active
    ? `Pro, billed ${sub.interval === "year" ? "yearly" : "monthly"}${
        sub.periodEnd ? `; ${sub.cancelAtPeriodEnd ? "ends" : "renews"} on ${formatDateTime(sub.periodEnd).split(",").slice(0, 2).join(",")}` : ""
      }${sub.status === "past_due" ? ". The last payment failed; update your payment method." : "."}`
    : sources.granted
      ? "Pro, granted by the administrators of this server."
      : sources.teams.length
        ? `Pro through ${sources.teams.map((t) => `${t.name} (${PLAN_LABEL[t.plan]})`).join(", ")}.`
        : "Free.";

  return (
    <>
      <PageHeader title="Billing" description="Your plan, what it includes, and your teams' plans." />
      {checkout === "done" ? (
        <p role="status" className="mb-5 rounded-md border border-ok/30 bg-ok-wash px-3 py-2 text-[13px] text-ok">
          Thank you! Your plan updates as soon as Stripe confirms the payment, usually within a few seconds. Reload if it hasn&apos;t yet.
        </p>
      ) : null}
      {typeof error === "string" && error ? (
        <p role="alert" className="mb-5 rounded-md border border-danger/30 bg-danger-wash px-3 py-2 text-[13px] text-danger">
          {error}
        </p>
      ) : null}

      <Panel
        title="Your plan"
        description={status}
        actions={
          cust?.stripe_customer_id && on ? (
            <form action={billingPortalAction}>
              <SubmitButton variant="secondary" size="sm" pendingText="Opening…">
                {sub?.active ? "Manage subscription" : "Invoices and payment methods"}
              </SubmitButton>
            </form>
          ) : null
        }
        className="mb-6"
      >
        <div className="overflow-x-auto scroll-thin">
          <table className="w-full min-w-[520px] text-[13px]">
            <thead>
              <tr className="border-b border-line text-left text-[12px] text-muted">
                <th className="px-4 py-2 font-medium" />
                <th className="w-1/4 px-4 py-2 font-medium">
                  Free {!isPro ? <Badge tone="outline">Current</Badge> : null}
                </th>
                <th className="w-1/4 px-4 py-2 font-medium">
                  Pro {isPro ? <Badge tone="ok">Current</Badge> : null}
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {rows(free).map(([label, f], i) => (
                <tr key={label}>
                  <th scope="row" className="px-4 py-2 text-left font-normal text-ink-2">
                    {label}
                  </th>
                  <td className="px-4 py-2 text-ink">{f}</td>
                  <td className="px-4 py-2 font-medium text-ink">{rows(pro)[i][1]}</td>
                </tr>
              ))}
              <tr>
                <th scope="row" className="px-4 py-2 text-left font-normal text-ink-2">
                  Browser warning page, custom domain review
                </th>
                <td className="px-4 py-2 text-ink">As set by the server</td>
                <td className="px-4 py-2 font-medium text-ink">Not for paying accounts</td>
              </tr>
            </tbody>
          </table>
        </div>
        {on && !isPro ? (
          <div className="flex flex-wrap items-center gap-3 border-t border-line px-4 py-3">
            <form action={subscribeProAction} className="flex flex-wrap gap-2">
              <input type="hidden" name="interval" value="month" />
              <SubmitButton pendingText="Opening checkout…">Upgrade: {formatPrice(prices.pro_month, cur)} / month</SubmitButton>
            </form>
            <form action={subscribeProAction}>
              <input type="hidden" name="interval" value="year" />
              <SubmitButton variant="secondary" pendingText="Opening checkout…">
                {formatPrice(prices.pro_year, cur)} / year{yearlySaving > 0 ? ` (save ${yearlySaving}%)` : ""}
              </SubmitButton>
            </form>
            <p className="text-[12px] text-muted">Checkout and invoices by Stripe. Cancel any time; Pro stays until the end of the paid period.</p>
          </div>
        ) : null}
      </Panel>

      <Panel
        title="Team plans"
        description={
          on
            ? `Team: ${formatPrice(prices.team_month, cur)} / month or ${formatPrice(prices.team_year, cur)} / year, ${s.team_seats} members, ${s.team_custom_domains ? `${s.team_custom_domains} shared custom ${s.team_custom_domains === 1 ? "domain" : "domains"}` : "shared custom domains"}, Pro for the owner who pays. Team Pro: ${formatPrice(prices.team_pro_month, cur)} / month, Pro for every member. More members: ${formatPrice(prices.team_seats_month, cur)} / month per ${s.team_seat_pack}.`
            : "Plans of the teams you belong to."
        }
      >
        {teams.length ? (
          <ul className="divide-y divide-line">
            {teams.map((t) => (
              <li key={t.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5">
                <div className="min-w-0">
                  <p className="flex items-center gap-2 text-[13.5px] text-ink">
                    <Link href={`/teams/${t.slug}`} className="font-medium hover:underline">
                      {t.name}
                    </Link>
                    {t.billing.plan ? <Badge tone="ok">{PLAN_LABEL[t.billing.plan]}</Badge> : <Badge tone="outline">No plan</Badge>}
                    {t.billing.granted ? <Badge tone="outline">Provided</Badge> : null}
                  </p>
                  <p className="text-[12px] text-muted">
                    {t.billing.members} {t.billing.members === 1 ? "member" : "members"}
                    {t.billing.seats !== null ? ` of ${t.billing.seats} seats` : ""}, you&apos;re {t.role}
                  </p>
                </div>
                <Link href={`/teams/${t.slug}#billing`} className={buttonClass("ghost", "sm")}>
                  {t.billing.plan || !on ? "Open" : t.role === "owner" ? "Subscribe" : "Open"}
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <p className="px-4 py-3 text-[13px] text-ink-2">
            You&apos;re not in a team.{" "}
            <Link href="/teams" className="font-medium text-ink underline underline-offset-4">
              Create one
            </Link>{" "}
            to share domains and identity providers.
          </p>
        )}
      </Panel>
    </>
  );
}
