import type { User } from "@/lib/auth";
import { Sidebar } from "./sidebar";
import { LiveProvider } from "./live";
import { VerifyBanner } from "./verify-banner";
import { verificationRequired } from "@/lib/mail";

/** Sidebar, live stream and page frame for signed-in pages. */
export async function AppShell({ user, children }: { user: User; children: React.ReactNode }) {
  const mustVerify = !user.emailVerified && (await verificationRequired());
  return (
    <LiveProvider>
      <Sidebar user={{ email: user.email, name: user.name, isAdmin: user.isAdmin }} />
      <div className="lg:pl-60">
        <main className="mx-auto w-full max-w-[1320px] px-4 py-6 sm:px-8 sm:py-8">
          {mustVerify ? <VerifyBanner email={user.email} /> : null}
          {children}
        </main>
      </div>
    </LiveProvider>
  );
}
