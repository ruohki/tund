"use client";

import { useActionState, useRef } from "react";
import { TriangleAlert } from "lucide-react";
import { createTokenAction } from "@/app/actions/tokens";
import { inputClass, cn } from "@/components/ui";
import { Command, SubmitButton } from "@/components/client-ui";

export function CreateTokenForm({ dashboardUrl }: { dashboardUrl: string }) {
  const formRef = useRef<HTMLFormElement>(null);
  const [state, action] = useActionState(async (s: Parameters<typeof createTokenAction>[0], fd: FormData) => {
    const res = await createTokenAction(s, fd);
    formRef.current?.reset();
    return res;
  }, null);

  return (
    <div className="flex flex-col gap-4">
      <form ref={formRef} action={action} className="flex flex-wrap items-center gap-2">
        <input
          name="name"
          aria-label="Token name"
          placeholder="Name, e.g. work laptop"
          maxLength={80}
          className={cn(inputClass, "max-w-sm flex-1")}
        />
        <SubmitButton pendingText="Creating…">Create token</SubmitButton>
      </form>
      {state?.error ? <p className="text-[13px] text-danger">{state.error}</p> : null}
      {state?.token ? (
        <div className="rounded-md border border-sodium/50 bg-sodium-wash p-4">
          <p className="flex items-center gap-2 text-[13px] font-medium text-ink">
            <TriangleAlert size={14} className="text-sodium-ink" />
            Copy “{state.name}” now. It won&apos;t be shown again.
          </p>
          <Command prompt="" className="mt-3 bg-surface">{state.token}</Command>
          <p className="mt-3 text-[13px] text-ink-2">Save it on the machine where you run tund:</p>
          <Command className="mt-1.5 bg-surface">{`tund config add-authtoken ${state.token}`}</Command>
          <p className="mt-3 text-[13px] text-ink-2">Or, in CI, pass it as an environment variable:</p>
          <Command className="mt-1.5 bg-surface">{`TUND_AUTHTOKEN=${state.token} tund http 3000 --log`}</Command>
          <p className="mt-3 text-[12.5px] text-muted">
            Self-hosting with a client downloaded from another server? Point it here with{" "}
            <code className="font-mono text-ink-2">tund config set-server {dashboardUrl}</code>
          </p>
        </div>
      ) : null}
    </div>
  );
}
