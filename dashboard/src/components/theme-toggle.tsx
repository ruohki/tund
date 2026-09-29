"use client";

import { useEffect, useState } from "react";
import { Monitor, Moon, Sun } from "lucide-react";
import { cn } from "./ui";

type Theme = "system" | "light" | "dark";

function applyTheme(t: Theme) {
  try {
    if (t === "system") localStorage.removeItem("tund-theme");
    else localStorage.setItem("tund-theme", t);
  } catch {
    /* storage unavailable */
  }
  const root = document.documentElement;
  if (t === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", t);
}

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>("system");

  useEffect(() => {
    try {
      const t = localStorage.getItem("tund-theme");
      // eslint-disable-next-line react-hooks/set-state-in-effect
      if (t === "light" || t === "dark") setTheme(t);
    } catch {
      /* storage unavailable */
    }
  }, []);

  const apply = (t: Theme) => {
    setTheme(t);
    applyTheme(t);
  };

  const options: { id: Theme; icon: typeof Sun; label: string }[] = [
    { id: "system", icon: Monitor, label: "Match system" },
    { id: "light", icon: Sun, label: "Light" },
    { id: "dark", icon: Moon, label: "Dark" },
  ];

  return (
    <div role="radiogroup" aria-label="Color theme" className="inline-flex rounded-[5px] border border-line p-0.5">
      {options.map(({ id, icon: Icon, label }) => (
        <button
          key={id}
          type="button"
          role="radio"
          aria-checked={theme === id}
          aria-label={label}
          title={label}
          onClick={() => apply(id)}
          className={cn(
            "grid h-6 w-7 place-items-center rounded-[3px] transition-colors",
            theme === id ? "bg-surface-3 text-ink" : "text-muted hover:text-ink",
          )}
        >
          <Icon size={13} />
        </button>
      ))}
    </div>
  );
}
