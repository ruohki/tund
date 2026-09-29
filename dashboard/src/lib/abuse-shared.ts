// Values shared by the public report form and the admin queue (docs/SPEC.md "Abuse protection").

export const REPORT_CATEGORIES = [
  { value: "phishing", label: "Phishing or impersonation" },
  { value: "malware", label: "Malware or unwanted software" },
  { value: "fraud", label: "Fraud or scam" },
  { value: "spam", label: "Spam" },
  { value: "illegal", label: "Illegal content" },
  { value: "other", label: "Something else" },
] as const;

export type ReportCategory = (typeof REPORT_CATEGORIES)[number]["value"];

export const REPORT_SOURCES = [
  { value: "form", label: "Report form" },
  { value: "safe_browsing", label: "Safe Browsing" },
  { value: "heuristic", label: "Phishing heuristics" },
  { value: "admin", label: "System" },
] as const;

export type ReportSource = (typeof REPORT_SOURCES)[number]["value"];

export const categoryLabel = (v: string) => REPORT_CATEGORIES.find((c) => c.value === v)?.label ?? v;
export const sourceLabel = (v: string) => REPORT_SOURCES.find((s) => s.value === v)?.label ?? v;
