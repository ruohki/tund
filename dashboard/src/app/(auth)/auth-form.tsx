"use client";

import { useActionState, useEffect, useRef } from "react";
import { Field, FormMessage, Input } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";
import type { FormState } from "@/app/actions/auth";

export function AuthForm({
  action,
  mode,
  next,
}: {
  action: (s: FormState, fd: FormData) => Promise<FormState>;
  mode: "login" | "setup" | "signup";
  next?: string;
}) {
  const [state, formAction] = useActionState(action, null);
  // A redirect to /login keeps the original #fragment in the address bar but the
  // server never sees it; add it back so "/get-started#mcp" survives signing in.
  const nextRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const input = nextRef.current;
    if (input && window.location.hash && !input.value.includes("#")) input.value += window.location.hash;
  }, []);
  return (
    <form action={formAction} className="flex flex-col gap-4">
      {next && next !== "/" ? <input ref={nextRef} type="hidden" name="next" defaultValue={next} /> : null}
      {mode !== "login" ? (
        <Field label="Name" htmlFor="name">
          <Input id="name" name="name" autoComplete="name" placeholder="Ada Lovelace" />
        </Field>
      ) : null}
      <Field label="Email" htmlFor="email">
        <Input id="email" name="email" type="email" autoComplete="email" required autoFocus />
      </Field>
      <Field
        label="Password"
        htmlFor="password"
        hint={mode === "login" ? undefined : "At least 8 characters."}
      >
        <Input
          id="password"
          name="password"
          type="password"
          autoComplete={mode === "login" ? "current-password" : "new-password"}
          required
          minLength={mode === "login" ? undefined : 8}
        />
      </Field>
      <FormMessage state={state} />
      <SubmitButton className="mt-1 w-full" pendingText={mode === "login" ? "Signing in…" : "Creating account…"}>
        {mode === "login" ? "Sign in" : mode === "setup" ? "Create admin account" : "Create account"}
      </SubmitButton>
    </form>
  );
}
