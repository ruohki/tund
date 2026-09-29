import { Gauge } from "lucide-react";
import type { User } from "@/lib/auth";
import { formatKbps, formatNumber, formatTransfer } from "@/lib/format";
import { dailyUsage, effectiveLimits, GB, monthUsage } from "@/lib/usage";
import { Panel } from "./ui";
import { UsageChart } from "./usage-chart";

const resetDate = (iso: string) =>
  new Date(iso).toLocaleDateString("en", { month: "long", day: "numeric", year: "numeric", timeZone: "UTC" });

/** Shown on the overview when this month's quota is used up. */
export async function QuotaBanner({ user }: { user: User }) {
  const [limits, usage] = await Promise.all([effectiveLimits(user), monthUsage(user.id)]);
  if (!limits.transferGb) return null;
  const used = usage.bytesIn + usage.bytesOut;
  if (used < limits.transferGb * GB) return null;
  return (
    <p role="alert" className="mb-6 flex items-start gap-2.5 rounded-lg border border-danger/40 bg-danger-wash px-4 py-3 text-[13.5px] text-ink">
      <Gauge size={16} className="mt-0.5 shrink-0 text-danger" />
      <span>
        <span className="font-semibold">Your monthly transfer of {limits.transferGb} GB is used up.</span> Tunnels answer
        visitors with “509 Bandwidth Limit Exceeded” and new tunnels are refused until the quota resets on{" "}
        {resetDate(usage.periodEnd)}. Ask an administrator if you need more.
      </span>
    </p>
  );
}

/** "Transfer this month: X of Y" with a meter and the daily chart. */
export async function TransferUsage({ user, className }: { user: User; className?: string }) {
  const [limits, usage, days] = await Promise.all([effectiveLimits(user), monthUsage(user.id), dailyUsage(user.id)]);
  const used = usage.bytesIn + usage.bytesOut;
  const quota = limits.transferGb * GB;
  const ratio = quota ? Math.min(1, used / quota) : 0;
  const tone = !quota ? "var(--s3xx)" : ratio >= 1 ? "var(--danger)" : ratio >= 0.8 ? "var(--s4xx)" : "var(--s3xx)";
  return (
    <Panel
      title="Transfer this month"
      description={`Everything through your tunnels, both directions. Resets on ${resetDate(usage.periodEnd)} (UTC).`}
      className={className}
    >
      <div className="grid gap-4 border-b border-line px-4 py-3.5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
        <div>
          <p className="text-[24px] font-semibold leading-tight tracking-[-0.02em] text-ink">
            {formatTransfer(used)}
            <span className="ml-1.5 text-[14px] font-normal text-muted">
              {quota ? `of ${limits.transferGb} GB` : "no monthly limit"}
            </span>
          </p>
          {quota ? (
            <div
              className="mt-2 h-2 rounded-full bg-surface-3"
              role="meter"
              aria-valuemin={0}
              aria-valuemax={quota}
              aria-valuenow={Math.min(used, quota)}
              aria-label="Monthly transfer used"
            >
              <div className="h-2 rounded-full" style={{ width: `${Math.max(1, ratio * 100)}%`, background: tone }} />
            </div>
          ) : null}
        </div>
        <dl className="flex gap-5 text-[12.5px]">
          <div>
            <dt className="text-muted">Requests</dt>
            <dd className="font-medium text-ink tabular">{formatNumber(usage.requests)}</dd>
          </div>
          <div>
            <dt className="text-muted">Connections</dt>
            <dd className="font-medium text-ink tabular">{formatNumber(usage.connections)}</dd>
          </div>
          <div>
            <dt className="text-muted">Speed limit</dt>
            <dd className="font-medium text-ink">{formatKbps(limits.bandwidthKbps)}</dd>
          </div>
        </dl>
      </div>
      <div className="p-4">
        <UsageChart days={days} />
      </div>
    </Panel>
  );
}
