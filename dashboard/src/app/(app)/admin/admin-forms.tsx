"use client";

import { useActionState } from "react";
import {
  adminStopTunnelAction,
  sendResetEmailAction,
  setUserFlagAction,
} from "@/app/actions/admin";
import { cn, FormMessage, inputClass } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

type Flag = "disabled" | "trusted" | "admin" | "verified" | "flagged";

/** A labeled switch that flips one account flag on submit. */
export function FlagSwitch({
  id,
  flag,
  on,
  label,
  help,
  disabled,
}: {
  id: string;
  flag: Flag;
  on: boolean;
  label: string;
  help?: string;
  disabled?: boolean;
}) {
  const [state, action, pending] = useActionState(setUserFlagAction, null);
  const checked = pending ? !on : on;
  return (
    <form action={action} className="flex items-start justify-between gap-4 py-2.5">
      <input type="hidden" name="id" value={id} />
      <input type="hidden" name="flag" value={flag} />
      <input type="hidden" name="value" value={on ? "off" : "on"} />
      <span className="min-w-0">
        <span className="block text-[13.5px] font-medium text-ink">{label}</span>
        {help ? <span className="block text-[12.5px] text-muted">{help}</span> : null}
        {state?.error ? <span className="block text-[12.5px] text-danger">{state.error}</span> : null}
      </span>
      <button
        type="submit"
        role="switch"
        aria-checked={checked}
        aria-label={label}
        disabled={disabled || pending}
        className={cn(
          "relative mt-0.5 inline-flex h-5 w-9 shrink-0 items-center rounded-full border transition-colors",
          checked ? "border-ink bg-ink" : "border-line-strong bg-surface-3",
          disabled && "opacity-50",
        )}
      >
        <span
          aria-hidden
          className={cn(
            "absolute h-3.5 w-3.5 rounded-full transition-transform",
            checked ? "translate-x-[18px] bg-btn-ink" : "translate-x-[2px] bg-surface",
          )}
        />
      </button>
    </form>
  );
}

export function ResetPasswordButton({ id, disabled }: { id: string; disabled?: boolean }) {
  const [state, action] = useActionState(sendResetEmailAction, null);
  return (
    <form action={action} className="flex flex-col items-start gap-2">
      <input type="hidden" name="id" value={id} />
      <SubmitButton variant="secondary" size="sm" pendingText="Sending…" disabled={disabled}>
        Send password reset email
      </SubmitButton>
      <FormMessage state={state} />
    </form>
  );
}

export function StopTunnelForm({ id }: { id: string }) {
  const [state, action] = useActionState(adminStopTunnelAction, null);
  return (
    <form action={action} className="flex flex-wrap items-center justify-end gap-2">
      <input type="hidden" name="tunnel_id" value={id} />
      <input
        name="reason"
        aria-label="Reason shown to the client"
        placeholder="Reason (shown to the client)"
        maxLength={200}
        className={cn(inputClass.replace("w-full", ""), "h-7 w-56 text-[12.5px]")}
      />
      <SubmitButton variant="danger" size="sm" pendingText="Stopping…">
        Stop
      </SubmitButton>
      {state?.error ? <span className="w-full text-right text-[12px] text-danger">{state.error}</span> : null}
    </form>
  );
}
