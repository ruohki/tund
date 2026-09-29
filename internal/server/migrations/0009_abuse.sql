-- Abuse protection: reports, blocked hostnames, flagged accounts.

create table abuse_reports (
  id             uuid primary key default gen_random_uuid(),
  hostname       text not null,
  url            text not null default '',
  category       text not null default 'other' check (category in ('phishing', 'malware', 'spam', 'fraud', 'illegal', 'other')),
  description    text not null default '',
  reporter_email text not null default '',
  reporter_ip    text not null default '',
  source         text not null check (source in ('form', 'safe_browsing', 'heuristic', 'admin')),
  details        jsonb not null default '{}',
  status         text not null default 'open' check (status in ('open', 'resolved', 'dismissed')),
  user_id        uuid references users(id) on delete set null,    -- tunnel owner at report time
  tunnel_id      uuid references tunnels(id) on delete set null,
  created_at     timestamptz not null default now(),
  resolved_at    timestamptz,
  resolved_by    uuid references users(id) on delete set null,
  resolution     text not null default ''
);
create index abuse_reports_status_idx on abuse_reports(status, created_at desc);
create index abuse_reports_host_idx on abuse_reports(hostname, created_at desc);

-- Hostnames nobody may bind (exact hostnames, e.g. "paypa1-login.tund.io").
create table blocked_hosts (
  hostname   text primary key,
  reason     text not null default '',
  created_by uuid references users(id) on delete set null,
  created_at timestamptz not null default now()
);

alter table users add column flagged_at timestamptz;
alter table users add column flag_reason text not null default '';

-- Custom domains of non-trusted accounts can require an admin's approval.
alter table domains add column approval text not null default 'approved' check (approval in ('approved', 'pending', 'rejected'));
alter table domains add column registered_at date;          -- from RDAP, when known
alter table domains add column risk jsonb not null default '{}'; -- signals shown to the reviewer
