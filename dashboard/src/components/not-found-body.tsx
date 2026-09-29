"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { buttonClass } from "./ui";

export function NotFoundBody({ baseDomain, dashboardHost }: { baseDomain: string; dashboardHost: string }) {
  const pathname = usePathname();
  const looksLikeRequest = pathname.startsWith("/inspect") || pathname.startsWith("/api/requests");
  const links = [
    { href: "/", label: "Overview", body: "What's online and how much traffic it carried." },
    { href: "/inspect", label: "Inspect", body: "Every captured request, live." },
    { href: "/tunnels", label: "Tunnels", body: "Online and recently closed tunnels." },
    { href: "/get-started", label: "Get started", body: "Install the client and open a tunnel." },
  ];
  return (
    <main className="mx-auto max-w-2xl py-16 sm:py-24">
      <div className="mb-8 flex items-center gap-3 font-mono text-[12.5px]" aria-hidden>
        <span className="rounded-[5px] border border-line-strong bg-surface px-2 py-1 text-ink">{dashboardHost}</span>
        <span className="route-tube flex-1" data-lit="false" />
        <span className="max-w-[40%] truncate rounded-[5px] border border-dashed border-line-strong px-2 py-1 text-muted">
          {pathname}
        </span>
      </div>
      <p className="font-mono text-[13px] text-muted">404</p>
      <h1 className="mt-1 text-[30px] font-semibold leading-tight tracking-[-0.02em] text-ink">Nothing is routed here</h1>
      <p className="mt-3 max-w-[60ch] text-[15px] text-ink-2">
        The dashboard has no page at <code className="break-all font-mono text-[13px] text-ink">{pathname}</code>. The
        link may be mistyped, or it pointed at something that has since been removed.
      </p>

      <ul className="mt-6 flex flex-col gap-3 text-[13.5px] text-ink-2">
        <li className="border-l-2 border-line-strong pl-3">
          <span className="font-medium text-ink">Looking for a tunnel?</span> Tunnels live on their own hostnames, such as{" "}
          <code className="font-mono text-[12.5px] text-ink">https://my-app.{baseDomain}</code>, not under the dashboard.
        </li>
        {looksLikeRequest ? (
          <li className="border-l-2 border-line-strong pl-3">
            <span className="font-medium text-ink">Following a link to a request?</span> Captured requests are deleted
            after the retention period, or when someone clears them.
          </li>
        ) : null}
      </ul>

      <nav aria-label="Dashboard pages" className="mt-10 grid gap-px overflow-hidden rounded-lg border border-line bg-line sm:grid-cols-2">
        {links.map((l) => (
          <Link key={l.href} href={l.href} className="bg-surface px-4 py-3 hover:bg-surface-2">
            <span className="text-[14px] font-medium text-ink">{l.label}</span>
            <span className="mt-0.5 block text-[12.5px] text-muted">{l.body}</span>
          </Link>
        ))}
      </nav>
      <div className="mt-8">
        <Link href="/" className={buttonClass("primary")}>
          Back to overview
        </Link>
      </div>
    </main>
  );
}
