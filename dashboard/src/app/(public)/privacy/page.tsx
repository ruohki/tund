import type { Metadata } from "next";
import { publicPageMetadata, siteInfo } from "@/lib/seo";
import { LegalPage } from "../legal-page";

export async function generateMetadata(): Promise<Metadata> {
  const { name } = await siteInfo();
  return publicPageMetadata("/privacy", "Privacy Policy", `How ${name} handles personal data.`);
}

export default function PrivacyPage() {
  return <LegalPage page="privacy" />;
}
