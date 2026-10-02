"use client";

import { useEffect, useState } from "react";
import { CopyButton } from "./client-ui";
import { cn } from "./ui";
import { highlightCode, type CodeLang } from "./code-highlight";

// Reading the identity headers (docs/SPEC.md "OIDC identity headers") in the
// common web stacks: email for a greeting, groups for an admins-only page.

const EXAMPLES: { id: string; label: string; lang: CodeLang; code: string }[] = [
  {
    id: "node",
    label: "Node.js",
    lang: "js",
    code: `// Express: set after the visitor signed in
app.get("/admin", (req, res) => {
  const email = req.get("X-Tund-User-Email");
  const groups = (req.get("X-Tund-User-Groups") ?? "").split(",");
  if (!groups.includes("admins")) return res.status(403).send("Admins only");
  res.send(\`Hello \${email}\`);
});`,
  },
  {
    id: "python",
    label: "Python",
    lang: "python",
    code: `# Flask: set after the visitor signed in
from flask import Flask, abort, request

app = Flask(__name__)

@app.get("/admin")
def admin():
    email = request.headers.get("X-Tund-User-Email")
    groups = request.headers.get("X-Tund-User-Groups", "").split(",")
    if "admins" not in groups:
        abort(403, "Admins only")
    return f"Hello {email}"`,
  },
  {
    id: "go",
    label: "Go",
    lang: "go",
    code: `// net/http: set after the visitor signed in
http.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
	email := r.Header.Get("X-Tund-User-Email")
	groups := strings.Split(r.Header.Get("X-Tund-User-Groups"), ",")
	if !slices.Contains(groups, "admins") {
		http.Error(w, "Admins only", http.StatusForbidden)
		return
	}
	fmt.Fprintf(w, "Hello %s", email)
})`,
  },
  {
    id: "rust",
    label: "Rust",
    lang: "rust",
    code: `// axum: set after the visitor signed in
use axum::http::{HeaderMap, StatusCode};

async fn admin(headers: HeaderMap) -> Result<String, StatusCode> {
    let header = |name: &str| {
        headers.get(name).and_then(|v| v.to_str().ok()).unwrap_or("")
    };
    if !header("X-Tund-User-Groups").split(',').any(|g| g == "admins") {
        return Err(StatusCode::FORBIDDEN);
    }
    Ok(format!("Hello {}", header("X-Tund-User-Email")))
}`,
  },
  {
    id: "java",
    label: "Java",
    lang: "java",
    code: `// Spring Boot: set after the visitor signed in
@GetMapping("/admin")
String admin(@RequestHeader("X-Tund-User-Email") String email,
             @RequestHeader(value = "X-Tund-User-Groups", defaultValue = "") String groups) {
    if (!Arrays.asList(groups.split(",")).contains("admins")) {
        throw new ResponseStatusException(HttpStatus.FORBIDDEN, "Admins only");
    }
    return "Hello " + email;
}`,
  },
  {
    id: "csharp",
    label: "C#",
    lang: "csharp",
    code: `// ASP.NET Core: set after the visitor signed in
app.MapGet("/admin", (HttpRequest req) =>
{
    var email = req.Headers["X-Tund-User-Email"].ToString();
    var groups = req.Headers["X-Tund-User-Groups"].ToString().Split(',');
    return groups.Contains("admins")
        ? Results.Text($"Hello {email}")
        : Results.Text("Admins only", statusCode: 403);
});`,
  },
];

const STORAGE_KEY = "tund.identity-example";

/** The identity-header example in the viewer's language (remembered in this browser). */
export function IdentityExamples() {
  const [active, setActive] = useState(EXAMPLES[0].id);
  useEffect(() => {
    try {
      const saved = window.localStorage.getItem(STORAGE_KEY);
      // eslint-disable-next-line react-hooks/set-state-in-effect
      if (saved && EXAMPLES.some((e) => e.id === saved)) setActive(saved);
    } catch {
      /* storage unavailable */
    }
  }, []);
  const pick = (id: string) => {
    setActive(id);
    try {
      window.localStorage.setItem(STORAGE_KEY, id);
    } catch {
      /* storage unavailable */
    }
  };
  const example = EXAMPLES.find((e) => e.id === active) ?? EXAMPLES[0];

  return (
    <div className="overflow-hidden rounded-md border border-line bg-surface-2">
      {/* The bottom line is an inset shadow so the active tab's border can sit on it without overflowing. */}
      <div className="flex items-center justify-between gap-2 px-1 shadow-[inset_0_-1px_0_var(--color-line)]">
        <div role="tablist" aria-label="Language" className="flex min-w-0 overflow-x-auto overflow-y-hidden scroll-thin">
          {EXAMPLES.map((e) => (
            <button
              key={e.id}
              type="button"
              role="tab"
              aria-selected={e.id === example.id}
              onClick={() => pick(e.id)}
              className={cn(
                "shrink-0 border-b-2 px-2.5 py-1.5 text-[12.5px] font-medium transition-colors",
                e.id === example.id ? "border-ink text-ink" : "border-transparent text-muted hover:text-ink",
              )}
            >
              {e.label}
            </button>
          ))}
        </div>
        <CopyButton value={example.code} />
      </div>
      <pre
        role="tabpanel"
        aria-label={`${example.label} example`}
        className="overflow-x-auto scroll-thin p-3 font-mono text-[12px] leading-5 text-ink [tab-size:4]"
      >
        <code>{highlightCode(example.code, example.lang)}</code>
      </pre>
    </div>
  );
}
