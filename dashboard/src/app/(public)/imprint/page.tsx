import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { hasImprint } from "@/lib/legal";
import { publicPageMetadata, siteInfo } from "@/lib/seo";
import { LegalPage } from "../legal-page";

export async function generateMetadata(): Promise<Metadata> {
  const { name } = await siteInfo();
  return publicPageMetadata("/imprint", "Imprint", `Who runs ${name}.`);
}

/** Only exists once the operator wrote one (Admin → Settings → Legal pages). */
export default async function ImprintPage() {
  if (!(await hasImprint())) notFound();
  return <LegalPage page="imprint" />;
}
