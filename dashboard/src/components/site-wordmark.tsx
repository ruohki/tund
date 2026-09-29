import "server-only";
import { siteInfo } from "@/lib/seo";
import { Wordmark } from "./brand";

/** The wordmark with the instance name from the settings. */
export async function SiteWordmark() {
  return <Wordmark name={(await siteInfo()).name} />;
}
