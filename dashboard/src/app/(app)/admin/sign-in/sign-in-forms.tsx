"use client";

import { useActionState } from "react";
import { saveOAuthProviderAction } from "@/app/actions/admin";
import { Field, FormMessage, inputClass } from "@/components/ui";
import { Command, SubmitButton } from "@/components/client-ui";
import { PROVIDER_LABEL, type OAuthProviderId } from "@/lib/oauth-shared";

type Current = { enabled: boolean; clientId: string; hasSecret: boolean; secretBroken: boolean };

export function OAuthProviderForm({ provider, callbackUrl, current }: { provider: OAuthProviderId; callbackUrl: string; current: Current }) {
  const [state, action] = useActionState(saveOAuthProviderAction, null);
  const label = PROVIDER_LABEL[provider];
  return (
    <form action={action} className="flex flex-col gap-3.5">
      <input type="hidden" name="provider" value={provider} />
      <Field label={provider === "google" ? "Authorized redirect URI" : "Authorization callback URL"} htmlFor={`${provider}-callback`}>
        <Command prompt="">{callbackUrl}</Command>
      </Field>
      <Field label="Client ID" htmlFor={`${provider}-id`}>
        <input
          id={`${provider}-id`}
          name="client_id"
          defaultValue={current.clientId}
          autoComplete="off"
          spellCheck={false}
          className={`${inputClass} font-mono text-[13px]`}
        />
      </Field>
      <Field
        label="Client secret"
        htmlFor={`${provider}-secret`}
        hint={
          current.secretBroken
            ? "The stored secret can't be decrypted anymore (TUND_INTERNAL_SECRET changed). Enter it again."
            : current.hasSecret
              ? "Stored encrypted and never shown. Leave empty to keep it."
              : "Stored encrypted and never shown again."
        }
      >
        <input
          id={`${provider}-secret`}
          name="client_secret"
          type="password"
          autoComplete="new-password"
          placeholder={current.hasSecret ? "unchanged" : ""}
          className={inputClass}
        />
      </Field>
      <label className="flex items-center gap-2 text-[13.5px] text-ink">
        <input type="checkbox" name="enabled" defaultChecked={current.enabled} className="h-4 w-4 accent-[var(--ink)]" />
        Show “Continue with {label}” on the sign-in and sign-up pages
      </label>
      <FormMessage state={state} />
      <div>
        <SubmitButton pendingText="Saving…">Save {label}</SubmitButton>
      </div>
    </form>
  );
}
