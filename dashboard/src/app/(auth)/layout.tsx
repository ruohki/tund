import Link from "next/link";
import { SiteWordmark } from "@/components/site-wordmark";
import { publicConfig } from "@/lib/config";
import { hasImprint } from "@/lib/legal";

export default async function AuthLayout({ children }: { children: React.ReactNode }) {
  const c = publicConfig();
  const imprint = await hasImprint();
  const link = "underline-offset-4 hover:text-ink hover:underline";
  return (
    <div className="grid min-h-dvh lg:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]">
      <div className="flex flex-col px-6 py-8 sm:px-12">
        <SiteWordmark />
        <div className="flex flex-1 items-center">
          <div className="w-full max-w-sm py-12">{children}</div>
        </div>
        <p className="flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-muted">
          <span>{c.dashboardHost}</span>
          <Link href="/terms" className={link}>
            Terms
          </Link>
          <Link href="/privacy" className={link}>
            Privacy
          </Link>
          {imprint ? (
            <Link href="/imprint" className={link}>
              Imprint
            </Link>
          ) : null}
        </p>
      </div>
      <div className="relative hidden overflow-hidden border-l border-line bg-surface lg:flex lg:flex-col lg:justify-center lg:px-14">
        <div className="max-w-lg">
          <p className="text-[34px] font-semibold leading-[1.1] tracking-[-0.02em] text-ink">
            Your laptop,
            <br />
            on a public HTTPS address.
          </p>
          <p className="mt-4 max-w-[46ch] text-[15px] text-ink-2">
            Run <code className="font-mono text-[13px] text-ink">tund http 3000</code> and every request to your tunnel is
            recorded here with headers, bodies and timings, ready to inspect and replay.
          </p>
          <div className="mt-10 flex items-center gap-4 font-mono text-[12.5px]" aria-hidden>
            <span className="rounded-md border border-line-strong bg-surface-2 px-2.5 py-1.5 text-ink">
              {c.scheme}://*.{c.baseDomain}
            </span>
            <span className="route-tube flex-1" data-lit="true">
              <span className="route-pulse" style={{ animationIterationCount: "infinite", animationDuration: "2.4s" }} />
            </span>
            <span className="rounded-md border border-line-strong bg-surface-2 px-2.5 py-1.5 text-ink">localhost:3000</span>
          </div>
        </div>
      </div>
    </div>
  );
}
