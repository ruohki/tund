import type { Metadata } from "next";
import { publicPageMetadata, siteInfo } from "@/lib/seo";
import { LegalPage } from "../legal-page";

export async function generateMetadata(): Promise<Metadata> {
  const { name } = await siteInfo();
  return publicPageMetadata("/terms", "Terms of Service", `The terms for using ${name}.`);
}

export default function TermsPage() {
  return <LegalPage page="terms" />;
}
