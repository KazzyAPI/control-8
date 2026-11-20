# 05 Auth and policy

No custom identity product: OIDC for humans, scoped tokens for agents, one break-glass login for emergencies. Prod deploys additionally need a time-boxed access grant with elevated approval (09).

## Principals

| Principal | How | `auth_method` |
|---|---|---|
| Human | OIDC auth-code + PKCE → server session cookie | `oidc` |
| Emergency admin | Local break-glass login → session cookie | `break_glass` |
| Agent / script | `Authorization: Bearer c8_...` | `token` |

## OIDC (v1: one provider; Authentik is the reference)

| ID | Rule |
|---|---|
| AUTH-001 | Discovery from `C8_OIDC_ISSUER`; scopes `openid profile email groups`; redirect `C8_PUBLIC_URL/auth/callback`. |
| AUTH-002 | Validate `state`, `nonce`, PKCE verifier, ID token signature/iss/aud/exp (go-oidc verifier). |
| AUTH-003 | User keyed by (`iss`,`sub`); upsert email, name, and groups from claim `C8_OIDC_GROUPS_CLAIM` (string array; missing claim = no groups). |
| AUTH-004 | Roles and approver-group membership are recomputed from groups at every login; stored groups are a cache. |
| AUTH-005 | Session cookie `c8_session`: 32 random bytes, `HttpOnly`, `Secure` (unless `C8_PUBLIC_URL` is http://localhost), `SameSite=Lax`, 8 h absolute expiry; DB stores SHA-256 only. Logout deletes the row. |
| AUTH-006 | A user with no matching role binding can log in but sees nothing and gets a "no access — ask an admin to map your group" page. |
| AUTH-007 | **Authentik** is the first/reference provider: the M3 manual test runs against Authentik and `docs/ops/authentik.md` documents the setup (OAuth2/OpenID provider, confidential client, redirect URI, `groups` scope mapping, one product approver group, e.g. `shop-approvers`). Automated tests use `mockoidc`. Other IdPs are expected to work but are not documented in v1. |

## Break-glass

| ID | Rule |
|---|---|
| AUTH-010 | Enabled only when `C8_BREAKGLASS_HASH` (Secret env, or `_FILE`) holds an argon2id PHC string. Username fixed: `breakglass`. |
| AUTH-011 | Grants `global_admin`. Session expiry 1 h. Skips the prod grant check (GRT-016), cannot approve access grants (GRT-020). |
| AUTH-012 | Every login attempt (success or fail) is audited (`auth.break_glass`). Every action of a break-glass session is audited with `auth_method=break_glass`; prod actions add `grant_bypass=break_glass`. |
| AUTH-013 | 5 failed attempts per 15 min per source IP → 429 for the rest of the window. |
| AUTH-014 | While any break-glass session exists, every page shows a red "Break-glass session active" banner to all admins; the grant webhook (if set) receives `break_glass.login`. |
| AUTH-015 | `control8 hash-password` subcommand reads a password from stdin and prints the PHC string. |

## Roles and group mapping

| Role | Can |
|---|---|
| `viewer` | Read namespace: assets, stacks, revisions, deployments, services, namespace audit |
| `operator` | viewer + register/update assets, create stacks/revisions, request deployments, teardowns and image updates (prod: with a grant), request access grants, cancel own requests |
| `admin` | operator + approve/reject deployments (not own; prod: with a grant), clear `rollout_frozen` by deploying, edit namespace policy, delete assets |
| `global_admin` | admin on all namespaces + namespaces CRUD, tiers, `approver_group`, global policy, role bindings, all audit (targets and tokens are config, D-49/D-11) |
| *approver-group member* | Not a role: member of `namespace.approver_group` (the owning product team's OIDC group). Can see that namespace's grants and approve/deny/revoke others' grants for it (09). Gives no deploy right by itself |

| ID | Rule |
|---|---|
| AUTH-020 | `role_binding(idp_group, namespace|null, role)`. Effective role per namespace = highest of all bindings matching any of the user's groups; `namespace=null` + `global_admin` = global admin; `null` namespace with another role applies to every namespace. |
| AUTH-021 | Bootstrap: `C8_BOOTSTRAP_ADMIN_GROUP` (optional) — on startup, if no `global_admin` binding exists, create one for that group. Otherwise use break-glass. |
| AUTH-022 | Role checks live in `authz.Require(principal, namespaceID, role)`; prod checks in `grants.Require(principal, stack)` (ARC-004). Handlers call them before any work. Denials are audited (`outcome=denied`). |
| AUTH-023 | Approver-group membership comes only from IdP groups (no role binding, no token). Approval actions require a session (tokens → 403 `token_not_allowed`). |

## API tokens (static, from config)

Tokens are not minted through the UI or API in v1 (D-11). They are defined in env `C8_TOKENS`, injected from a Kubernetes Secret via `envFrom`, and only hashes ever reach Control 8.

```json
[{"name": "ci-shop", "owner_sub": "8f2c1d0a-example-sub", "namespace": "shop", "role": "operator",
  "sha256": "9f2c...e1", "expires_at": "2027-06-30T00:00:00Z"}]
```

| ID | Rule |
|---|---|
| AUTH-030 | Plaintext format `c8_` + 32 random bytes base32 (no padding). `control8 gen-token` prints a new plaintext and its SHA-256; the plaintext goes to the client's own Secret, the hash into Control 8's `C8_TOKENS`. |
| AUTH-031 | Entry fields: `name` (unique), `owner_sub`, `namespace` (null = every namespace the owner has), `role`, `sha256`, `expires_at` (required, ≤ 365 d from startup). Invalid JSON, duplicate names/hashes or missing fields → exit 2 naming the entry. |
| AUTH-032 | Owner = the user whose OIDC `sub` equals `owner_sub` (exact match; stable, unlike email) and who has logged in at least once; none or more than one → token rejected (401) and a startup warning. Effective permission = min(token role, owner's current role); prod actions need the owner's active grant (GRT-015). |
| AUTH-033 | On startup, `C8_TOKENS` is synced into the `token` table (hash + metadata only); entries no longer present are marked revoked. Revoke = remove the entry from the Secret and roll out Control 8. Audit `token.config_sync` (system) lists added/removed names. |
| AUTH-034 | Tokens cannot request/approve/deny/revoke grants or change role bindings (403 `token_not_allowed`). `last_used_at` updated at most once per minute. Expired/revoked → 401. |
| AUTH-035 | Tokens are only accepted in the `Authorization` header. |

Shortcut accepted: owner's role and approver groups are taken from groups cached at last login, so a group removed in the IdP only takes effect at next login or token expiry.

Later (not v1): token minting/revocation via API/UI.

## Event stream authorization

| ID | Rule |
|---|---|
| AUTH-040 | `GET /api/v1/events` accepts the session cookie or a bearer token; same principal resolution as REST. |
| AUTH-041 | Each event is delivered only if the principal could `GET` the resource now (10 filter table): namespace role ≥ viewer; grants to requester, namespace `approver_for` members and global_admin; global audit to global_admin. No event bypasses `authz.Require`. |
| AUTH-042 | Roles are re-read on each event (cheap, cached per principal for 10 s), so a role binding removal stops delivery within 10 s; session/token invalidation closes the stream at the next heartbeat (EVT-014). |
| AUTH-043 | Payloads never contain secrets, token hashes, kubeconfig references beyond what REST returns, or deployment output. |

## Policy

Policies exist at `global` scope (one row, seeded with defaults) and per `namespace` (optional). Evaluation is a pure function `policy.Evaluate(effectiveRules, revisionInput) -> PolicyResult`, unit-tested with table tests. The prod grant gate is **not** a policy rule; it is checked per action (09).

### Rules document

| Key | Type | Default (global seed) | Merge (global + namespace) |
|---|---|---|---|
| `allowed_registries` | []string prefix | `[]` (= any) | Namespace entries must be within global ones (else PUT rejected); effective = namespace list if set, else global |
| `allowed_k8s_namespaces` | []string glob (`*` suffix only) | `[]` (= any, minus the fixed denylist) | intersection |
| `required_labels` | []string keys | `[]` | union |
| `protected_db_approval` | bool | `true` | OR |
| `allowed_safety_profiles` | []profile | `["managed_rollout","as_is","parachute"]` | intersection |
| `allowed_bind_prefixes` | []string abs paths | `[]` | intersection (namespace within global). Governs Kubernetes `hostPath` and Docker bind mounts |
| `allow_privileged` | bool | `false` | AND |

### Rule evaluation (per revision)

| ID | Check | Runtime | Result on failure |
|---|---|---|---|
| POL-R1 | Every image (Kubernetes: containers, initContainers, ephemeral; Docker: services) starts with an `allowed_registries` prefix (no registry ⇒ `docker.io/library/` or `docker.io/<ns>/`) | both | `registry_not_allowed` |
| POL-R2 | Every workload (Kubernetes object `metadata.labels`; Docker service `labels`) has every `required_labels` key | both | `label_missing` |
| POL-R3 | `safety_profile` ∈ `allowed_safety_profiles` | both | `safety_profile_not_allowed` |
| POL-R4 | Kubernetes `hostPath` volumes / Docker bind mounts and secret host paths lie under an `allowed_bind_prefixes` entry (after clean, no `..`). Default empty ⇒ all forbidden | both | `host_path_not_allowed` |
| POL-R6 | No user label/annotation in Control 8's or Argo's prefixes (K8S-025; Docker: 04) | both | `label_forbidden` |
| POL-R7 | Kubernetes: `securityContext.privileged: true`, `allowPrivilegeEscalation: true`, `hostNetwork`/`hostPID`/`hostIPC: true`, `capabilities.add` containing `SYS_ADMIN`, `NET_ADMIN` or `ALL`, `hostPort`. Docker: `privileged`, `pid: host`, `network_mode: host`, `cap_add SYS_ADMIN|ALL`, docker.sock mount. All require `allow_privileged` | both | `privileged_not_allowed` |
| POL-R9 | `protected_db_approval` and the revision has `database_services` and (first apply, or any database workload's rendered spec differs from the deployed revision, or profile changes). Applies to image updates too | both | `requires_approval=true`, reason `protected_database` |
| POL-R10 | Teardown of a stack with `database_services` under `protected_db_approval` | both | deployment `pending_approval` |
| POL-R12 | Every kind in K8S-021 allow-list; no cluster-scoped objects | kubernetes | `kind_not_allowed` |
| POL-R13 | Mapped Kubernetes namespace matches `allowed_k8s_namespaces` (if non-empty) and is not `kube-*` or `default` (always); manifest `metadata.namespace` empty or equal (K8S-022) | kubernetes | `namespace_not_allowed` |
| POL-R14 | No `Secret` objects (K8S-023) | kubernetes | `kind_not_allowed` |
| POL-R5, R8, R11 | *(Docker track)* digest-pinned refs on `parachute`, Parachute configured, no `build:` (04) | docker | as in 04 |

Violations block (DOM-003). `requires_approval` creates the deployment as `pending_approval`; an `admin` other than the requester approves (DOM-004), with a grant on prod.

Policy changes are audited (`policy.update`) with old and new rules. Stored revisions are re-evaluated at deployment request time; a revision that no longer passes is refused (422 `policy_violation`).

### Two independent approvals

| Approval | Who | When | Spec |
|---|---|---|---|
| Elevated approval of an **access grant** | one member of the namespace's `approver_group` ≠ requester | Before any prod deploy; time-boxed | 09 |
| 4-eyes **deployment** approval (protected DB) | namespace `admin` ≠ requester (with a grant on prod) | Per deployment touching database workloads | POL-R9/R10 |
