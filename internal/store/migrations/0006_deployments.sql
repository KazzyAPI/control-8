CREATE TABLE deployment (
  id           TEXT PRIMARY KEY,
  stack_id     TEXT NOT NULL REFERENCES stack(id),
  revision_id  TEXT,
  action       TEXT NOT NULL CHECK (action IN ('apply','teardown')),
  trigger      TEXT NOT NULL DEFAULT 'manual' CHECK (trigger IN ('manual','image_update','digest_poll')),
  status       TEXT NOT NULL CHECK (status IN ('pending_approval','queued','running','succeeded','failed','rejected','cancelled')),
  requested_by TEXT NOT NULL,
  approved_by  TEXT,
  grant_id     TEXT,
  grant_bypass TEXT,
  reason       TEXT,
  rollout      TEXT,
  started_at   TEXT,
  finished_at  TEXT,
  output       TEXT NOT NULL DEFAULT '',
  error        TEXT,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE INDEX deployment_stack ON deployment(stack_id);
-- DOM-001: at most one queued or running deployment per stack, enforced by the database.
CREATE UNIQUE INDEX deployment_one_open ON deployment(stack_id) WHERE status IN ('queued','running');
