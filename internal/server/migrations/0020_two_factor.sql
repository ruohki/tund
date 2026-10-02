-- Dashboard sign-in: two-factor authentication (TOTP + recovery codes) and
-- passkeys (WebAuthn). See docs/SPEC.md "Two-factor authentication and passkeys".

-- TOTP secret encrypted by the dashboard (key derived from TUND_INTERNAL_SECRET);
-- '' = two-factor is off. totp_last_step rejects a code that was already used.
alter table users add column totp_secret text not null default '';
alter table users add column totp_enabled_at timestamptz;
alter table users add column totp_last_step bigint not null default 0;
-- Consecutive wrong second-step codes across sign-ins: they lock the second
-- step for a while (growing) and email the owner, whose password is known.
alter table users add column second_factor_failures int not null default 0;
alter table users add column second_factor_failed_at timestamptz;

create table user_recovery_codes (
  user_id uuid not null references users(id) on delete cascade,
  code_hash text not null,
  used_at timestamptz,
  primary key (user_id, code_hash)
);

create table user_passkeys (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references users(id) on delete cascade,
  credential_id text not null unique, -- base64url
  public_key bytea not null,
  counter bigint not null default 0,
  transports text[] not null default '{}',
  name text not null default '',
  backed_up boolean not null default false,
  created_at timestamptz not null default now(),
  last_used_at timestamptz
);
create index user_passkeys_user_idx on user_passkeys(user_id);

-- Short-lived steps of signing in and enrolling: a password accepted while the
-- second factor is missing, a WebAuthn challenge waiting for its answer, a TOTP
-- secret waiting for its first code. The browser holds a token in a cookie,
-- the row its hash.
create table auth_flows (
  id text primary key,
  purpose text not null,
  user_id uuid references users(id) on delete cascade,
  data text not null default '',
  next text not null default '/',
  attempts int not null default 0,
  expires_at timestamptz not null
);
create index auth_flows_expires_idx on auth_flows(expires_at);
