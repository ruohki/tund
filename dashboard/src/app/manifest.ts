import type { MetadataRoute } from "next";
import { DESCRIPTION, siteInfo } from "@/lib/seo";

export const dynamic = "force-dynamic";

export default async function manifest(): Promise<MetadataRoute.Manifest> {
  const { name } = await siteInfo();
  return {
    name,
    short_name: name,
    description: DESCRIPTION,
    start_url: "/",
    display: "standalone",
    background_color: "#edeff2",
    theme_color: "#14171c",
    icons: [
      { src: "/icon.svg", sizes: "any", type: "image/svg+xml" },
      { src: "/apple-icon", sizes: "180x180", type: "image/png" },
    ],
  };
}
