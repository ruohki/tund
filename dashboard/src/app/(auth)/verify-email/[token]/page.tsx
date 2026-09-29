import type { Metadata } from "next";
import { checkEmailToken } from "@/lib/email-tokens";
import { VerifyForm } from "./form";

export const metadata: Metadata = { title: "Confirm email address" };

// Confirming is a button press (POST), not the GET of the link itself, so link
// scanners and cross-site requests can't confirm an address.
export default async function VerifyEmailPage({ params }: PageProps<"/verify-email/[token]">) {
  const { token } = await params;
  const check = await checkEmailToken(token, "verify");
  if (!check.ok) {
    return (
      <>
        <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">
          {check.reason === "used" ? "Already confirmed" : check.reason === "expired" ? "This link has expired" : "This link isn't valid"}
        </h1>
        <p className="mt-2 text-[14px] text-ink-2">
          {check.reason === "used"
            ? "This address was confirmed with this link already."
            : "Sign in and use “Send a new link” in the banner at the top of the dashboard."}
        </p>
      </>
    );
  }
  return (
    <>
      <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">Confirm your email address</h1>
      <p className="mt-1 mb-6 text-[14px] text-ink-2">
        Confirm <span className="font-medium text-ink">{check.email}</span> to finish setting up your account.
      </p>
      <VerifyForm token={token} />
    </>
  );
}
