-- Verified custom domains are re-checked (docs/SPEC.md "Custom domains"):
-- checked_at = last check, check_failures = failed checks in a row. When the
-- ownership record stays gone, verification is withdrawn: verified_at = NULL,
-- with unverified_at/_reason shown to the owner, and unverified_notified_at
-- set by the dashboard that sent the email about it.
alter table domains
  add column checked_at             timestamptz,
  add column check_failures         integer not null default 0,
  add column unverified_at          timestamptz,
  add column unverified_reason      text not null default '',
  add column unverified_notified_at timestamptz;
create index domains_check_due_idx on domains (checked_at) where kind = 'custom' and verified_at is not null;

-- Abuse report emails: with several dashboards, the one that sets this sends.
-- Existing reports count as sent.
alter table abuse_reports add column notified_at timestamptz;
update abuse_reports set notified_at = created_at;
