import { AlertTriangle, RefreshCw } from "lucide-react";
import { riskFlags, type DomainRisk } from "@/lib/abuse";
import { timeAgo } from "@/lib/format";
import { buttonClass } from "@/components/ui";
import { recheckDomainAction } from "@/app/actions/abuse";
import { ReviewDomainForm } from "../abuse/abuse-forms";
import { ApprovalBadge } from "./approval-badge";

/** Risk signals of a custom domain plus approve / reject (docs/SPEC.md "Custom domain review"). */
export function DomainReview({
  domain,
}: {
  domain: { id: string; hostname: string; approval: string; verified: boolean; risk: DomainRisk | null };
}) {
  const r = domain.risk ?? {};
  const flags = riskFlags(r);
  const facts: [string, string][] = [
    ["Registrable domain", r.registrable ?? "…"],
    [
      "Registered",
      r.registered_at ? `${r.registered_at} (${r.age_days} days ago)` : r.rdap_error ? r.rdap_error : r.checked_at ? "unknown" : "looking up…",
    ],
    ["Blocked words", r.deceptive_words?.length ? r.deceptive_words.join(", ") : "none"],
    ["Internationalized", r.idn ? (r.unicode ?? "yes") : "no"],
    [
      "Safe Browsing",
      r.safe_browsing
        ? r.safe_browsing
        : r.safe_browsing_error
          ? r.safe_browsing_error
          : r.safe_browsing === null
            ? "no match"
            : "not checked (no API key)",
    ],
    ["DNS ownership", domain.verified ? "verified" : "not verified yet"],
  ];
  return (
    <div className="flex flex-col gap-3 p-4 text-[13px]">
      <p className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-[12.5px] text-ink">{domain.hostname}</span>
        <ApprovalBadge approval={domain.approval} />
      </p>
      {flags.length ? (
        <ul className="flex flex-col gap-1 rounded-md border border-danger/30 bg-danger-wash px-3 py-2">
          {flags.map((f) => (
            <li key={f} className="flex items-center gap-2 text-ink">
              <AlertTriangle size={13} className="shrink-0 text-danger" /> {f}
            </li>
          ))}
        </ul>
      ) : r.checked_at ? (
        <p className="text-ink-2">No risk signals found.</p>
      ) : null}
      <dl className="grid grid-cols-[9rem_minmax(0,1fr)] gap-x-3 gap-y-1">
        {facts.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted">{k}</dt>
            <dd className="break-words text-ink-2">{v}</dd>
          </div>
        ))}
      </dl>
      {r.review ? (
        <p className="text-ink-2">
          {r.review.decision === "approved" ? "Approved" : "Rejected"} by {r.review.by} {timeAgo(r.review.at)}
          {r.review.reason ? `: ${r.review.reason}` : ""}
        </p>
      ) : null}
      <ReviewDomainForm id={domain.id} approval={domain.approval} />
      <form action={recheckDomainAction}>
        <input type="hidden" name="id" value={domain.id} />
        <button type="submit" className={buttonClass("ghost", "sm", "-ml-2")}>
          <RefreshCw size={13} /> Check again{r.checked_at ? ` (last ${timeAgo(r.checked_at)})` : ""}
        </button>
      </form>
    </div>
  );
}
