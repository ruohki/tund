"use client";

import * as RS from "@radix-ui/react-select";
import { Check, ChevronDown, ChevronUp } from "lucide-react";
import { useState, type ReactNode } from "react";
import { cn, inputClass } from "./classes";

export type SelectOption = { value: string; label: ReactNode; disabled?: boolean };
export type SelectGroup = { label: string; options: SelectOption[] };

type Props = {
  options: (SelectOption | SelectGroup)[];
  /** Form field name. The chosen value is submitted like a native select's. */
  name?: string;
  value?: string;
  defaultValue?: string;
  onValueChange?: (value: string) => void;
  /** Shown while no option is selected (value ""). */
  placeholder?: ReactNode;
  required?: boolean;
  disabled?: boolean;
  autoFocus?: boolean;
  id?: string;
  "aria-label"?: string;
  /** Classes for the trigger (width, height, font). */
  className?: string;
  /** Monospace items, e.g. hostnames. */
  mono?: boolean;
};

// Radix reserves "" for "nothing selected"; options whose value is "" (like
// "Any method") use this stand-in internally.
const EMPTY = "\u0000empty";
const toRadix = (v: string) => (v === "" ? EMPTY : v);
const fromRadix = (v: string) => (v === EMPTY ? "" : v);

function isGroup(o: SelectOption | SelectGroup): o is SelectGroup {
  return "options" in o;
}

/**
 * Accessible select (listbox) built on Radix: keyboard navigation, typeahead,
 * screen-reader semantics and collision-aware positioning, styled like the
 * app's inputs. Works inside plain HTML forms, including GET forms.
 */
export function Select({
  options,
  name,
  value,
  defaultValue,
  onValueChange,
  placeholder,
  required,
  disabled,
  autoFocus,
  id,
  "aria-label": ariaLabel,
  className,
  mono,
}: Props) {
  const [inner, setInner] = useState(defaultValue ?? "");
  const current = value ?? inner;
  // Empty groups (e.g. no HTTP tunnels yet) would render a bare heading.
  const shown = options.filter((o) => !isGroup(o) || o.options.length > 0);
  const flat = shown.flatMap((o) => (isGroup(o) ? o.options : [o]));
  const hasEmptyOption = flat.some((o) => o.value === "");

  const change = (v: string) => {
    const real = fromRadix(v);
    if (value === undefined) setInner(real);
    onValueChange?.(real);
  };

  const item = (o: SelectOption) => (
    <RS.Item
      key={o.value}
      value={toRadix(o.value)}
      disabled={o.disabled}
      className={cn(
        "relative flex min-h-7.5 cursor-default select-none items-center rounded-[4px] py-1 pl-7 pr-2.5 text-[13px] text-ink outline-none",
        "data-[highlighted]:bg-surface-3 data-[disabled]:pointer-events-none data-[disabled]:text-muted",
        mono && "font-mono text-[12.5px]",
      )}
    >
      <RS.ItemIndicator className="absolute left-2 inline-flex items-center text-ink">
        <Check size={13} strokeWidth={2.5} />
      </RS.ItemIndicator>
      <RS.ItemText>{o.label}</RS.ItemText>
    </RS.Item>
  );

  return (
    <>
      <RS.Root
        value={hasEmptyOption ? toRadix(current) : current}
        onValueChange={change}
        // With an "" option the value travels in our own hidden input (Radix
        // would submit the stand-in); otherwise Radix's native bubble select
        // handles name/required, including browser validation.
        name={hasEmptyOption ? undefined : name}
        required={hasEmptyOption ? undefined : required}
        disabled={disabled}
      >
        <RS.Trigger
          id={id}
          aria-label={ariaLabel}
          autoFocus={autoFocus}
          className={cn(
            inputClass.replace("w-full", ""),
            // Full width unless the caller sets a (non-responsive) width.
            !/(^|\s)w-/.test(className ?? "") && "w-full",
            "inline-flex items-center justify-between gap-2 text-left",
            "data-[placeholder]:text-muted data-[state=open]:border-focus data-[state=open]:ring-2 data-[state=open]:ring-focus/20",
            mono && "font-mono",
            className,
          )}
        >
          <span className="min-w-0 truncate">
            <RS.Value placeholder={placeholder} />
          </span>
          <RS.Icon className="shrink-0 text-muted">
            <ChevronDown size={14} />
          </RS.Icon>
        </RS.Trigger>
        <RS.Portal>
          <RS.Content
            position="popper"
            sideOffset={4}
            collisionPadding={8}
            className={cn(
              "z-50 min-w-[var(--radix-select-trigger-width)] max-w-[min(32rem,calc(100vw-1rem))] overflow-hidden",
              "max-h-[min(22rem,var(--radix-select-content-available-height))]",
              "rounded-md border border-line-strong bg-surface shadow-pop",
            )}
          >
            <RS.ScrollUpButton className="flex h-6 items-center justify-center text-muted">
              <ChevronUp size={14} />
            </RS.ScrollUpButton>
            <RS.Viewport className="p-1">
              {shown.map((o, i) =>
                isGroup(o) ? (
                  <RS.Group key={`g-${o.label}`}>
                    {i > 0 ? <RS.Separator className="mx-1 my-1 h-px bg-line" /> : null}
                    <RS.Label className="px-2.5 pb-1 pt-1.5 text-[11px] font-medium uppercase tracking-wide text-muted">
                      {o.label}
                    </RS.Label>
                    {o.options.map(item)}
                  </RS.Group>
                ) : (
                  item(o)
                ),
              )}
            </RS.Viewport>
            <RS.ScrollDownButton className="flex h-6 items-center justify-center text-muted">
              <ChevronDown size={14} />
            </RS.ScrollDownButton>
          </RS.Content>
        </RS.Portal>
      </RS.Root>
      {hasEmptyOption && name ? <input type="hidden" name={name} value={current} /> : null}
    </>
  );
}
