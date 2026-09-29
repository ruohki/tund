import type { Metadata } from "next";
import { abuseHref } from "@/lib/config";
import { publicPageMetadata, siteInfo } from "@/lib/seo";
import { getSettings } from "@/lib/settings";
import { turnstileSiteKey } from "@/lib/turnstile";
import { ReportForm } from "./report-form";

export async function generateMetadata(): Promise<Metadata> {
  const { name } = await siteInfo();
  return publicPageMetadata(
    "/report",
    "Report abuse",
    `Report a ${name} tunnel that is used for phishing, malware, fraud or other abuse.`,
  );
}

export default async function ReportPage({ searchParams }: PageProps<"/report">) {
  const { host } = await searchParams;
  const [{ name, baseDomain }, settings, siteKey] = await Promise.all([siteInfo(), getSettings(), turnstileSiteKey()]);
  const contact = abuseHref(settings.abuse_contact);
  return (
    <div className="max-w-[560px]">
      <h1 className="text-[30px] font-semibold leading-tight tracking-[-0.02em] text-ink">Report abuse</h1>
      <p className="mt-2 text-[15px] leading-relaxed text-ink-2">
        Tell the operators of {name} about a tunnel used for phishing, malware, fraud or anything else that harms
        people. Reports are read by a person, so describe what you saw.
      </p>
      <div className="mt-8">
        <ReportForm
          host={typeof host === "string" ? host.slice(0, 300) : ""}
          placeholder={`something.${baseDomain}`}
          siteKey={siteKey}
        />
      </div>
      {contact ? (
        <p className="mt-8 text-[13px] text-muted">
          Urgent, or about something other than a single address?{" "}
          <a href={contact} className="font-medium text-ink-2 underline underline-offset-4 hover:text-ink" rel="noreferrer">
            Contact the operators directly
          </a>
          .
        </p>
      ) : null}
    </div>
  );
}
