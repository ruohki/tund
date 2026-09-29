import type { MetadataRoute } from "next";
import { config } from "@/lib/config";
import { getSettings } from "@/lib/settings";

export const dynamic = "force-dynamic";

// Private app pages are never crawled. Only an open instance (public sign-up)
// exposes its landing and sign-in pages to search engines.
const PRIVATE = [
  "/api/",
  "/admin",
  "/inspect",
  "/tunnels",
  "/domains",
  "/access",
  "/teams",
  "/authtokens",
  "/settings",
  "/get-started",
  "/device",
  "/invite/",
  "/verify-email/",
  "/reset-password/",
  "/forgot-password",
  "/setup",
  "/_tund/",
];

export default async function robots(): Promise<MetadataRoute.Robots> {
  const open = await getSettings().then(
    (s) => s.signup_mode === "open",
    () => false,
  );
  const base = config().dashboardUrl;
  if (!open) return { rules: { userAgent: "*", disallow: "/" } };
  return {
    rules: { userAgent: "*", allow: ["/$", "/login", "/signup", "/report", "/terms", "/acceptable-use"], disallow: PRIVATE },
    sitemap: `${base}/sitemap.xml`,
    host: base,
  };
}
