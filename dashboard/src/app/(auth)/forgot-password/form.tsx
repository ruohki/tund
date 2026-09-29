"use client";

import { useActionState } from "react";
import { forgotPasswordAction } from "@/app/actions/password";
import { Field, FormMessage, Input } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";
import { Turnstile } from "@/components/turnstile";

export function ForgotForm({ turnstileSiteKey }: { turnstileSiteKey: string | null }) {
  const [state, action] = useActionState(forgotPasswordAction, null);
  if (state?.ok) return <FormMessage state={state} />;
  return (
    <form action={action} className="flex flex-col gap-4">
      <Field label="Email" htmlFor="email">
        <Input id="email" name="email" type="email" autoComplete="email" required autoFocus />
      </Field>
      {turnstileSiteKey ? <Turnstile siteKey={turnstileSiteKey} reset={state} /> : null}
      <FormMessage state={state} />
      <SubmitButton className="w-full" pendingText="Sending…">
        Send reset link
      </SubmitButton>
    </form>
  );
}
