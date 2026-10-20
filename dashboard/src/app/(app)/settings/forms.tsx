"use client";

import { useActionState, useRef } from "react";
import { changeEmailAction, changePasswordAction, deleteOwnAccountAction, updateProfileAction } from "@/app/actions/account";
import { Field, FormMessage, Input } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

export function ProfileForm({ name }: { name: string }) {
  const [state, action] = useActionState(updateProfileAction, null);
  return (
    <form action={action} className="flex flex-col gap-3.5">
      <Field label="Name" htmlFor="name">
        <Input id="name" name="name" defaultValue={name} autoComplete="name" />
      </Field>
      <FormMessage state={state} />
      <div>
        <SubmitButton pendingText="Saving…">Save name</SubmitButton>
      </div>
    </form>
  );
}

/** A new address applies once it opens the emailed link (right away when the server can't send email). */
export function EmailForm({ email, hasPassword, mailOn }: { email: string; hasPassword: boolean; mailOn: boolean }) {
  const ref = useRef<HTMLFormElement>(null);
  const [state, action] = useActionState(async (s: Parameters<typeof changeEmailAction>[0], fd: FormData) => {
    const res = await changeEmailAction(s, fd);
    if (res?.ok) ref.current?.reset();
    return res;
  }, null);
  return (
    <form ref={ref} action={action} className="flex flex-col gap-3.5">
      <Field
        label="Email"
        htmlFor="email"
        hint={`You sign in with ${email}.${mailOn ? " A new address changes once you open the link we send to it." : ""}`}
      >
        <Input id="email" name="email" type="email" placeholder="new@example.com" required autoComplete="email" />
      </Field>
      {hasPassword ? (
        <Field label="Current password" htmlFor="email-password">
          <Input id="email-password" name="password" type="password" required autoComplete="current-password" />
        </Field>
      ) : null}
      <FormMessage state={state} />
      <div>
        <SubmitButton variant="secondary" pendingText={mailOn ? "Sending…" : "Saving…"}>
          {mailOn ? "Send confirmation link" : "Change email"}
        </SubmitButton>
      </div>
    </form>
  );
}

/** Settings → Delete account; the consequences are listed by the page. */
export function DeleteAccountForm({ email, hasPassword }: { email: string; hasPassword: boolean }) {
  const [state, action] = useActionState(deleteOwnAccountAction, null);
  return (
    <form action={action} className="flex flex-col gap-3.5">
      <Field label={`Type ${email} to confirm`} htmlFor="delete-confirm">
        <Input id="delete-confirm" name="confirm" type="text" required autoComplete="off" spellCheck={false} />
      </Field>
      {hasPassword ? (
        <Field label="Password" htmlFor="delete-password">
          <Input id="delete-password" name="password" type="password" required autoComplete="current-password" />
        </Field>
      ) : null}
      <FormMessage state={state} />
      <div>
        <SubmitButton variant="danger" pendingText="Deleting…">
          Delete my account
        </SubmitButton>
      </div>
    </form>
  );
}

/** Change the password, or set a first one for accounts created with Google or GitHub. */
export function PasswordForm({ hasPassword = true }: { hasPassword?: boolean }) {
  const ref = useRef<HTMLFormElement>(null);
  const [state, action] = useActionState(async (s: Parameters<typeof changePasswordAction>[0], fd: FormData) => {
    const res = await changePasswordAction(s, fd);
    if (res?.ok) ref.current?.reset();
    return res;
  }, null);
  return (
    <form ref={ref} action={action} className="flex flex-col gap-3.5">
      {hasPassword ? (
        <Field label="Current password" htmlFor="current">
          <Input id="current" name="current" type="password" required autoComplete="current-password" />
        </Field>
      ) : null}
      <Field label="New password" htmlFor="next" hint="At least 8 characters.">
        <Input id="next" name="next" type="password" required minLength={8} autoComplete="new-password" />
      </Field>
      <FormMessage state={state} />
      <div>
        <SubmitButton pendingText="Saving…">{hasPassword ? "Change password" : "Set password"}</SubmitButton>
      </div>
    </form>
  );
}
