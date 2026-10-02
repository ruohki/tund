import type { User } from "@/lib/auth";
import { Sidebar } from "./sidebar";
import { LiveProvider } from "./live";
import { VerifyBanner } from "./verify-banner";
import { verificationRequired } from "@/lib/mail";
import { siteInfo } from "@/lib/seo";
import { billingEnabled, isPro } from "@/lib/plans";

/** Sidebar, live stream and page frame for signed-in pages. */
export async function AppShell({ user, children }: { user: User; children: React.ReactNode }) {
  const [mustVerify, site, billing, pro] = await Promise.all([
    verificationRequired().then((required) => !user.emailVerified && required),
    siteInfo(),
    billingEnabled(),
    isPro(user.id),
  ]);
  return (
    <LiveProvider>
      <Sidebar user={{ email: user.email, name: user.name, isAdmin: user.isAdmin, pro }} brand={site.name} billing={billing} />
      <div className="lg:pl-60">
        <main className="mx-auto w-full max-w-[1320px] px-4 py-6 sm:px-8 sm:py-8">
          {mustVerify ? <VerifyBanner email={user.email} /> : null}
          {children}
        </main>
      </div>
    </LiveProvider>
  );
}
