-- Admin area: runtime instance settings, email flows and an audit log.

-- Instance settings edited in the dashboard. A stored value wins over the
-- corresponding TUND_* environment variable, which only provides the default.
create table settings (
  key        text primary key,
  value      jsonb not null,
  updated_at timestamptz not null default now(),
  updated_by uuid references users(id) on delete set null
);

-- Email verification. Accounts that existed before are treated as verified.
alter table users add column email_verified_at timestamptz;
update users set email_verified_at = created_at;

-- One-time tokens sent by email (verification, password reset).
create table email_tokens (
  id         uuid primary key default gen_random_uuid(),
  user_id    uuid not null references users(id) on delete cascade,
  kind       text not null check (kind in ('verify', 'reset')),
  token_hash text not null unique,             -- sha256 hex of the token in the link
  created_at timestamptz not null default now(),
  expires_at timestamptz not null,
  used_at    timestamptz
);
create index email_tokens_user_idx on email_tokens(user_id, kind);

create table audit_log (
  id          bigserial primary key,
  actor_id    uuid references users(id) on delete set null,
  actor_email text not null default '',
  action      text not null,                   -- e.g. "user.disable", "settings.update"
  target      text not null default '',        -- human-readable subject (email, hostname, …)
  details     jsonb not null default '{}',
  ip          text not null default '',
  created_at  timestamptz not null default now()
);
create index audit_log_created_idx on audit_log(created_at desc);
