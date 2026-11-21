# 03 API

Contract file: [openapi.yaml](openapi.yaml) (OpenAPI 3.1). Handlers and the file MUST agree; a test walks the router and fails on any route missing from the file or vice versa (API-002). Operations of later milestones carry `x-milestone` and are skipped by the test until implemented.

## Conventions

| ID | Rule |
|---|---|
| API-001 | Base path `/api/v1`. JSON request/response, UTF-8. Request bodies max 1 MiB (413). Unknown JSON fields → 400. |
| API-002 | Route/openapi parity test (see above). |
| API-003 | Errors: `application/problem+json` `{type, title, status, detail, code}`; `code` is stable snake_case (e.g. `policy_violation`, `grant_required`, `grant_exists`, `approver_group_missing`, `ownership_conflict`, `argo_managed`, `deployment_in_progress`, `target_unreachable`, `image_update_not_allowed`, `rollout_frozen`, `forbidden`). |
| API-004 | Auth: `Authorization: Bearer c8_...` **or** session cookie `c8_session`. Cookie requests with a non-GET method MUST send `X-CSRF-Token` matching the session (403 otherwise). |
| API-005 | Authorization failure: 404 if the caller cannot view the namespace (no existence leak), 403 if they can view but lack the role. Missing prod grant: 403 `grant_required` with `scope: {target, namespace}`. |
| API-006 | Lists: `?limit=` (default 50, max 200) and opaque `?cursor=`; response `{items: [...], next_cursor: string|null}`. |
| API-007 | Every response carries `X-Request-Id`; audit events store it. |
| API-008 | Namespaced resources are addressed by slugs: `/namespaces/{ns}/stacks/{stack}`. Targets by name. Deployments and grants by id. |
| API-009 | Every resource with a safety profile returns `safety_profile` top-level (never nested, never omitted; `null` only for `draft` stacks). Stacks and deployments also return top-level `runtime` and `environment_tier`. |

## Roles

`viewer` < `operator` < `admin` (per namespace) and `global_admin` (implies `admin` everywhere). Grant approval comes from the namespace's `approver_group` (09). Tokens are static, defined in `C8_TOKENS` (05); they carry at most their owner's role and act with the owner's grants.

## Resources

Prod column: ✔ = needs an active grant on prod stacks (09).

| Method | Path | Purpose | Min role | Prod |
|---|---|---|---|---|
| GET | `/healthz` | Liveness (no auth) | none | |
| GET | `/readyz` | DB ok; targets reported, not required (no auth) | none | |
| GET | `/api/v1/events` | SSE stream of live events, filtered per viewer (10) | any | |
| GET | `/api/v1/me` | Principal, `csrf_token` (cookie sessions), roles per namespace, `approver_for` (namespaces whose approver group the user is in), active grants | any | |
| GET | `/api/v1/targets` | List targets (read-only; defined in `C8_TARGETS`) | viewer (any ns) | |
| GET | `/api/v1/targets/{target}` | One target | viewer (any ns) | |
| GET | `/api/v1/namespaces` | Visible namespaces | any | |
| POST | `/api/v1/namespaces` | Create (with `environment_tier`) | global_admin | |
| GET, PATCH | `/api/v1/namespaces/{ns}` | Read / update display_name, description (tier, `approver_group`: global_admin) | viewer / admin | |
| DELETE | `/api/v1/namespaces/{ns}` | Delete (DOM-011) | global_admin | |
| GET, POST | `/api/v1/namespaces/{ns}/assets` | List / register asset | viewer / operator | |
| GET, PATCH, DELETE | `/api/v1/namespaces/{ns}/assets/{asset}` | Read / update / delete (409 if stacks exist) | viewer / operator / admin | |
| GET, POST | `/api/v1/namespaces/{ns}/stacks` | List / create stack (draft) | viewer / operator | |
| GET | `/api/v1/namespaces/{ns}/stacks/{stack}` | Stack + services + drift + rollout/digest status | viewer | |
| DELETE | `/api/v1/namespaces/{ns}/stacks/{stack}` | Draft: delete. Else: request teardown (body `{reason}`) | operator | ✔ |
| GET, POST | `.../stacks/{stack}/revisions` | List / create revision (render + policy, never deploys) | viewer / operator | |
| GET | `.../stacks/{stack}/revisions/{n}` | Revision incl. rendered objects + policy result | viewer | |
| GET | `.../stacks/{stack}/revisions/{n}/diff?against={m}` | Unified diff of rendered YAML | viewer | |
| GET, POST | `.../stacks/{stack}/deployments` | List / request apply of a revision | viewer / operator | ✔ |
| POST | `.../stacks/{stack}/image-updates` | Managed-rollout image update → new revision + deployment (K8S-050) | operator | ✔ |
| GET | `/api/v1/deployments/{id}` | Status, rollout, output | viewer | |
| POST | `/api/v1/deployments/{id}/approve` | 4-eyes approve | admin (≠ requester) | ✔ |
| POST | `/api/v1/deployments/{id}/reject` | Reject `{reason}` | admin | |
| POST | `/api/v1/deployments/{id}/cancel` | Cancel if `pending_approval`/`queued` | requester or admin | |
| GET | `.../stacks/{stack}/services` | Observed workloads | viewer | |
| GET | `/api/v1/inventory` | All visible stacks with services | any | |
| POST | `/api/v1/access-grants` | Request grant | operator in ns | |
| GET | `/api/v1/access-grants` | List (global_admin: all; approver-group members: their namespaces; others: own) | any | |
| GET | `/api/v1/access-grants/mine` | Caller's grants | any | |
| GET | `/api/v1/access-grants/{id}` | One grant | requester / namespace approver-group member / global_admin | |
| POST | `/api/v1/access-grants/{id}/approve` | Elevated approval | namespace approver-group member ≠ requester | |
| POST | `/api/v1/access-grants/{id}/deny` | Deny `{note}` | namespace approver-group member ≠ requester | |
| POST | `/api/v1/access-grants/{id}/revoke` | Revoke / cancel `{reason}` | requester / namespace approver-group member / global_admin | |
| GET, PUT | `/api/v1/policies/global` | Global policy rules | global_admin (GET: any) | |
| GET, PUT | `/api/v1/namespaces/{ns}/policy` | Namespace policy rules | viewer / admin | |
| GET | `/api/v1/namespaces/{ns}/policy/effective` | Merged effective rules | viewer | |
| GET, POST | `/api/v1/role-bindings` | Group → role mappings | global_admin | |
| DELETE | `/api/v1/role-bindings/{id}` | | global_admin | |
| GET | `/api/v1/tokens` | Configured tokens, metadata only (own; global_admin: all). No create/revoke in v1 (later) | any | |
| GET | `/api/v1/audit` | Query `?namespace=&action=&target_id=&actor_id=&grant_id=&since=&until=` | viewer (own ns); global events: global_admin | |

Docker track (later, 04): `GET .../stacks/{stack}/parachute`, `POST .../parachute/approve`, `POST .../parachute/release`, `GET /parachute/status`, `POST /parachute/cycles`, `GET /parachute/cycles/{id}`. Shapes stay as in 04; marked `x-milestone: docker` in openapi.

Outside `/api/v1` and outside the OpenAPI file: `/healthz`, `/readyz`, the embedded SPA (`/`, assets, SPA fallback), and auth: `GET /auth/login`, `GET /auth/callback`, `POST /auth/logout`, `GET|POST /auth/break-glass`.

## Core shapes

### Targets
Read-only. Defined in `C8_TARGETS` (01, K8S-001); `GET /targets` returns name, kind, tier, `kube_auth`, the kubeconfig **reference** (`kubeconfig_env`/`kubeconfig_path`), reachability. Never the content.

### Create stack — `POST /namespaces/{ns}/stacks`
```json
{"slug": "shop-prod", "asset": "shop", "target": "prod-eu", "runtime": "kubernetes"}
```
`runtime` optional; defaults to the target's kind; mismatch → 400 `runtime_target_mismatch`. → 201 `Stack` with `state: "draft"`, `safety_profile: null`, `environment_tier` computed. 409 `argo_managed` if asset is Argo.

### Create revision — `POST .../stacks/{stack}/revisions`
```json
{
  "safety_profile": "managed_rollout",
  "manifests": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web}\nspec: ...\n---\napiVersion: v1\nkind: Service\nmetadata: {name: web}\nspec: ...\n",
  "rollout_timeout_seconds": 300,
  "digest_poll": true,
  "database_services": []
}
```
→ 201 `Revision`. Exactly one of `manifests` (kubernetes) or `compose` (docker) matching the stack runtime, else 400 `spec_runtime_mismatch`. `safety_profile` required (400 `safety_profile_required`) and valid for the runtime (400 `safety_profile_invalid_for_runtime`). Policy violations still create the revision with `policy_result.allowed=false`; unparseable input → 422 `invalid_manifests` / `invalid_compose`, no revision.

### Request deployment — `POST .../stacks/{stack}/deployments`
```json
{"revision": 3, "confirm_safety_profile": "managed_rollout", "confirm_environment_tier": "prod", "reason": "release 1.4"}
```
- 400 `safety_profile_mismatch` / `environment_tier_mismatch` if a confirm field ≠ actual (callers must state mode and tier).
- 403 `grant_required` (prod, no active grant).
- 422 `policy_violation`.
- 409 `deployment_in_progress`, `rollout_frozen` (non-admin on a frozen stack); 503 `target_unreachable`.
- 202 `Deployment` (`queued` or `pending_approval`); `Location: /api/v1/deployments/{id}`.

### Image update — `POST .../stacks/{stack}/image-updates`
```json
{"workload": "Deployment/web", "container": "web", "image": "ghcr.io/acme/shop:1.4@sha256:ab12...", "reason": "CVE-2026-1234 patch", "confirm_environment_tier": "prod"}
```
→ 202 `Deployment` (`trigger: image_update`) with the new revision number. 409 `image_update_not_allowed` (not kubernetes + `managed_rollout` + `active`), 409 `rollout_frozen`, 400 `image_repo_change`, 404 unknown workload/container, 403 `grant_required`.

### Request access grant — `POST /access-grants`
```json
{"target": "prod-eu", "namespace": "shop", "reason": "Release 1.4 of shop", "ticket_ref": "CHG-1042", "duration_minutes": 120}
```
→ 201 `AccessGrant` `status: requested`; 409 `approver_group_missing` if the namespace has none. Approve: `POST /access-grants/{id}/approve {"duration_minutes": 60, "note": "ok"}` → 200, `status: active`, `expires_at` set.

### Stack (response)
```json
{
  "id": "stk_...", "namespace": "shop", "slug": "shop-prod", "asset": "shop",
  "target": "prod-eu", "runtime": "kubernetes", "environment_tier": "prod", "k8s_namespace": "shop",
  "state": "active", "safety_profile": "managed_rollout", "desired_revision": 4,
  "health": "healthy", "rollout_frozen": false, "drift": [],
  "digest_status": {"checked_at": "...", "error": null, "updates": [{"workload": "Deployment/web", "container": "web", "current": "sha256:aa..", "available": "sha256:bb.."}]},
  "services": [{"name": "Deployment/web", "kind": "Deployment", "state": "running", "health": "healthy",
                "desired_replicas": 3, "ready_replicas": 3, "image_ref": "ghcr.io/acme/shop:1.4@sha256:aa..",
                "image_id": "ghcr.io/acme/shop@sha256:aa..", "is_database": false,
                "containers": [{"name": "web", "image": "ghcr.io/acme/shop:1.4@sha256:aa.."}]}]
}
```
`health`: `healthy`, `progressing` (deployment running), `degraded` (any workload not ready), `drifted`, `frozen` (`rollout_frozen`, or Parachute freeze on Docker), `unknown` (target unreachable). Precedence: unknown > frozen > progressing > degraded > drifted > healthy.

### Deployment (response)
```json
{"id": "dep_...", "stack": "shop-prod", "namespace": "shop", "action": "apply", "trigger": "image_update", "revision": 5,
 "runtime": "kubernetes", "safety_profile": "managed_rollout", "environment_tier": "prod",
 "status": "failed", "requested_by": "usr_...", "approved_by": null, "grant_id": "agr_...", "grant_bypass": null,
 "reason": "CVE patch", "started_at": "...", "finished_at": "...", "error": "rollout_failed: Deployment/web ProgressDeadlineExceeded",
 "rollout": {"state": "done", "result": "rolled_back", "timeout_seconds": 300, "rolled_back_to": 4,
             "workloads": [{"kind": "Deployment", "name": "web", "desired": 3, "updated": 1, "ready": 2, "available": 2, "reason": "ProgressDeadlineExceeded"}]},
 "output": "..."}
```

### Live events — `GET /api/v1/events`
`text/event-stream`; optional `?types=deployment.updated,stack.updated&namespace=shop`; resume with `Last-Event-ID`. Each event's `data` is `{type, at, namespace, data}` where `data` is the REST schema of the resource (`Stack`, `Deployment`, `AccessGrant`, `AuditEvent`, `Target`, `Revision`). Heartbeat `: ping` every 15 s; `reset` means "refetch everything". Full rules, catalogue and filtering: 10. Documented in openapi as `getEvents` with the `Event` schemas (OpenAPI cannot describe the stream framing; 10 is normative).

## Post-v1 routes (M17–M21)

Unbuilt routes carry `x-milestone: <M>` in openapi and are skipped by the API-002 parity test until their milestone (existing convention). Rules: 12 (products, environments, migration), 11 (discovery, findings, adoption).

| Method | Path | Purpose | Min role | Prod | M |
|---|---|---|---|---|---|
| GET, POST | `/api/v1/products` | List / create | any / global_admin | | M17 |
| GET, PATCH, DELETE | `/api/v1/products/{product}` | Read / update / delete | viewer / admin / global_admin | | M17 |
| GET, POST | `/api/v1/products/{product}/applications` | List / create application | viewer / operator | | M17 |
| GET, PATCH, DELETE | `/api/v1/products/{product}/applications/{app}` | Read / update (reassign: global_admin) / delete | viewer / operator / admin | | M17 |
| POST | `/api/v1/products/{product}/applications/{app}/stacks` | Place the app in an environment (creates a draft stack) | operator (product) + `allowed_products` | | M17 |
| GET, POST | `/api/v1/products/{product}/environments` | List / create environment | viewer / admin | | M17 |
| GET, PATCH, DELETE | `/api/v1/products/{product}/environments/{env}` | Read / update (PRD-046) / delete (409 if stacks) | viewer / admin / admin | | M17 |
| GET | `/api/v1/products/{product}/overview` | Applications × environments matrix (PRD-030) | viewer (product) | | M17 |
| GET | `/api/v1/products/{product}/members` | Members (direct + via group) | viewer (product) | | M17c |
| PUT, DELETE | `/api/v1/products/{product}/members/{user}` | Set role / remove direct member | owner / product admin | | M17c |
| GET, POST | `/api/v1/products/{product}/owners` | List / add owner | viewer / owner, global_admin | | M17c |
| DELETE | `/api/v1/products/{product}/owners/{user}` | Remove owner (409 `last_owner`) | owner, global_admin | | M17c |
| GET, POST | `/api/v1/products/{product}/access-requests` | List (approvers) / request membership (non-members) | approver set / any | | M17c |
| GET | `/api/v1/access-requests/mine` | Caller's membership requests | any | | M17c |
| POST | `/api/v1/access-requests/{id}/approve` | `{granted_role?, note?}` | approver set ≠ requester (fallback: global_admin) | | M17c |
| POST | `/api/v1/access-requests/{id}/deny` | `{note}` | same | | M17c |
| POST | `/api/v1/access-requests/{id}/cancel` | | requester | | M17c |
| GET | `/api/v1/users` | `?q=` user search (logged-in users only) | owner / product admin / global_admin | | M17c |
| POST | `/api/v1/namespaces/{ns}/stacks/{stack}/plans` | Dry run → stored deploy plan (13 PLN-001..007) | operator (product member) | per GRT-032 | M17e |
| GET | `/api/v1/deploy-plans/{id}` | Read plan | viewer | | M17e |
| POST | `/api/v1/migrations/dry-run` | Compute a migration plan, not stored | MIG-008 | | M17b |
| POST | `/api/v1/migrations/plans` | Compute + store plan | MIG-008 | | M17b |
| GET | `/api/v1/migrations/plans/{id}` | Read plan | creator / product admin / global_admin | | M17b |
| POST | `/api/v1/migrations/plans/{id}/apply` | Apply (one transaction; adoption items start deployments) | MIG-008 | ✔ (adoption items) | M17b |
| POST | `/api/v1/migrations/{id}/undo` | Reverse within the undo window | MIG-008 | | M17b |
| GET | `/api/v1/targets/{target}/discovery` | Discovery status, counts per class, truncation | viewer (any ns) | | M19 |
| GET | `/api/v1/targets/{target}/discovery/namespaces` | K8s namespaces: mapping, tier, system flag, class counts | viewer (any ns) | | M19 |
| PUT, DELETE | `/api/v1/targets/{target}/namespace-mappings/{k8s_ns}` | Set/remove explicit mapping `{namespace}` | global_admin | | M19 |
| GET | `/api/v1/discovery/workloads` | `?target=&namespace=&k8s_namespace=&class=&tier=&product=&kind=&q=&include_gone=` | per DSC-022/024 | | M19 |
| GET | `/api/v1/discovery/workloads/{id}` | One workload | per DSC-022/024 | | M19 |
| POST | `/api/v1/discovery/workloads/assign` | Bulk-assign to a product (DSC-024) | operator (product) + ns admin | | M19 |
| GET | `/api/v1/findings` | `?target=&namespace=&product=&type=&severity=&state=&acknowledged=` | per DSC-022/035 | | M20 |
| GET | `/api/v1/findings/{id}` | One finding | same | | M20 |
| POST | `/api/v1/findings/{id}/acknowledge` | `{reason, expires_at?}` | ns admin / product admin / global_admin | | M20 |
| POST | `/api/v1/findings/{id}/unacknowledge` | | same | | M20 |
| POST | `/api/v1/discovery/adoptions` | Adoption plan (no cluster write) | operator (product) | | M21 |
| GET | `/api/v1/discovery/adoptions/{id}` | Read plan | creator / product admin / global_admin | | M21 |
| POST | `/api/v1/discovery/adoptions/{id}/execute` | Start adopt deployment | operator (product) | ✔ | M21 |
| POST | `/api/v1/namespaces/{ns}/stacks/{stack}/release` | Stop managing, keep objects | operator | ✔ | M21 |

Changed shapes (M17e): `DeploymentCreate` adds `plan`; `Deployment` adds `plan`, `steps`, `steps_summary`; `RevisionCreate` adds `base_revision`, `copy_from`; revision/plan/step payloads are redacted (RED-*). Changed shapes (M17c): `Principal` adds `memberships`, `owner_of`, `pending_requests`, `approval_queue_count`; `Product` adds `member`, `my_role`, `owners` (directory entries carry only directory fields, MEM-002); `ProductCreate.approver_group` optional; `RoleBinding(Create)` adds `user` (direct membership); `AccessGrant` adds `fallback_required`; list endpoints accept `?product=` (CTX-004). Changed shapes (M17): `Stack`, `Deployment` add `product`, `application`, `environment` (nullable); `AccessGrantCreate` adds `product` (default `unassigned`); `AccessGrant` adds `product`, `environment`, `approver_group`; `RoleBinding(Create)` adds `product`; `Principal` adds `approver_for_products`; `PolicyRules` adds `allowed_products`; `Asset` adds `product`, `external_ref`, extended `managed_by`; `POST /namespaces/{ns}/stacks` accepts `product` (400 `asset_ambiguous` without it when ambiguous) and is limited to `unassigned` apps for new stacks (PRD-044). M19: `Target` adds `discovery`. M21: `Deployment.action` adds `adopt`, `release`; `Revision.origin` adds `adoption`.

New problem codes (M17e): `plan_stale`, `hidden_value_unresolved`, 429 on dry-run rate limit. `grant_required` bodies gain `approvers`, `pending_grant`, `request_url` and are returned **before** anything is created (GRT-030). New problem codes (M17c): `not_a_member`, `last_owner`, `request_exists`, `fallback_required`. Earlier: `approver_group_missing` (now per product), `product_not_allowed_in_namespace`, `asset_ambiguous`, `plan_stale`, `undo_conflict`, `not_adoptable`, `tool_managed`, `secret_not_adoptable`, `spec_change_detected`, `namespace_mapping_mismatch`, `discovery_unavailable`.
