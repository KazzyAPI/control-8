CREATE TABLE namespace (
  id               TEXT PRIMARY KEY,
  slug             TEXT NOT NULL UNIQUE,
  display_name     TEXT NOT NULL,
  environment_tier TEXT NOT NULL CHECK (environment_tier IN ('prod','non-prod')),
  approver_group   TEXT,
  description      TEXT,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);
CREATE TABLE asset (
  id           TEXT PRIMARY KEY,
  namespace_id TEXT NOT NULL REFERENCES namespace(id),
  slug         TEXT NOT NULL,
  name         TEXT NOT NULL,
  description  TEXT,
  owner        TEXT,
  image_repo   TEXT,
  managed_by   TEXT NOT NULL DEFAULT 'control8' CHECK (managed_by IN ('control8','argo')),
  external_url TEXT,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE (namespace_id, slug)
);
