-- Plans and billing (docs/SPEC.md "Plans and billing"). Free accounts get the
-- instance limits, Pro accounts the pro_* settings. Pro comes from a Stripe
-- subscription, a paid team (its payer on Team, every member on Team Pro), or
-- an administrator granting it. The edge and the dashboard read the same
-- functions below.

alter table users add column pro_granted boolean not null default false;
alter table users add column stripe_customer_id text unique;

-- Billing exemption by an administrator: the team gets this plan without a
-- subscription. seats_override replaces the plan's member limit.
alter table teams add column plan_granted text check (plan_granted in ('team', 'team_pro'));
alter table teams add column seats_override integer check (seats_override >= 0);

-- Mirrors the Stripe subscriptions (webhooks keep it current).
create table subscriptions (
  id text primary key,
  customer_id text not null,
  user_id uuid references users(id) on delete set null,
  team_id uuid references teams(id) on delete set null,
  plan text not null check (plan in ('pro', 'team', 'team_pro')),
  billing_interval text not null default 'month',
  status text not null,
  seat_packs integer not null default 0,
  current_period_end timestamptz,
  cancel_at_period_end boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index subscriptions_user_idx on subscriptions(user_id);
create index subscriptions_team_idx on subscriptions(team_id);

-- Stripe keeps retrying a failed payment while past_due: keep the plan meanwhile.
create function subscription_active(status text) returns boolean language sql immutable as $$
  select status in ('active', 'trialing', 'past_due')
$$;

-- The team's plan: granted, else the best active subscription; null = none.
create function team_plan(tid uuid) returns text language sql stable as $$
  select coalesce(
    (select plan_granted from teams where id = tid),
    (select s.plan from subscriptions s
      where s.team_id = tid and s.plan in ('team', 'team_pro') and subscription_active(s.status)
      order by s.plan = 'team_pro' desc limit 1))
$$;

create function user_is_pro(uid uuid) returns boolean language sql stable as $$
  select exists (select 1 from users where id = uid and pro_granted)
    or exists (select 1 from subscriptions s where s.user_id = uid and s.plan = 'pro' and subscription_active(s.status))
    -- Team: Pro for whoever pays, while they own the team.
    or exists (select 1 from subscriptions s join team_members m on m.team_id = s.team_id and m.user_id = s.user_id
      where s.user_id = uid and s.plan = 'team' and subscription_active(s.status) and m.role = 'owner')
    -- Granted Team: Pro for the owners.
    or exists (select 1 from team_members m join teams t on t.id = m.team_id
      where m.user_id = uid and m.role = 'owner' and t.plan_granted = 'team')
    -- Team Pro: Pro for every member.
    or exists (select 1 from team_members m where m.user_id = uid and team_plan(m.team_id) = 'team_pro')
$$;

-- Paying accounts (an active subscription in their own name) gave their name
-- and billing address, so they count as trusted like users.trusted: no browser
-- warning, no custom domain review, none of the hostname word list.
create function user_is_paying(uid uuid) returns boolean language sql stable as $$
  select exists (select 1 from subscriptions s where s.user_id = uid and subscription_active(s.status))
$$;
