ALTER TABLE stack ADD COLUMN health TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE stack ADD COLUMN observed_at TEXT;
CREATE TABLE service (
  id               TEXT PRIMARY KEY,
  stack_id         TEXT NOT NULL REFERENCES stack(id),
  name             TEXT NOT NULL,
  kind             TEXT,
  desired_replicas INTEGER NOT NULL DEFAULT 0,
  ready_replicas   INTEGER NOT NULL DEFAULT 0,
  image_ref        TEXT,
  image_id         TEXT,
  state            TEXT NOT NULL,
  health           TEXT NOT NULL,
  is_database      INTEGER NOT NULL DEFAULT 0,
  containers       TEXT NOT NULL DEFAULT '[]',
  observed_at      TEXT NOT NULL,
  UNIQUE (stack_id, name)
);
