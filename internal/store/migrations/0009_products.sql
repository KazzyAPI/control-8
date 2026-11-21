-- 12 Products (M17): product, environment, asset -> application, product-scoped bindings and grants (PRD-001..003, 010, 020, 040, 044).
CREATE TABLE product (
  id             TEXT PRIMARY KEY,
  slug           TEXT NOT NULL UNIQUE,
  name           TEXT NOT NULL,
  description    TEXT,
  owner          TEXT,
  links          TEXT NOT NULL DEFAULT '[]',
  approver_group TEXT,
  tags           TEXT NOT NULL DEFAULT '[]',
  builtin        INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);
INSERT INTO product(id,slug,name,description,builtin,created_at,updated_at) VALUES
  ('prd_unassigned','unassigned','Unassigned','Applications not yet assigned to a product',1,
   strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now'));

CREATE TABLE environment (
  id           TEXT PRIMARY KEY,
  product_id   TEXT NOT NULL REFERENCES product(id),
  name         TEXT NOT NULL,
  tier         TEXT NOT NULL CHECK (tier IN ('prod','non-prod')),
  target_id    TEXT NOT NULL,
  namespace_id TEXT NOT NULL REFERENCES namespace(id),
  position     INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE (product_id, name),
  UNIQUE (product_id, target_id, namespace_id)
);

-- PRD-003: slug collisions inside `unassigned` rename the later rows to <slug>-<home-namespace> (audited, system).
INSERT INTO audit_event(at,actor_type,action,namespace_id,target_type,target_id,outcome,details)
SELECT strftime('%Y-%m-%dT%H:%M:%fZ','now'),'system','asset.migrated_rename',n.slug,'asset',a.id,'success',
       json_object('from',a.slug,'to',a.slug||'-'||n.slug)
FROM asset a JOIN namespace n ON n.id=a.namespace_id
WHERE EXISTS (SELECT 1 FROM asset b WHERE b.slug=a.slug AND (b.created_at<a.created_at OR (b.created_at=a.created_at AND b.id<a.id)));

-- PRD-002: rebuild asset (namespace_id becomes nullable home_namespace_id). The store disables foreign keys while migrating.
CREATE TABLE asset_new (
  id                TEXT PRIMARY KEY,
  product_id        TEXT NOT NULL REFERENCES product(id),
  home_namespace_id TEXT REFERENCES namespace(id),
  slug              TEXT NOT NULL,
  name              TEXT NOT NULL,
  description       TEXT,
  owner             TEXT,
  image_repo        TEXT,
  managed_by        TEXT NOT NULL DEFAULT 'control8' CHECK (managed_by IN ('control8','argo','flux','helm','operator','external')),
  external_ref      TEXT,
  external_url      TEXT,
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL,
  UNIQUE (product_id, slug)
);
INSERT INTO asset_new(id,product_id,home_namespace_id,slug,name,description,owner,image_repo,managed_by,external_url,created_at,updated_at)
SELECT a.id,'prd_unassigned',a.namespace_id,
  CASE WHEN EXISTS (SELECT 1 FROM asset b WHERE b.slug=a.slug AND (b.created_at<a.created_at OR (b.created_at=a.created_at AND b.id<a.id)))
       THEN a.slug||'-'||n.slug ELSE a.slug END,
  a.name,a.description,a.owner,a.image_repo,a.managed_by,a.external_url,a.created_at,a.updated_at
FROM asset a JOIN namespace n ON n.id=a.namespace_id;
DROP TABLE asset;
ALTER TABLE asset_new RENAME TO asset;

ALTER TABLE stack ADD COLUMN environment_id TEXT;
CREATE UNIQUE INDEX stack_asset_env ON stack(asset_id, environment_id) WHERE state != 'removed';

-- PRD-010: a binding has at most one of namespace/product (product_id holds the product id).
ALTER TABLE role_binding ADD COLUMN product_id TEXT;
CREATE TRIGGER role_binding_one_scope BEFORE INSERT ON role_binding
WHEN NEW.namespace_id IS NOT NULL AND NEW.product_id IS NOT NULL
BEGIN SELECT RAISE(ABORT, 'role_binding has at most one of namespace/product'); END;

-- PRD-020: grant scope (target, namespace, product); existing grants keep status and expiry and become `unassigned`.
ALTER TABLE access_grant ADD COLUMN product_id TEXT NOT NULL DEFAULT 'prd_unassigned';
DROP INDEX access_grant_open;
CREATE UNIQUE INDEX access_grant_open ON access_grant(requester_id, target_id, namespace_id, product_id) WHERE status IN ('requested','approved','active');
