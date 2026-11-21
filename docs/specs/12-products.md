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
| MIG-008 | Permissions: `global_admin`, or an **owner** of the target product (M17c). Creating a new product in a plan follows MEM-005 and makes the caller its first owner; `team_binding` becomes a group-based product binding. An owner may claim applications from `unassigned` only if they are also `admin` of every namespace where those applications have stacks. |
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

## Membership, owners and access requests (M17c, amends M17)

M17 is built as written above; M17c changes the rules below and they win where they differ (PRD-001 `approver_group` becomes optional, PRD-011 namespace bindings, PRD-013, PRD-021, PRD-023).

| ID | Rule |
|---|---|
| MEM-001 | **Membership** = holding any product role. M17 product role bindings gain a user subject: `role_binding` has exactly one of `idp_group` / `user_id`; `user_id` bindings are allowed only with a `product_id` (direct membership). Effective product role = highest of the user's group-based and direct product bindings (and `global_admin`). |
| MEM-002 | Non-members see only the **product directory**: `slug`, `name`, `description`, `owner` (team text), owners' display names, `tags`, and `member: false`. Every other product, application, environment, stack, deployment, grant, finding or migration of that product → 404 (API-005). Built-in `unassigned` is exempt (legacy namespace rules, PRD-021). |
| MEM-003 | On stacks of a non-`unassigned` product, namespace bindings grant at most `viewer` (namespace owners still see what runs in their namespace); deploying, teardown, image updates and revisions need product role ≥ `operator` (or `global_admin`). Amends PRD-011. `allowed_products` (PRD-012) still applies. |
| MEM-004 | **Owners**: `product_owner(product_id, user_id, added_by, added_at)`. The creator is the first owner. Owners have product `admin` implicitly and can: edit product settings incl. `approver_group` (amends PRD-013), manage members (any role up to `admin`) and owners, approve/deny access requests and EA grants. Product `admin`s (non-owners) manage members up to `operator`. |
| MEM-005 | Product creation: `global_admin`, or members of optional `C8_PRODUCT_CREATOR_GROUP` (default unset = `global_admin` only). `approver_group` is optional (amends PRD-001). `global_admin` may add/remove owners of any product (reassign). |
| MEM-006 | The last owner cannot be removed (409 `last_owner`); add another owner first. Owner add/remove audited (`product.owner_add`, `product.owner_remove`) and published (`product.updated`). |
| MEM-007 | **Approver set** of a product = its owners ∪ current members of `approver_group` (if set) — either source suffices (OR). Applies to access requests **and** EA grants. For `unassigned` the set is the namespace `approver_group` (unchanged). Amends PRD-021. |
| MEM-008 | **Never self-approve**: the requester is always removed from the approver set. If the remaining set is empty (sole owner, no group, requesting for themselves), the request is still created, marked `fallback_required`, and only a `global_admin` may decide it (audit `approved_as_fallback`); the UI tells the requester to add a second owner or an approver group. Break-glass never approves. |
| MEM-009 | **Access request** (`par_`): `product`, `requester`, `reason` (10..500), `requested_role` (`viewer` \| `operator`, default `operator`, AUTH-028; `admin` only by owner action), `status` `requested` → `approved` \| `denied` \| `cancelled`, `decided_by`, `decision_note`, `granted_role` (approver may lower, never raise above `operator`). One open request per (user, product) (409 `request_exists`). Approval creates a direct membership binding (MEM-001). Requests older than 30 days auto-cancel (sweeper). Audited `product_access.request/approve/deny/cancel`. |
| MEM-010 | **EA grant** = the M8 access grant for prod-tier environments (prod, staging), scope (target, namespace, product) (PRD-020). Requesting needs product role ≥ `operator` (else 403 `not_a_member`). It can be requested from anywhere for the active product (`POST /access-grants {product, environment or target+namespace, ...}`); approvers = MEM-007/008. |
| MEM-011 | `/me` adds `memberships: [{product, role, via: user\|group\|owner}]`, `owner_of: [product]`, `pending_requests: {access: [AccessRequest], grants: [AccessGrant]}` (the caller's own open ones) and `approval_queue_count`. `approver_for_products` = products where the caller is in the approver set (owner or group). Amends PRD-023. |
| MEM-012 | Notifications: the GRT-040 webhook also sends `product_access.requested`, `.approved`, `.denied`; payloads carry the product, its owners' emails and the approver group so a bridge can route to the owners. In-app: approver queue badge (MEM-011). |
| MEM-013 | Membership changes: `PUT /products/{p}/members/{user}` (role) and `DELETE` by owners/product admins (MEM-004); removing a member revokes their requested/approved/active EA grants on that product (audit `access_grant.revoke` with reason `membership_removed`). |

### API (M17c)

| Method | Path | Purpose | Who |
|---|---|---|---|
| GET | `/api/v1/products` | Member products in full + directory entries (`member: false`) for the rest | any |
| GET | `/api/v1/products/{p}/members` | Direct + group-derived members | product viewer |
| PUT, DELETE | `/api/v1/products/{p}/members/{user}` | Set role / remove direct membership | owner / product admin (MEM-004) |
| GET, POST | `/api/v1/products/{p}/owners` | List / add owner `{user}` | product viewer / owner, global_admin |
| DELETE | `/api/v1/products/{p}/owners/{user}` | Remove owner (409 `last_owner`) | owner, global_admin |
| POST | `/api/v1/products/{p}/access-requests` | Request membership `{reason, requested_role?}` | any non-member |
| GET | `/api/v1/products/{p}/access-requests` | Open/decided requests | approver set, global_admin |
| GET | `/api/v1/access-requests/mine` | Caller's requests | any |
| POST | `/api/v1/access-requests/{id}/approve` | `{granted_role?, note?}` | approver set ≠ requester (MEM-008 fallback: global_admin) |
| POST | `/api/v1/access-requests/{id}/deny` | `{note}` | same |
| POST | `/api/v1/access-requests/{id}/cancel` | | requester |
| GET | `/api/v1/users?q=` | Find users who have logged in (id, display name, email) to add as member/owner | owners, product admins, global_admin |

Directory entries never expose counts, applications or environments.

## Active product context (M18)

| ID | Rule |
|---|---|
| CTX-001 | The UI always has one **active product**, carried in the URL: every product-scoped screen lives under `/p/{product}/…` (applications, environments, deployments, grants, findings, migration). The navbar product switcher changes the URL. |
| CTX-002 | Redirects: after login, or on any route without a product: if the URL names a product the user is a member of → go; else last-used product (per-viewer `localStorage`) if still a member; else the only membership if exactly one; else `/products` ("Choose a product"). A non-member opening `/p/{x}/…` sees the directory entry and "Request access" (no data). |
| CTX-003 | Cross-product views (`/all/stacks`, `/all/grants`, `/all/findings`, `/all/audit`) exist only for `global_admin`. |
| CTX-004 | API: product-scoped paths where they exist (`/products/{p}/…`); existing list endpoints (`/inventory`, `/access-grants`, `/audit`, `/findings`, `/discovery/workloads`) accept `?product=` and the SPA always sends it. No `X-Product` header and no server-side "current product" state: authorization never depends on the selected product, only on membership (MEM-002/003). |
| CTX-005 | The navbar shows a persistent **"Request prod access"** (EA) action for the active product when the user is a member and the product has prod-tier environments; it opens the grant form with the product's prod-tier environments to choose from. |

## Local multi-user testing (M17d, dev only)

| ID | Rule |
|---|---|
| DEV-001 | `cmd/devidp` (build tag `devidp`; the production Dockerfile builds only `./cmd/control8` without that tag, and CI asserts the image has no `devidp` binary) runs a standalone OIDC provider on `127.0.0.1` using the vendored `mockoidc`. |
| DEV-002 | Refuses to start unless `--i-know-this-is-insecure` or `C8_DEVIDP_INSECURE=1` is set, and refuses to bind a non-loopback address. Prints a red "DEV ONLY" banner. |
| DEV-003 | Users and groups from a JSON file (`dev/devidp.json`, default shipped): `alice` (groups `platform-admins`), `bob` (`developers`), `carol` (`payments-approvers`), `dave` (none). Its `/login` page lists the users as buttons; choosing one queues it in mockoidc and continues the auth-code flow. `GET /dev/login-as?user=<name>` does the same non-interactively for scripts. |
| DEV-004 | `make dev-idp` starts devidp; `make dev` starts devidp + Control 8 (`go run`, no Docker) with `C8_OIDC_ISSUER` pointing at it, `C8_BOOTSTRAP_ADMIN_GROUP=platform-admins`, a break-glass password `dev` (hash generated at start), data in `./tmp/dev`, and `C8_TARGETS` = the kind cluster if `./tmp/kubeconfig` exists, else `[]`. `scripts/dev-seed.sh` logs in each user via `login-as` (curl cookie jar) and, as alice, creates product `shop` (alice owner), as alice creates `payments` and adds carol as owner (alice then removes herself), so the preset story holds. |
| DEV-005 | `docs/ops/local-dev.md`: three browser profiles (alice, bob, carol) walkthrough — bob requests access to `shop`, alice approves; bob deploys to dev; bob requests EA for staging, alice approves; carol cannot approve `shop`; dave sees only the directory. |
| DEV-006 | `scripts/e2e-multiuser.sh` runs the same story via the API with `login-as` and asserts each step (used in CI with the unit job; no cluster needed except for the deploy steps, which are skipped when no kind kubeconfig exists). |

## Events (M17)

| Event | Payload | Delivered to |
|---|---|---|
| `product.updated` | `Product` | Anyone who can see the product |
| `product.removed` | `{slug}` | Same |
| `application.updated` | `Application` | Viewers of the product |
| `environment.updated` | `Environment` | Viewers of the product |
| `product_access_request.updated` | `AccessRequest` | M17c: requester, the product's approver set, `global_admin` |
| `product_membership.updated` | `{product, user, role \| null}` | M17c: that user, owners/admins of the product |

`stack.*`, `deployment.*`, `revision.*` events are additionally delivered to holders of a product role on the stack's product (PRD-011).

## UI (M18)

See 06 "Products". Breadcrumbs everywhere: **Product › Application › Stack** (stack shown as `namespace @ target`).
