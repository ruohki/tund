"use client";

import { useActionState } from "react";
import { Pin } from "lucide-react";
import { pinTunnelAction } from "@/app/actions/tunnels";
import { SubmitButton } from "@/components/client-ui";

/** Pins the tunnel's hostname so the account keeps it. */
export function PinButton({ id }: { id: string }) {
  const [state, action] = useActionState(pinTunnelAction, null);
  return (
    <form action={action} className="flex items-center justify-end gap-2">
      <input type="hidden" name="id" value={id} />
      {state?.error ? <span className="max-w-72 text-right text-[12px] text-danger">{state.error}</span> : null}
      <SubmitButton variant="ghost" size="sm" pendingText="Pinning…">
        <Pin size={13} /> Pin
      </SubmitButton>
    </form>
  );
}
