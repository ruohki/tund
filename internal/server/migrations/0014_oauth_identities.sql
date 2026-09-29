-- Sign-in with Google and GitHub (dashboard, Admin → Sign-in). An identity is
-- the provider's stable account id; accounts created through a provider have
-- no password until they set one.
alter table users alter column password_hash drop not null;
create table user_identities (
  id           uuid primary key default gen_random_uuid(),
  user_id      uuid not null references users(id) on delete cascade,
  provider     text not null check (provider in ('google', 'github')),
  subject      text not null,             -- Google "sub", GitHub numeric user id
  email        text not null default '',  -- as the provider reported it, for display
  created_at   timestamptz not null default now(),
  last_used_at timestamptz,
  unique (provider, subject),
  unique (user_id, provider)
);
