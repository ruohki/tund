import type { MetadataRoute } from "next";
import { config } from "@/lib/config";
import { getSettings } from "@/lib/settings";

export const dynamic = "force-dynamic";

export default async function sitemap(): Promise<MetadataRoute.Sitemap> {
  const open = await getSettings().then(
    (s) => s.signup_mode === "open",
    () => false,
  );
  if (!open) return [];
  const base = config().dashboardUrl;
  const page = (path: string, priority: number) => ({
    url: `${base}${path}`,
    changeFrequency: "monthly" as const,
    priority,
  });
  return [page("/", 1), page("/signup", 0.6), page("/login", 0.4), page("/terms", 0.2), page("/acceptable-use", 0.2)];
}
