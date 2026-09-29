import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { getCurrentUser, usersExist } from "@/lib/auth";
import { getSettings } from "@/lib/settings";
import { DESCRIPTION, publicPageMetadata, siteInfo, TAGLINE } from "@/lib/seo";
import { AppShell } from "@/components/app-shell";
import { Overview } from "@/components/overview";
import { Landing } from "@/components/landing/landing";

export async function generateMetadata(): Promise<Metadata> {
  // title.template from the root layout does not apply to its own segment.
  const { name } = await siteInfo();
  if (await getCurrentUser()) return { title: { absolute: `Overview · ${name}` } };
  const title = `${name}: ${TAGLINE}`;
  const meta = await publicPageMetadata("/", title, DESCRIPTION);
  return { ...meta, title: { absolute: title } };
}

// Signed in: the overview. Signed out: the public landing page when anyone may
// sign up (hosted service), otherwise straight to the login form.
export default async function Home() {
  const user = await getCurrentUser();
  if (user) {
    return (
      <AppShell user={user}>
        <Overview user={user} />
      </AppShell>
    );
  }
  if (!(await usersExist())) redirect("/setup");
  // The public landing page is for instances anyone can sign up to.
  if ((await getSettings()).signup_mode !== "open") redirect("/login");
  return <Landing />;
}
