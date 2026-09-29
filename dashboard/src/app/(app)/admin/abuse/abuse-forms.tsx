"use client";

import { useActionState } from "react";
import { blockHostAction, reviewDomainAction, setReportStatusAction } from "@/app/actions/abuse";
import { setUserFlagAction } from "@/app/actions/admin";
import { cn, FormMessage, inputClass } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

const small = cn(inputClass.replace("w-full", ""), "h-8 min-w-0 flex-1 text-[13px]");

/** Resolve / dismiss with a note, or reopen. */
export function ReportStatusForm({ id, status }: { id: string; status: string }) {
  const [state, action] = useActionState(setReportStatusAction, null);
  if (status !== "open") {
    return (
      <form action={action} className="flex flex-col items-start gap-2">
        <input type="hidden" name="id" value={id} />
        <input type="hidden" name="status" value="open" />
        <SubmitButton variant="secondary" size="sm" pendingText="Reopening…">
          Reopen
        </SubmitButton>
        <FormMessage state={state} />
      </form>
    );
  }
  return (
    <form action={action} className="flex flex-col gap-2">
      <input type="hidden" name="id" value={id} />
      <label htmlFor="note" className="text-[12.5px] text-muted">
        Note (for the audit log and other admins)
      </label>
      <textarea id="note" name="note" rows={2} maxLength={2000} className={cn(inputClass, "h-auto py-2")} />
      <div className="flex flex-wrap gap-2">
        <SubmitButton size="sm" name="status" value="resolved" pendingText="Saving…">
          Resolve
        </SubmitButton>
        <SubmitButton variant="secondary" size="sm" name="status" value="dismissed" pendingText="Saving…">
          Dismiss
        </SubmitButton>
      </div>
      <FormMessage state={state} />
    </form>
  );
}

export function BlockHostForm({ hostname, reportId }: { hostname?: string; reportId?: string }) {
  const [state, action] = useActionState(blockHostAction, null);
  return (
    <form action={action} className="flex flex-col gap-2">
      {reportId ? <input type="hidden" name="report" value={reportId} /> : null}
      <div className="flex flex-wrap gap-2">
        {hostname ? (
          <input type="hidden" name="hostname" value={hostname} />
        ) : (
          <input name="hostname" required placeholder="hostname.example.com" aria-label="Hostname" className={cn(small, "font-mono")} />
        )}
        <input name="reason" maxLength={300} placeholder="Reason (internal)" aria-label="Reason" className={small} />
        <SubmitButton variant="danger" size="sm" pendingText="Blocking…">
          Block hostname
        </SubmitButton>
      </div>
      <FormMessage state={state} />
    </form>
  );
}

export function FlagUserForm({ id }: { id: string }) {
  const [state, action] = useActionState(setUserFlagAction, null);
  return (
    <form action={action} className="flex flex-col gap-2">
      <input type="hidden" name="id" value={id} />
      <input type="hidden" name="flag" value="flagged" />
      <input type="hidden" name="value" value="on" />
      <div className="flex flex-wrap gap-2">
        <input name="reason" maxLength={300} placeholder="Why (shown to admins)" aria-label="Flag reason" className={small} />
        <SubmitButton variant="secondary" size="sm" pendingText="Flagging…">
          Flag account
        </SubmitButton>
      </div>
      <FormMessage state={state} />
    </form>
  );
}

/** Approve or reject a pending custom domain; a rejection needs a reason the owner sees. */
export function ReviewDomainForm({ id, approval }: { id: string; approval: string }) {
  const [state, action] = useActionState(reviewDomainAction, null);
  return (
    <form action={action} className="flex flex-col gap-2">
      <input type="hidden" name="id" value={id} />
      <div className="flex flex-wrap gap-2">
        <input name="reason" maxLength={300} placeholder="Reason (shown to the owner)" aria-label="Reason" className={small} />
        {approval !== "approved" ? (
          <SubmitButton size="sm" name="decision" value="approved" pendingText="Saving…">
            Approve
          </SubmitButton>
        ) : null}
        {approval !== "rejected" ? (
          <SubmitButton variant="danger" size="sm" name="decision" value="rejected" pendingText="Saving…">
            Reject
          </SubmitButton>
        ) : null}
      </div>
      <FormMessage state={state} />
    </form>
  );
}
