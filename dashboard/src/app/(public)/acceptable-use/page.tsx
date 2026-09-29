import type { Metadata } from "next";
import { publicPageMetadata, siteInfo } from "@/lib/seo";
import { LegalPage } from "../legal-page";

export async function generateMetadata(): Promise<Metadata> {
  const { name } = await siteInfo();
  return publicPageMetadata("/acceptable-use", "Acceptable Use Policy", `What ${name} tunnels may not be used for.`);
}

export default function AcceptableUsePage() {
  return <LegalPage page="acceptable-use" />;
}
