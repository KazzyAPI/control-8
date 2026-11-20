CREATE TABLE stack (
  id               TEXT PRIMARY KEY,
  namespace_id     TEXT NOT NULL REFERENCES namespace(id),
  asset_id         TEXT NOT NULL REFERENCES asset(id),
  target_id        TEXT NOT NULL,
  slug             TEXT NOT NULL,
  runtime          TEXT NOT NULL CHECK (runtime IN ('kubernetes','docker')),
  k8s_namespace    TEXT,
  state            TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft','active','failed','removed')),
  desired_revision INTEGER,
  safety_profile   TEXT,
  rollout_frozen   INTEGER NOT NULL DEFAULT 0,
  drift            TEXT NOT NULL DEFAULT '[]',
  digest_status    TEXT,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL,
  UNIQUE (namespace_id, slug)
);
CREATE TABLE revision (
  id                      TEXT PRIMARY KEY,
  stack_id                TEXT NOT NULL REFERENCES stack(id),
  number                  INTEGER NOT NULL,
  safety_profile          TEXT NOT NULL,
  origin                  TEXT NOT NULL DEFAULT 'user' CHECK (origin IN ('user','image_update')),
  base_revision           INTEGER,
  manifests_input         TEXT,
  rendered                TEXT NOT NULL,
  inventory               TEXT NOT NULL,
  rollout_timeout_seconds INTEGER NOT NULL,
  digest_poll             INTEGER NOT NULL DEFAULT 0,
  database_services       TEXT NOT NULL DEFAULT '[]',
  policy_result           TEXT NOT NULL,
  created_by              TEXT NOT NULL,
  created_at              TEXT NOT NULL,
  UNIQUE (stack_id, number)
);
CREATE TABLE policy (
  id           TEXT PRIMARY KEY,
  scope        TEXT NOT NULL CHECK (scope IN ('global','namespace')),
  namespace_id TEXT UNIQUE,
  rules        TEXT NOT NULL,
  updated_by   TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO policy(id,scope,rules) VALUES('pol_global','global',
 '{"allowed_registries":[],"allowed_k8s_namespaces":[],"required_labels":[],"protected_db_approval":true,"allowed_safety_profiles":["managed_rollout","as_is","parachute"],"allowed_bind_prefixes":[],"allow_privileged":false}');
