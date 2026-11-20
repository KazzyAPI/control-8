CREATE TABLE target (
  name             TEXT PRIMARY KEY,
  kind             TEXT NOT NULL CHECK (kind IN ('kubernetes','docker')),
  environment_tier TEXT NOT NULL CHECK (environment_tier IN ('prod','non-prod')),
  config           TEXT NOT NULL DEFAULT '{}',
  created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
