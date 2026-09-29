import type { Metadata } from "next";
import Link from "next/link";
import { checkEmailToken } from "@/lib/email-tokens";
import { ResetForm } from "./form";

export const metadata: Metadata = { title: "Choose a new password" };

export default async function ResetPasswordPage({ params }: PageProps<"/reset-password/[token]">) {
  const { token } = await params;
  const check = await checkEmailToken(token, "reset");
  if (!check.ok) {
    return (
      <>
        <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">
          {check.reason === "expired" ? "This link has expired" : check.reason === "used" ? "This link was already used" : "This link isn't valid"}
        </h1>
        <p className="mt-2 text-[14px] text-ink-2">Reset links work once, for 1 hour. Ask for a new one.</p>
        <Link href="/forgot-password" className="mt-5 inline-block font-medium text-ink underline underline-offset-4">
          Send a new link
        </Link>
      </>
    );
  }
  return (
    <>
      <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">Choose a new password</h1>
      <p className="mt-1 mb-6 text-[14px] text-ink-2">
        For <span className="font-medium text-ink">{check.email}</span>. Saving it signs you out on every device.
      </p>
      <ResetForm token={token} />
    </>
  );
}
