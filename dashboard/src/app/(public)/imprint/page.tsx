import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { hasImprint } from "@/lib/legal";
import { publicPageMetadata, siteInfo } from "@/lib/seo";
import { LegalPage } from "../legal-page";

export async function generateMetadata(): Promise<Metadata> {
  const { name } = await siteInfo();
  return publicPageMetadata("/imprint", "Imprint", `Who runs ${name}.`);
}

/** Only exists once the operator filled in their details (Admin → Settings → Imprint) or wrote their own text. */
export default async function ImprintPage() {
  if (!(await hasImprint())) notFound();
  return <LegalPage page="imprint" />;
}
