import "server-only";
import { config } from "./config";
import { getSettings } from "./settings";

export const TAGLINE = "Share localhost on a public HTTPS address";
/** Meta description and link-preview text: complements TAGLINE, stays under ~160 characters. */
export const DESCRIPTION =
  "One command gives your local app a stable HTTPS URL. Watch and replay every request, and put a password or single sign-on in front when you need it.";

export const OG_SIZE = { width: 1200, height: 630 };
export const OG_ALT = `${TAGLINE}: tund http 3000 gives a local port an HTTPS URL`;

// Named explicitly because a page's own openGraph replaces its parents'
// (including the root opengraph-image), which left /login without an image.
const OG_IMAGES = [{ url: "/opengraph-image", ...OG_SIZE, alt: OG_ALT, type: "image/png" }];
const TWITTER_IMAGES = [{ url: "/twitter-image", ...OG_SIZE, alt: OG_ALT }];

/** Instance name and public URL for metadata; tolerant of a missing database (e.g. during a build). */
export async function siteInfo() {
  const name = await getSettings().then(
    (s) => s.instance_name || "TUNd",
    () => "TUNd",
  );
  const c = config();
  return { name, url: c.dashboardUrl, host: c.dashboardHost, baseDomain: c.baseDomain };
}

/**
 * Metadata for public, indexable pages. With `card: "product"` a shared link
 * previews as the product (name, tagline, description) rather than the page:
 * for sign-in and sign-up, where `/` sends visitors when sign-up is closed.
 */
export async function publicPageMetadata(path: string, title: string, description: string, card: "page" | "product" = "page") {
  const { name } = await siteInfo();
  const preview = card === "product" ? { title: `${name}: ${TAGLINE}`, description: DESCRIPTION } : { title, description };
  return {
    title,
    description,
    alternates: { canonical: path },
    robots: { index: true, follow: true },
    openGraph: { type: "website" as const, url: path, siteName: name, locale: "en_US", images: OG_IMAGES, ...preview },
    twitter: { card: "summary_large_image" as const, images: TWITTER_IMAGES, ...preview },
  };
}

/** The root layout's defaults, for pages that don't set their own. */
export function defaultSocialMetadata(name: string) {
  const preview = { title: `${name}: ${TAGLINE}`, description: DESCRIPTION };
  return {
    openGraph: { type: "website" as const, siteName: name, locale: "en_US", images: OG_IMAGES, ...preview },
    twitter: { card: "summary_large_image" as const, images: TWITTER_IMAGES, ...preview },
  };
}
