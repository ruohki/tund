"use client";

import { useActionState } from "react";
import { ConfirmSubmit } from "@/components/client-ui";
import { stopTunnelAction } from "@/app/actions/tunnels";

export function StopButton({ id }: { id: string }) {
  const [state, action] = useActionState(stopTunnelAction, null);
  return (
    <form action={action} className="flex items-center gap-2">
      <input type="hidden" name="id" value={id} />
      {state?.error ? <span className="max-w-64 text-[12px] text-danger">{state.error}</span> : null}
      <ConfirmSubmit confirmText="Stop tunnel?">Stop</ConfirmSubmit>
    </form>
  );
}
