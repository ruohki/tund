"use client";

import { useActionState, useRef } from "react";
import { createUserAction } from "@/app/actions/admin";
import { Field, FormMessage, Input } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

export function CreateUserForm() {
  const ref = useRef<HTMLFormElement>(null);
  const [state, action] = useActionState(async (s: Parameters<typeof createUserAction>[0], fd: FormData) => {
    const res = await createUserAction(s, fd);
    if (res?.ok) ref.current?.reset();
    return res;
  }, null);
  return (
    <form ref={ref} action={action} className="flex flex-col gap-3.5">
      <div className="grid gap-3.5 md:grid-cols-3">
        <Field label="Email" htmlFor="new-email">
          <Input id="new-email" name="email" type="email" required autoComplete="off" />
        </Field>
        <Field label="Name" htmlFor="new-name">
          <Input id="new-name" name="name" autoComplete="off" />
        </Field>
        <Field label="Initial password" htmlFor="new-password" hint="At least 8 characters.">
          <Input id="new-password" name="password" type="password" required minLength={8} autoComplete="new-password" />
        </Field>
      </div>
      <label className="flex items-center gap-2 text-[13px] text-ink">
        <input type="checkbox" name="admin" className="accent-[var(--ink)]" />
        Administrator (can manage users)
      </label>
      <FormMessage state={state} />
      <div>
        <SubmitButton pendingText="Creating…">Create user</SubmitButton>
      </div>
    </form>
  );
}
