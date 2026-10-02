"use client";

import { useActionState } from "react";
import { setUserFeatureAction } from "@/app/actions/admin";
import { FormMessage, Select } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

const modeOf = (v: boolean | null) => (v === null ? "inherit" : v ? "on" : "off");

/** Per-account feature override (custom domains, TCP and TLS tunnels): the instance setting, on or off. */
export function FeatureForm({
  id,
  feature,
  label,
  value,
  inheritLabel,
}: {
  id: string;
  feature: "custom_domains" | "passthrough";
  label: string;
  value: boolean | null;
  inheritLabel: string;
}) {
  const [state, action] = useActionState(setUserFeatureAction, null);
  return (
    <form action={action} className="flex flex-col gap-2">
      <input type="hidden" name="id" value={id} />
      <input type="hidden" name="feature" value={feature} />
      <div className="flex flex-wrap items-center gap-2">
        {/* React resets the form after the action and Radix then restores the
            select's initial value; keyed on the saved value, it remounts with
            the new one once the page refreshes. */}
        <Select
          key={modeOf(value)}
          name="mode"
          defaultValue={modeOf(value)}
          aria-label={`${label} for this account`}
          className="sm:w-64"
          options={[
            { value: "inherit", label: `Default (${inheritLabel})` },
            { value: "on", label: "On" },
            { value: "off", label: "Off" },
          ]}
        />
        <SubmitButton size="sm" pendingText="Saving…">
          Save
        </SubmitButton>
      </div>
      <FormMessage state={state} />
    </form>
  );
}
