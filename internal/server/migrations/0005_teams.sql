-- Teams: users share OIDC providers and domains (static hostnames + custom
-- domains). Resources keep user_id as their creator; team_id set means the
-- team owns them.

create table teams (
  id         uuid primary key default gen_random_uuid(),
  name       text not null,
  slug       text not null unique,          -- a-z0-9-, used as `--oidc <team>/<provider>`
  created_by uuid references users(id) on delete set null,
  created_at timestamptz not null default now()
);

create table team_members (
  team_id    uuid not null references teams(id) on delete cascade,
  user_id    uuid not null references users(id) on delete cascade,
  role       text not null check (role in ('owner', 'admin', 'member')),
  created_at timestamptz not null default now(),
  primary key (team_id, user_id)
);
create index team_members_user_idx on team_members(user_id);

-- Invite links. email '' = anyone with the link; otherwise only that address.
create table team_invites (
  id          uuid primary key default gen_random_uuid(),
  team_id     uuid not null references teams(id) on delete cascade,
  email       text not null default '',
  role        text not null check (role in ('admin', 'member')),
  token_hash  text not null unique,         -- sha256 hex of the token in the link
  invited_by  uuid references users(id) on delete set null,
  created_at  timestamptz not null default now(),
  expires_at  timestamptz not null,
  accepted_by uuid references users(id) on delete set null,
  accepted_at timestamptz
);
create index team_invites_team_idx on team_invites(team_id);

-- OIDC providers: personal (team_id null, slug unique per user) or team-owned
-- (slug unique per team).
alter table oidc_providers add column team_id uuid references teams(id) on delete cascade;
alter table oidc_providers drop constraint oidc_providers_user_id_slug_key;
create unique index oidc_providers_personal_slug_idx on oidc_providers(user_id, slug) where team_id is null;
create unique index oidc_providers_team_slug_idx on oidc_providers(team_id, slug) where team_id is not null;

-- Domains: team-owned domains are usable by every member; the default static
-- hostname is a personal concept only.
alter table domains add column team_id uuid references teams(id) on delete cascade;
alter table domains add constraint domains_team_not_default check (team_id is null or not is_default);
create index domains_team_idx on domains(team_id) where team_id is not null;
