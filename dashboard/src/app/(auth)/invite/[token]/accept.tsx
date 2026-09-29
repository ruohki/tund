"use client";

import { useState, useTransition } from "react";
import { Loader2 } from "lucide-react";
import { acceptInviteAction } from "@/app/actions/teams";
import { buttonClass } from "@/components/ui";

export function AcceptInvite({ token }: { token: string }) {
  const [pending, start] = useTransition();
  const [error, setError] = useState<string | null>(null);
  return (
    <div className="mt-5">
      <button
        type="button"
        disabled={pending}
        onClick={() =>
          start(async () => {
            const res = await acceptInviteAction(token);
            if (res?.error) setError(res.error);
          })
        }
        className={buttonClass("primary")}
      >
        {pending ? <Loader2 size={14} className="animate-spin" /> : null}
        Accept and join
      </button>
      {error ? (
        <p role="alert" className="mt-3 text-[13px] text-danger">
          {error}
        </p>
      ) : null}
    </div>
  );
}
