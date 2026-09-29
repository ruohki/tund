import { OG_ALT, OG_SIZE, renderOgImage } from "@/lib/og";

// Rendered per request: it shows the instance name and hosts from the runtime configuration.
export const dynamic = "force-dynamic";
export const alt = OG_ALT;
export const size = OG_SIZE;
export const contentType = "image/png";

export default async function OpenGraphImage() {
  return renderOgImage();
}
