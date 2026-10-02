"use client";

import { useActionState } from "react";
import { Field, FormMessage, Input } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";
import { verifySecondFactorAction } from "@/app/actions/two-factor";

export function TwoFactorForm() {
  const [state, action] = useActionState(verifySecondFactorAction, null);
  return (
    <form action={action} className="flex flex-col gap-4">
      <Field label="Code" htmlFor="code" hint="6 digits from your authenticator app, or a recovery code like abcde-12345.">
        <Input
          id="code"
          name="code"
          autoComplete="one-time-code"
          inputMode="text"
          autoCapitalize="none"
          spellCheck={false}
          required
          autoFocus
          maxLength={20}
          className="font-mono tracking-[0.12em]"
        />
      </Field>
      <FormMessage state={state} />
      <SubmitButton className="mt-1 w-full" pendingText="Checking…">
        Verify
      </SubmitButton>
    </form>
  );
}
