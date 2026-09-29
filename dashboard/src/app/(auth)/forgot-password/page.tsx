import type { Metadata } from "next";
import Link from "next/link";
import { smtpConfigured } from "@/lib/mail";
import { turnstileSiteKey } from "@/lib/turnstile";
import { ForgotForm } from "./form";

export const metadata: Metadata = { title: "Reset password" };

export default async function ForgotPasswordPage() {
  const available = await smtpConfigured();
  return (
    <>
      <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">Reset your password</h1>
      {available ? (
        <>
          <p className="mt-1 mb-6 text-[14px] text-ink-2">We&apos;ll email you a link to choose a new password.</p>
          <ForgotForm turnstileSiteKey={await turnstileSiteKey()} />
        </>
      ) : (
        <p className="mt-2 text-[14px] text-ink-2">
          This server can&apos;t send email, so an administrator has to reset your password.
        </p>
      )}
      <p className="mt-6 text-[13px] text-ink-2">
        <Link href="/login" className="font-medium text-ink underline underline-offset-4">
          Back to sign in
        </Link>
      </p>
    </>
  );
}
