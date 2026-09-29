"use client";

import { useActionState } from "react";
import { resetPasswordAction } from "@/app/actions/password";
import { Field, FormMessage, Input } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

export function ResetForm({ token }: { token: string }) {
  const [state, action] = useActionState(resetPasswordAction, null);
  return (
    <form action={action} className="flex flex-col gap-4">
      <input type="hidden" name="token" value={token} />
      <Field label="New password" htmlFor="password" hint="At least 8 characters.">
        <Input id="password" name="password" type="password" autoComplete="new-password" required minLength={8} autoFocus />
      </Field>
      <Field label="Repeat it" htmlFor="confirm">
        <Input id="confirm" name="confirm" type="password" autoComplete="new-password" required minLength={8} />
      </Field>
      <FormMessage state={state} />
      <SubmitButton className="w-full" pendingText="Saving…">
        Save new password
      </SubmitButton>
    </form>
  );
}
