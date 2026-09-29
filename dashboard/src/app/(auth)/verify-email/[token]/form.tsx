"use client";

import { useActionState } from "react";
import { verifyEmailAction } from "@/app/actions/password";
import { FormMessage } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

export function VerifyForm({ token }: { token: string }) {
  const [state, action] = useActionState(verifyEmailAction, null);
  return (
    <form action={action} className="flex flex-col gap-4">
      <input type="hidden" name="token" value={token} />
      <FormMessage state={state} />
      <SubmitButton className="w-full" pendingText="Confirming…">
        Confirm email address
      </SubmitButton>
    </form>
  );
}
