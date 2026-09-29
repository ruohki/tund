"use client";

import { useActionState } from "react";
import { setUserCustomDomainsAction } from "@/app/actions/admin";
import { FormMessage, Select } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

const modeOf = (v: boolean | null) => (v === null ? "inherit" : v ? "on" : "off");

/** Per-account custom domains override: the instance setting, on or off. */
export function CustomDomainsForm({ id, value, inheritLabel }: { id: string; value: boolean | null; inheritLabel: string }) {
  const [state, action] = useActionState(setUserCustomDomainsAction, null);
  return (
    <form action={action} className="flex flex-col gap-2">
      <input type="hidden" name="id" value={id} />
      <div className="flex flex-wrap items-center gap-2">
        {/* React resets the form after the action and Radix then restores the
            select's initial value; keyed on the saved value, it remounts with
            the new one once the page refreshes. */}
        <Select
          key={modeOf(value)}
          name="custom_domains"
          defaultValue={modeOf(value)}
          aria-label="Custom domains for this account"
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
