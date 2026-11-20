CREATE TABLE "user" (
  id            TEXT PRIMARY KEY,
  issuer        TEXT NOT NULL,
  subject       TEXT NOT NULL,
  email         TEXT,
  display_name  TEXT NOT NULL DEFAULT '',
  groups        TEXT NOT NULL DEFAULT '[]',
  is_break_glass INTEGER NOT NULL DEFAULT 0,
  last_login_at TEXT,
  UNIQUE (issuer, subject)
);
CREATE TABLE session (
  id_hash     TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
  csrf_token  TEXT NOT NULL,
  auth_method TEXT NOT NULL CHECK (auth_method IN ('oidc','break_glass')),
  expires_at  TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
-- namespace_id holds the namespace slug until M2 adds the namespace table.
CREATE TABLE role_binding (
  id           TEXT PRIMARY KEY,
  idp_group    TEXT NOT NULL,
  namespace_id TEXT,
  role         TEXT NOT NULL CHECK (role IN ('viewer','operator','admin','global_admin'))
);
CREATE TABLE token (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,
  hash         TEXT NOT NULL UNIQUE,
  owner_sub    TEXT NOT NULL,
  namespace_id TEXT,
  role         TEXT NOT NULL CHECK (role IN ('viewer','operator','admin','global_admin')),
  expires_at   TEXT NOT NULL,
  last_used_at TEXT,
  revoked_at   TEXT
);
CREATE TABLE audit_event (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  at           TEXT NOT NULL,
  actor_type   TEXT NOT NULL CHECK (actor_type IN ('user','token','system','parachute')),
  actor_id     TEXT,
  actor_display TEXT,
  auth_method  TEXT,
  namespace_id TEXT,
  action       TEXT NOT NULL,
  target_type  TEXT,
  target_id    TEXT,
  outcome      TEXT NOT NULL CHECK (outcome IN ('success','denied','failed')),
  details      TEXT NOT NULL DEFAULT '{}',
  request_id   TEXT
);
CREATE TRIGGER audit_event_no_update BEFORE UPDATE ON audit_event BEGIN SELECT RAISE(ABORT, 'audit_event is append-only'); END;
CREATE TRIGGER audit_event_no_delete BEFORE DELETE ON audit_event BEGIN SELECT RAISE(ABORT, 'audit_event is append-only'); END;
