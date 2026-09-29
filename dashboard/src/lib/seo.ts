import "server-only";
import { config } from "./config";
import { getSettings } from "./settings";

export const TAGLINE = "Share localhost on a public HTTPS address";
export const DESCRIPTION =
  "Expose a local port on an HTTPS URL that stays the same every run. Inspect and replay every request, protect tunnels with a password or single sign-on, use your own domains, or run TCP and TLS tunnels.";

/** Instance name and public URL for metadata; tolerant of a missing database (e.g. during a build). */
export async function siteInfo() {
  const name = await getSettings().then(
    (s) => s.instance_name || "TUNd",
    () => "TUNd",
  );
  const c = config();
  return { name, url: c.dashboardUrl, host: c.dashboardHost, baseDomain: c.baseDomain };
}

/** Metadata for public, indexable pages. */
export async function publicPageMetadata(path: string, title: string, description: string) {
  const { name } = await siteInfo();
  return {
    title,
    description,
    alternates: { canonical: path },
    robots: { index: true, follow: true },
    openGraph: { type: "website" as const, url: path, siteName: name, title, description, locale: "en_US" },
    twitter: { card: "summary_large_image" as const, title, description },
  };
}
