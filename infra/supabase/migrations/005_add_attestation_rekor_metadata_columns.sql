alter table public.attestations
  add column if not exists rekor_uuid text,
  add column if not exists rekor_signed_entry_timestamp text;

create index if not exists idx_attestations_rekor_uuid
  on public.attestations (rekor_uuid);
