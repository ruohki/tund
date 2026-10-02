"use client";

import { useActionState } from "react";
import { setTeamPlanAction } from "@/app/actions/billing";
import { Field, FormMessage, inputClass, Select, cn } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

/** Billing exemption: a plan without a subscription, and a seat count override. */
export function TeamPlanForm({ teamId, planGranted, seatsOverride }: { teamId: string; planGranted: string | null; seatsOverride: number | null }) {
  const [state, action] = useActionState(setTeamPlanAction, null);
  return (
    <form action={action} className="flex flex-col gap-3.5">
      <input type="hidden" name="team_id" value={teamId} />
      <Field
        label="Plan without billing"
        htmlFor="plan-granted"
        hint="The team gets this plan without paying. Team: Pro for its owners. Team Pro: Pro for every member."
      >
        <Select
          id="plan-granted"
          name="plan_granted"
          defaultValue={planGranted ?? ""}
          className="sm:w-72"
          options={[
            { value: "", label: "None (subscription decides)" },
            { value: "team", label: "Team" },
            { value: "team_pro", label: "Team Pro" },
          ]}
        />
      </Field>
      <Field label="Seats" htmlFor="seats-override" hint="Members allowed, replacing the plan's. Empty: the plan decides.">
        <input
          id="seats-override"
          name="seats_override"
          inputMode="numeric"
          defaultValue={seatsOverride ?? ""}
          placeholder="from the plan"
          className={cn(inputClass, "w-36 tabular")}
        />
      </Field>
      <FormMessage state={state} />
      <div>
        <SubmitButton pendingText="Saving…">Save</SubmitButton>
      </div>
    </form>
  );
}
