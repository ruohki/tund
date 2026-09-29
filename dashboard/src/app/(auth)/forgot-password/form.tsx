"use client";

import { useActionState } from "react";
import { forgotPasswordAction } from "@/app/actions/password";
import { Field, FormMessage, Input } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

export function ForgotForm() {
  const [state, action] = useActionState(forgotPasswordAction, null);
  if (state?.ok) return <FormMessage state={state} />;
  return (
    <form action={action} className="flex flex-col gap-4">
      <Field label="Email" htmlFor="email">
        <Input id="email" name="email" type="email" autoComplete="email" required autoFocus />
      </Field>
      <FormMessage state={state} />
      <SubmitButton className="w-full" pendingText="Sending…">
        Send reset link
      </SubmitButton>
    </form>
  );
}
