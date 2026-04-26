create extension if not exists pgcrypto;

create table public.orgs (
  id uuid primary key default gen_random_uuid(),
  name text not null,
  github_org_slug text unique,
  plan text not null default 'free',
  created_at timestamptz not null default now()
);

create table public.users (
  id uuid primary key references auth.users (id),
  org_id uuid not null references public.orgs (id),
  role text not null default 'member',
  created_at timestamptz not null default now()
);

create table public.repos (
  id uuid primary key default gen_random_uuid(),
  org_id uuid not null references public.orgs (id),
  github_repo_full_name text not null,
  connected_at timestamptz not null default now(),
  active boolean not null default true
);

create table public.bots (
  id uuid primary key default gen_random_uuid(),
  name text not null unique,
  version text,
  author_id uuid references public.users (id),
  description text,
  manifest jsonb,
  pricing_tier text not null default 'free',
  price_per_execution numeric not null default 0,
  published boolean not null default false,
  created_at timestamptz not null default now()
);

create table public.bot_installations (
  id uuid primary key default gen_random_uuid(),
  org_id uuid not null references public.orgs (id),
  bot_id uuid not null references public.bots (id),
  installed_by uuid references public.users (id),
  config jsonb,
  active boolean not null default true,
  installed_at timestamptz not null default now()
);

create table public.executions (
  id uuid primary key default gen_random_uuid(),
  org_id uuid not null references public.orgs (id),
  bot_id uuid references public.bots (id),
  repo_id uuid references public.repos (id),
  status text,
  trigger_event jsonb,
  result jsonb,
  started_at timestamptz,
  completed_at timestamptz,
  requires_review boolean not null default false
);

create table public.attestations (
  id uuid primary key default gen_random_uuid(),
  org_id uuid not null references public.orgs (id),
  execution_id uuid references public.executions (id),
  artifact_hash text,
  rekor_log_index bigint,
  rekor_inclusion_proof jsonb,
  slsa_predicate jsonb,
  signed_at timestamptz not null default now()
);

create table public.review_queue (
  id uuid primary key default gen_random_uuid(),
  org_id uuid not null references public.orgs (id),
  execution_id uuid references public.executions (id),
  diff_content text,
  claude_summary text,
  status text not null default 'pending',
  reviewed_by uuid references public.users (id),
  reviewed_at timestamptz,
  created_at timestamptz not null default now()
);

create table public.events (
  id uuid primary key default gen_random_uuid(),
  org_id uuid not null references public.orgs (id),
  repo_id uuid references public.repos (id),
  event_type text,
  payload jsonb,
  received_at timestamptz not null default now(),
  processed boolean not null default false
);

create or replace function public.get_org_id()
returns uuid
language sql
stable
set search_path = public, auth
as $$
  select u.org_id
  from public.users as u
  where u.id = auth.uid()
  limit 1
$$;

alter table public.orgs enable row level security;
alter table public.users enable row level security;
alter table public.repos enable row level security;
alter table public.bots enable row level security;
alter table public.bot_installations enable row level security;
alter table public.executions enable row level security;
alter table public.attestations enable row level security;
alter table public.review_queue enable row level security;
alter table public.events enable row level security;

create policy orgs_select_own_org
on public.orgs
for select
using (id = public.get_org_id());

create policy users_select_own_row
on public.users
for select
using (id = auth.uid());

create policy users_update_own_row
on public.users
for update
using (id = auth.uid())
with check (id = auth.uid());

create policy users_admin_select_org_members
on public.users
for select
using (
  org_id = public.get_org_id()
  and exists (
    select 1
    from public.users as me
    where me.id = auth.uid()
      and me.role = 'admin'
  )
);

create policy repos_org_scope_all
on public.repos
for all
using (org_id = public.get_org_id())
with check (org_id = public.get_org_id());

create policy bot_installations_org_scope_all
on public.bot_installations
for all
using (org_id = public.get_org_id())
with check (org_id = public.get_org_id());

create policy executions_org_scope_all
on public.executions
for all
using (org_id = public.get_org_id())
with check (org_id = public.get_org_id());

create policy attestations_org_scope_all
on public.attestations
for all
using (org_id = public.get_org_id())
with check (org_id = public.get_org_id());

create policy review_queue_org_scope_all
on public.review_queue
for all
using (org_id = public.get_org_id())
with check (org_id = public.get_org_id());

create policy events_org_scope_all
on public.events
for all
using (org_id = public.get_org_id())
with check (org_id = public.get_org_id());

create policy bots_select_published
on public.bots
for select
using (published = true);

create policy bots_select_author_owned
on public.bots
for select
using (author_id = auth.uid());

create policy bots_update_author_owned
on public.bots
for update
using (author_id = auth.uid())
with check (author_id = auth.uid());

create policy bots_delete_author_owned
on public.bots
for delete
using (author_id = auth.uid());

create index idx_executions_org_status
  on public.executions (org_id, status);

create index idx_executions_bot_id
  on public.executions (bot_id);

create index idx_attestations_artifact_hash
  on public.attestations (artifact_hash);

create index idx_attestations_org_signed_at
  on public.attestations (org_id, signed_at);

create index idx_events_org_processed_received_at
  on public.events (org_id, processed, received_at);

create index idx_review_queue_org_status
  on public.review_queue (org_id, status);
