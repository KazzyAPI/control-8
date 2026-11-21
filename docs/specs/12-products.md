# 12 Products

A **product** is the unit teams think in. A product has many **applications** (the existing `asset` entity, renamed in API/UI; the table stays `asset`). An application is deployed as **stacks**: one stack per (target, namespace) placement, each with its own revisions. **Namespace** stays the environment/placement concept and keeps the tier, policy and placement rules. So one product can be seen across all its environments.

```
product 1──n environment (name, tier) ──> (target, namespace)      placement + permissions
   │
   └──1──n application(asset) 1──n stack = application × environment ──> its revisions
```

**Environment vs namespace (decision D-121):** the Control 8 **namespace stays** the placement/permission container (Kubernetes namespace, policy, `allowed_products`, namespace role bindings, fallback approver). An **environment** is a product-scoped, named pointer to one (target, namespace) with a tier. They are not folded together because namespaces are shared by several products and already carry built behaviour (M2–M16 API, policy, K8S-010 mapping); an environment adds the product's vocabulary (dev/uat/staging/prod) without moving any cluster object.

## Entities and migration (M17)

| ID | Rule |
|---|---|
| PRD-001 | `product` (`prd_`): `slug` (global, unique, immutable), `name`, `description`, `owner` (team, free text), `links` (`[{label, url}]`), `approver_group` (the product team's OIDC group; required, except built-in `unassigned`), `tags` (`[]string`, ≤ 20, slug-like). |
| PRD-002 | Asset gets required `product_id`; uniqueness becomes (`product_id`, `slug`). `asset.namespace_id` is kept as nullable `home_namespace_id` (read-only, legacy; null for new applications). An application has no namespace of its own; its stacks do. |
| PRD-003 | Migration (one SQL migration + Go fix-up, idempotent): create built-in product `unassigned` (`approver_group = null`, cannot be deleted or renamed); move every existing asset into it; on slug collision inside `unassigned` rename later rows to `<slug>-<home-namespace>` (audit `asset.migrated_rename`, system). Existing grants get `product = unassigned` (PRD-020). Existing role bindings are unchanged (namespace/global). |
| PRD-004 | Moving an application to another product: `PATCH /products/{p}/applications/{app} {product}` by `global_admin` only, audited `application.reassign`. Its stacks follow; open/active grants for the old product no longer cover it (GRT re-check at deploy). |
| PRD-005 | `managed_by` on applications ∈ `control8` \| `argo` \| `flux` \| `helm` \| `operator` \| `external` (`argo` existed; the others are register-only, 11 DSC-050). Non-`control8` applications have no stacks (D-22). |
| PRD-006 | Delete product: `global_admin`, 409 while it has applications. |

## Environments (M17)

| ID | Rule |
|---|---|
| PRD-040 | `environment` (`env_`): `product_id`, `name` (slug, unique per product), `tier` (`prod` \| `non-prod`), `target_id`, `namespace_id`, `order` (column position). Unique (`product_id`, `target_id`, `namespace_id`): one environment per placement per product. |
| PRD-041 | Presets (`C8_ENVIRONMENT_PRESETS`, ConfigMap JSON, editable by the platform admin; default below). The wizard and API may rename, drop or add custom environments per product; presets are only defaults. |
| PRD-042 | Default presets: `dev` → non-prod, `uat` → non-prod, **`staging` → prod**, `prod` → prod. Staging is prod-tier on purpose: it needs a grant with elevated approval like prod. |
| PRD-043 | Effective tier of a stack = `prod` if its environment **or** namespace **or** target is prod (D-42 extended). An environment can make a placement stricter, never laxer; when tiers differ the UI shows both and the wizard calls it out. |
| PRD-044 | `stack.environment_id` (nullable: legacy stacks created before M17 have none until migrated). New stacks of a non-`unassigned` product must name an environment; the stack's target and namespace are taken from it (`POST /products/{p}/applications/{app}/stacks {environment, slug?}`; the old namespaced create stays for `unassigned` only). Unique (`asset_id`, `environment_id`). |
| PRD-045 | Grant scope (target, namespace, product) is equivalent to (product, environment) because of the PRD-040 uniqueness; the API keeps target + namespace (+ `product`) and returns `environment`; the UI shows the environment name. |
| PRD-046 | Creating an environment, or changing its tier/target/namespace, needs product `admin`; setting or raising tier to `prod` needs typed confirmation of the product slug (`confirm_prod`), lowering a tier needs `global_admin`. Changing placement of an environment with non-`removed` stacks → 409 (create a new environment and deploy there). Audited `environment.create/update/delete`. |

## Authorization (M17)

| ID | Rule |
|---|---|
| PRD-010 | `role_binding` gets optional `product_id`; a binding has at most one of `namespace_id` / `product_id` (both null = global). Product binding with role R = R on that product, all its applications, and **all their stacks in every namespace**. |
| PRD-011 | Effective role on a **stack** (and its revisions, deployments, services) = highest of: global binding, binding on `stack.namespace`, binding on the stack's product. On a **namespace** (namespace page, namespace policy, creating stacks there) = highest of global and namespace binding. On a **product/application** = highest of global and product binding. Highest wins; there are no deny bindings (AUTH-020 update). |
| PRD-012 | Creating a stack (placing an application in a namespace) needs `operator` on the product **and** the namespace's effective policy `allowed_products` (new rule, default `[]` = any) must include the product (else 403 `product_not_allowed_in_namespace`). Namespace admins thus control which products may land in their namespace; product teams control their apps. |
| PRD-013 | Product settings: `admin` on the product edits name/description/owner/links/tags; `approver_group` changes are `global_admin` only (same reason as D-50), audited `product.approver_group_change`. |
| PRD-014 | Visibility: a namespace is listed if the caller has a namespace role or can see at least one stack in it through a product role (then only those stacks are shown). API-005 (404 vs 403) applies per resource with the effective role above. |

## Approvers and grants (M17, reworks M8)

| ID | Rule |
|---|---|
| PRD-020 | Grant scope becomes **(target, namespace, product)**. A namespace can host several products and each product has exactly one approver group, so a three-part scope keeps "one grant, one approver group" and never lets one product's approver open another product's prod deploys. `grants.Require(principal, stack)` matches `stack.target`, `stack.namespace` and the stack's application's product. |
| PRD-021 | Approver group of a grant = `product.approver_group`; for the built-in `unassigned` product only, it falls back to `namespace.approver_group` (the M8 rule); none → 409 `approver_group_missing`. `namespace.approver_group` is kept **only** as this fallback (and stays required on prod namespaces so legacy apps remain deployable). |
| PRD-022 | Compatibility: `POST /access-grants` gains `product` (slug); omitted ⇒ `unassigned` (old clients keep working for legacy apps). Existing rows are migrated to `product = unassigned` with their status and expiry intact. `approver_group` on a grant is the resolved group snapshotted at request time; approval checks the current resolved group. |
| PRD-023 | `GET /me` keeps `approver_for` (namespaces, fallback semantics) and adds `approver_for_products` (product slugs whose `approver_group` the user is in). |
| PRD-024 | Audit details of grant actions and of prod-gated actions add `product` next to `grant_id`; `access_grant.updated` payload gains `product` and `approver_group`; it is delivered to the requester, members of the resolved approver group, `admin`s of the product, and `global_admin`. |

## API (M17)

| Method | Path | Purpose | Min role |
|---|---|---|---|
| GET, POST | `/api/v1/products` | List visible products (filter `?tag=`) / create (`approver_group` required) | any / global_admin |
| GET, PATCH, DELETE | `/api/v1/products/{product}` | Read / update (PRD-013) / delete (PRD-006) | viewer / admin / global_admin |
| GET, POST | `/api/v1/products/{product}/applications` | List / create application (asset) | viewer / operator (product) |
| GET, PATCH, DELETE | `/api/v1/products/{product}/applications/{app}` | Read / update (reassign: PRD-004) / delete (409 if stacks) | viewer / operator / admin |
| GET | `/api/v1/products/{product}/overview` | Matrix: applications × environments (PRD-030) | viewer (product) |
| GET | `/api/v1/namespaces/{ns}/assets?product=` | Existing list, now filterable by product; returns applications with a stack in `ns` or legacy home `ns`. `POST` there is deprecated: accepts optional `product` (default `unassigned`) | unchanged |

Stack create (`POST /namespaces/{ns}/stacks`) gains `product`; `asset` is resolved within it. Without `product`, the asset slug must resolve uniquely among applications visible to the caller, else 400 `asset_ambiguous`. `Stack` and `Deployment` responses add `product` (slug) and `application` (= asset slug; `asset` kept as an alias).

### Product overview (matrix) — `GET /products/{p}/overview`

| ID | Rule |
|---|---|
| PRD-030 | Response `{product, environments: [{name, tier, target, namespace, order}], applications: [{slug, name, managed_by, cells: [{target, namespace, stack, state, safety_profile, desired_revision, images: [{container, image, digest}], health, drift_count, rollout: {state, result} \| null, pending_grants, open_findings}]}], totals: {apps, stacks, prod, non_prod, unhealthy, open_findings, active_grants}}`. Columns = the product's environments in `order`, plus one "legacy" column per (target, namespace) holding stacks without an environment. Only stacks the caller may see are included. `open_findings` counts M20 findings linked to the product (0 before M20). |
| PRD-031 | Built from DB rows already maintained by inventory/rollout/grants; no cluster call. The SPA keeps it live by patching cells from `stack.updated`, `deployment.updated`, `access_grant.updated` and (M20) `discovery.finding` events, and refetching on `product.updated`. |

## Migration assistant (M17b backend, M18b wizard)

One wizard turns legacy data (applications in `unassigned`, stacks without environment) — and from M21 discovered workloads — into products, environments and placements.

| ID | Rule |
|---|---|
| MIG-001 | Input (wizard state, sent whole each time): `product` (existing slug, or new `{slug, name, owner, approver_group, tags}`), optional `team_binding` (`{idp_group, role}` → product role binding), `environments` (`[{name, tier, target, namespace}]`, from presets or custom), `assignments` (`[{kind: application\|stack\|discovered_workload, id, environment?, application?}]`; omitted environment = stays unassigned), from M21 `adopt` (`[{discovered workload ids, application, environment}]`). |
| MIG-002 | `POST /migrations/dry-run` computes and returns a plan without storing it. `POST /migrations/plans` computes, stores (expires 1 h) and returns it with `id` and `version`. Nothing else is written. |
| MIG-003 | A plan lists every change as `{entity, id?, action: create\|update\|move\|bind, before, after}`: product, environments, application reassignments, stack `environment_id`, role bindings; plus **approver resolution** before/after per stack (e.g. "namespace group `shop-ns-approvers` → product group `shop-approvers`"), **grant implications** (active/requested grants that stop covering a moved app, PRD-004), **tier changes** per stack (effective tier before/after), **policy findings** (namespace `allowed_products` refusals, M20 findings of included workloads) and **callouts** `{severity, effect: harder\|easier\|neutral, text}` for anything that makes prod deploys harder or easier. |
| MIG-004 | Validation: an `approver_group` not seen in any user's groups at recent logins (cached claims, 90 d) → warning, not error (the IdP is the authority). A placement whose namespace policy refuses the product → error. Tiers are never defaulted to prod silently: every environment's tier is explicit input; any environment with tier `prod` (or a stack whose effective tier becomes prod) requires `confirm_prod = <product slug>` at apply. |
| MIG-005 | `POST /migrations/plans/{id}/apply {confirm_prod?}` applies all DB changes in **one SQLite transaction**, then starts adoption items as normal adopt deployments (DSC-045: prod grant gate, events) and reports their ids. The plan records `updated_at` of every row it reads; any change since → 409 `plan_stale` (re-plan). Applying an applied plan returns its result (idempotent); adoption items are resumable per item (DSC-046). |
| MIG-006 | Every changed entity is audited with its own action (`product.create`, `environment.create`, `application.reassign`, `stack.environment_set`, `role_binding.create`) carrying `details.migration_id`, plus one `migration.apply` summary event. |
| MIG-007 | Undo: `POST /migrations/{id}/undo` within `C8_MIGRATION_UNDO_WINDOW` (default 7 d) builds and applies the reverse plan in one transaction, if every touched row is unchanged since the apply; else 409 `undo_conflict` listing rows (the user can build a manual reverse plan in the wizard). **Not undoable:** executed adoptions (use release, DSC-049), deployments and grants made after the migration, audit records; a created product or environment is deleted by undo only if still empty. |
| MIG-008 | Permissions: `global_admin`, or `admin` of the target product. Creating a product or setting `approver_group` stays `global_admin` (PRD-006/013). A product admin may claim applications from `unassigned` only if they are also `admin` of every namespace where those applications have stacks. |
| MIG-009 | Never writes to a cluster except adoption items, which go through the normal adopt path under the grant gate. |
| MIG-010 | Events: `migration.plan.created` (`MigrationPlan` summary) and `migration.applied` / `migration.undone` (`MigrationResult`), delivered to the plan creator, the product's admins and `global_admin`. |
| MIG-011 | Entry points: overview card "N applications need migrating" (count of `unassigned` applications plus legacy stacks), Products › Migrate (`/products/migrate`), and from M21 the Cluster page "Adopt…" which opens the wizard with discovered workloads preselected. |

### Legacy data at M17

| Item | Migration |
|---|---|
| Assets | All into `unassigned` (PRD-003) |
| Stacks | Keep target/namespace; `environment_id = null` (legacy column in matrices) until the wizard assigns one |
| Role bindings | Unchanged (namespace/global); product bindings only via wizard or role-binding API |
| Grants | `product = unassigned`, scope otherwise unchanged; approver = namespace group (PRD-021) |
| Demo/seed data (kind e2e fixtures, `scripts/smoke.sh`) | Updated to create product `shop` with `dev`/`staging`/`prod` environments and to exercise one legacy app through the wizard API |

## Events (M17)

| Event | Payload | Delivered to |
|---|---|---|
| `product.updated` | `Product` | Anyone who can see the product |
| `product.removed` | `{slug}` | Same |
| `application.updated` | `Application` | Viewers of the product |
| `environment.updated` | `Environment` | Viewers of the product |

`stack.*`, `deployment.*`, `revision.*` events are additionally delivered to holders of a product role on the stack's product (PRD-011).

## UI (M18)

See 06 "Products". Breadcrumbs everywhere: **Product › Application › Stack** (stack shown as `namespace @ target`).
