"use client";

import { useActionState, useState } from "react";
import { createTeamAction } from "@/app/actions/teams";
import { cn, Field, FormMessage, inputClass } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

function slugify(s: string) {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);
}

export function CreateTeamForm() {
  const [state, action] = useActionState(createTeamAction, null);
  const [slug, setSlug] = useState("");
  const [touched, setTouched] = useState(false);
  return (
    <form action={action} className="flex flex-col gap-3.5">
      <div className="grid gap-3.5 sm:grid-cols-2">
        <Field label="Name" htmlFor="team-name">
          <input
            id="team-name"
            name="name"
            required
            maxLength={80}
            placeholder="Acme platform"
            className={inputClass}
            onChange={(e) => {
              if (!touched) setSlug(slugify(e.target.value));
            }}
          />
        </Field>
        <Field
          label="Slug"
          htmlFor="team-slug"
          hint={
            <>
              Used on the command line: <code className="font-mono">--oidc {slug || "<team>"}/&lt;provider&gt;</code>
            </>
          }
        >
          <input
            id="team-slug"
            name="slug"
            required
            value={slug}
            onChange={(e) => {
              setTouched(true);
              setSlug(e.target.value.toLowerCase());
            }}
            placeholder="acme"
            spellCheck={false}
            className={cn(inputClass, "font-mono text-[12.5px]")}
          />
        </Field>
      </div>
      <FormMessage state={state} />
      <div>
        <SubmitButton pendingText="Creating…">Create team</SubmitButton>
      </div>
    </form>
  );
}
