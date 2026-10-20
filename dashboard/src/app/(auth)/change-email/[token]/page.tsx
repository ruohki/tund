import type { Metadata } from "next";
import { requireUser } from "@/lib/auth";
import { checkEmailToken } from "@/lib/email-tokens";
import { ChangeEmailForm } from "./form";

export const metadata: Metadata = { title: "Confirm new email address" };

// Like /verify-email: switching is a button press (POST), and only in the
// account that asked, so a mistyped address's owner can't take the account.
export default async function ChangeEmailPage({ params }: PageProps<"/change-email/[token]">) {
  const user = await requireUser();
  const { token } = await params;
  const check = await checkEmailToken(token, "email_change");
  if (!check.ok || check.userId !== user.id) {
    return (
      <>
        <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">
          {!check.ok && check.reason === "used"
            ? "Already changed"
            : !check.ok && check.reason === "expired"
              ? "This link has expired"
              : "This link isn't for this account"}
        </h1>
        <p className="mt-2 text-[14px] text-ink-2">
          {!check.ok && check.reason === "used"
            ? "The address was changed with this link already."
            : !check.ok && check.reason === "expired"
              ? "Ask for a new link under Settings → Profile."
              : `You're signed in as ${user.email}. Sign in to the account that asked for the change, then open the link again.`}
        </p>
      </>
    );
  }
  return (
    <>
      <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">Confirm your new email address</h1>
      <p className="mt-1 mb-6 text-[14px] text-ink-2">
        Change the address of your account from <span className="font-medium text-ink">{user.email}</span> to{" "}
        <span className="font-medium text-ink">{check.email}</span>. You&apos;ll sign in with the new address from now on.
      </p>
      <ChangeEmailForm token={token} />
    </>
  );
}
