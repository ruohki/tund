import type { Metadata } from "next";
import Link from "next/link";
import { publicConfig } from "@/lib/config";
import { SiteWordmark } from "@/components/site-wordmark";
import { NotFoundBody } from "@/components/not-found-body";

export const metadata: Metadata = { title: "Page not found", robots: { index: false, follow: false } };

export default function NotFound() {
  const cfg = publicConfig();
  return (
    <div className="min-h-dvh px-6 py-8 sm:px-12">
      <Link href="/" className="inline-block">
        <SiteWordmark />
      </Link>
      <NotFoundBody baseDomain={cfg.baseDomain} dashboardHost={cfg.dashboardHost} />
    </div>
  );
}
