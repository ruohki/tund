"use client";

import { useActionState } from "react";
import { MailWarning } from "lucide-react";
import { resendVerificationAction } from "@/app/actions/password";
import { SubmitButton } from "./client-ui";

/** Shown until an account confirms its address, while the server requires it. */
export function VerifyBanner({ email }: { email: string }) {
  const [state, action] = useActionState(resendVerificationAction, null);
  return (
    <div className="mb-6 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-sodium/60 bg-sodium-wash px-4 py-3">
      <p className="flex min-w-0 items-start gap-2.5 text-[13.5px] text-ink">
        <MailWarning size={16} className="mt-0.5 shrink-0 text-sodium-ink" />
        <span>
          <span className="font-semibold">Confirm your email address.</span> We sent a link to {email}. Until you open it,
          you can&apos;t log in a terminal or create auth tokens.
          {state?.ok ? <span className="ml-1 text-ok">{state.ok}</span> : null}
          {state?.error ? <span className="ml-1 text-danger">{state.error}</span> : null}
        </span>
      </p>
      <form action={action}>
        <SubmitButton variant="secondary" size="sm" pendingText="Sending…">
          Send a new link
        </SubmitButton>
      </form>
    </div>
  );
}
