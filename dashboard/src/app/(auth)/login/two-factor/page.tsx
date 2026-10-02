import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { db } from "@/lib/db";
import { readFlow } from "@/lib/two-factor";
import { cancelSecondFactorAction } from "@/app/actions/two-factor";
import { PasskeyButton } from "@/components/passkey-button";
import { TwoFactorForm } from "./two-factor-form";

export const metadata: Metadata = { title: "Two-factor authentication", robots: { index: false } };

/** Second step of signing in when two-factor authentication is on. */
export default async function TwoFactorPage() {
  const flow = await readFlow("second_factor");
  if (!flow?.userId) redirect("/login?two_factor=expired");
  const [user] = await db()`
    select email, (select count(*)::int from user_passkeys p where p.user_id = u.id) as passkeys
    from users u where u.id = ${flow.userId}`;
  if (!user) redirect("/login");
  return (
    <>
      <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">Two-factor authentication</h1>
      <p className="mt-1 mb-6 text-[14px] text-ink-2">
        Enter the code from your authenticator app to finish signing in as <span className="font-medium text-ink">{user.email}</span>.
      </p>
      <TwoFactorForm />
      {Number(user.passkeys) > 0 ? <PasskeyButton mode="second_factor" label="Use a passkey instead" className="mt-3" /> : null}
      <form action={cancelSecondFactorAction} className="mt-6 text-[13px] text-ink-2">
        Lost your phone? Use one of your recovery codes instead of the 6-digit code, or{" "}
        <button type="submit" className="font-medium text-ink underline underline-offset-4">
          sign in as someone else
        </button>
        .
      </form>
    </>
  );
}
