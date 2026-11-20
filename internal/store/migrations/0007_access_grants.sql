-- 09: single-scope, time-boxed prod access grants (never deleted, GRT-026).
CREATE TABLE access_grant (
  id               TEXT PRIMARY KEY,
  requester_id     TEXT NOT NULL,
  target_id        TEXT NOT NULL,
  namespace_id     TEXT NOT NULL REFERENCES namespace(id),
  reason           TEXT NOT NULL,
  ticket_ref       TEXT,
  duration_minutes INTEGER NOT NULL,
  req_starts_at    TEXT,
  starts_at        TEXT,
  expires_at       TEXT,
  status           TEXT NOT NULL CHECK (status IN ('requested','approved','active','expired','denied','revoked')),
  approver_group   TEXT NOT NULL,
  approver_id      TEXT,
  decided_at       TEXT,
  decision_note    TEXT,
  revoked_by       TEXT,
  revoked_at       TEXT,
  revoke_reason    TEXT,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);
-- GRT-021: one open grant per user and scope.
CREATE UNIQUE INDEX access_grant_open ON access_grant(requester_id, target_id, namespace_id) WHERE status IN ('requested','approved','active');
CREATE INDEX access_grant_ns ON access_grant(namespace_id);
