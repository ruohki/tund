"use client";

import { useActionState, useRef } from "react";
import { changePasswordAction, updateProfileAction } from "@/app/actions/account";
import { Field, FormMessage, Input } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

export function ProfileForm({ name, email }: { name: string; email: string }) {
  const [state, action] = useActionState(updateProfileAction, null);
  return (
    <form action={action} className="flex flex-col gap-3.5">
      <Field label="Name" htmlFor="name">
        <Input id="name" name="name" defaultValue={name} autoComplete="name" />
      </Field>
      <Field label="Email" htmlFor="email" hint="Used to sign in.">
        <Input id="email" name="email" type="email" defaultValue={email} required autoComplete="email" />
      </Field>
      <FormMessage state={state} />
      <div>
        <SubmitButton pendingText="Saving…">Save profile</SubmitButton>
      </div>
    </form>
  );
}

export function PasswordForm() {
  const ref = useRef<HTMLFormElement>(null);
  const [state, action] = useActionState(async (s: Parameters<typeof changePasswordAction>[0], fd: FormData) => {
    const res = await changePasswordAction(s, fd);
    if (res?.ok) ref.current?.reset();
    return res;
  }, null);
  return (
    <form ref={ref} action={action} className="flex flex-col gap-3.5">
      <Field label="Current password" htmlFor="current">
        <Input id="current" name="current" type="password" required autoComplete="current-password" />
      </Field>
      <Field label="New password" htmlFor="next" hint="At least 8 characters.">
        <Input id="next" name="next" type="password" required minLength={8} autoComplete="new-password" />
      </Field>
      <FormMessage state={state} />
      <div>
        <SubmitButton pendingText="Changing…">Change password</SubmitButton>
      </div>
    </form>
  );
}
