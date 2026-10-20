"use client";

import { useActionState } from "react";
import { confirmEmailChangeAction } from "@/app/actions/account";
import { FormMessage } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

export function ChangeEmailForm({ token }: { token: string }) {
  const [state, action] = useActionState(confirmEmailChangeAction, null);
  return (
    <form action={action} className="flex flex-col gap-4">
      <input type="hidden" name="token" value={token} />
      <FormMessage state={state} />
      <SubmitButton className="w-full" pendingText="Changing…">
        Use the new address
      </SubmitButton>
    </form>
  );
}
