-- Hosted-service support: disabling accounts and browser-based CLI login.

alter table users add column disabled_at timestamptz;

-- Device authorization for `tund login`. The CLI generates its authtoken
-- locally and only sends the hash; approving the code in the dashboard turns
-- that hash into an authtokens row. The plaintext token never reaches the server.
create table device_codes (
  id               uuid primary key default gen_random_uuid(),
  device_code_hash text not null unique,        -- sha256 hex of the secret the CLI polls with
  user_code        text not null unique,        -- shown in the terminal, e.g. "WDJB-MJHT"
  token_hash       text not null,               -- sha256 hex of the CLI-generated authtoken
  token_prefix     text not null,
  client_hostname  text not null default '',
  client_os        text not null default '',
  client_ip        text not null default '',
  status           text not null default 'pending' check (status in ('pending', 'approved', 'denied')),
  user_id          uuid references users(id) on delete cascade,
  authtoken_id     uuid references authtokens(id) on delete set null,
  created_at       timestamptz not null default now(),
  expires_at       timestamptz not null
);
create index device_codes_expires_idx on device_codes(expires_at);
