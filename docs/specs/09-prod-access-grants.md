# 09 Prod access grants

Non-prod: a normal namespace role is enough to deploy. Prod: nobody deploys by default; a user needs a **temporary, time-boxed access grant** approved by **one member of the owning product's approver group** (`namespace.approver_group`, elevated approval, D-40).

## Environment tiers

| ID | Rule |
|---|---|
| GRT-001 | `namespace.environment_tier` and `target.environment_tier` ∈ `prod` \| `non-prod` (enum, extensible later). Required on create; no default in the API. |
| GRT-002 | A stack's effective tier = `prod` if its namespace **or** its target is `prod` (D-42). Returned as `environment_tier` on stacks and deployments. |
| GRT-003 | Changing a tier is `global_admin` only and audited (`namespace.tier_change`, `target.tier_change`). Moving to `non-prod` while grants are active does not revoke them. |

## What needs a grant

On a prod stack, these actions need an **active grant** for the acting user whose scope matches the stack's (target, namespace):

| Action | Endpoint |
|---|---|
| Request apply / teardown | `POST .../deployments`, `DELETE .../stacks/{stack}` |
| Manual image update | `POST .../image-updates` |
| Approve a protected-DB deployment (4-eyes) | `POST /deployments/{id}/approve` (approver also needs a grant) |

Not gated: reads, creating stacks/revisions (no cluster write), automatic rollback of a deployment already running, cancel/reject. Digest poll on prod only reports `update_available` (D-46).

| ID | Rule |
|---|---|
| GRT-010 | The role check still applies first (operator for deploy). A grant adds prod permission; it never adds a role. |
| GRT-011 | Missing/expired grant → 403 `grant_required` with `scope` in the problem body so the UI can offer "Request access". |
| GRT-012 | The grant is checked again when the worker moves the deployment to `running` (D-44). Expired or revoked by then → deployment `failed`, `error=grant_expired`, nothing applied. A deployment waiting in `pending_approval` uses the **requester's** grant at run time. |
| GRT-013 | Expiry or revoke **during** a running deployment does not interrupt it: the rollout completes, or rolls back per 08. No new apply starts on that grant. |
| GRT-014 | Every gated action's audit event carries `details.grant_id` (or `details.grant_bypass="break_glass"`). |
| GRT-015 | API tokens act with their owner's grants (D-45). |
| GRT-016 | Break-glass sessions skip the grant check (D-48); every such action is audited with `grant_bypass=break_glass` and the break-glass banner (AUTH-014) shows. |

## access_grant

| Field | Notes |
|---|---|
| `id` (`agr_`) | |
| `requester_id` | user id |
| `target_id`, `namespace_id` | Single scope. Target must exist; namespace must exist; at least one is prod |
| `reason` | required, 10..500 chars |
| `ticket_ref` | optional change ticket ref (free text, ≤ 100) |
| `duration_minutes` | 15 .. `C8_GRANT_MAX` (default 4h; env-configurable up to `C8_GRANT_MAX_CAP`, default 24h) |
| `starts_at` | requested start; default = approval time; ≤ 7 days ahead |
| `expires_at` | set on approval = max(`starts_at`, approval time) + duration |
| `status` | below |
| `approver_id`, `decided_at`, `decision_note` | Member of the namespace's `approver_group`; note required on deny |
| `approver_group` | Snapshot of `namespace.approver_group` at request time (approval checks against the current value) |
| `revoked_by`, `revoked_at`, `revoke_reason` | |

### States

```
requested --approve--> approved --starts_at reached--> active --expires_at--> expired
    |                     |                              |
    +--deny--> denied     +--revoke--> revoked           +--revoke--> revoked
requested --cancel (requester)--> revoked
requested --not decided in 24 h--> expired
```

`approved` exists only for a future `starts_at`; an immediate grant goes straight to `active`.

| ID | Rule |
|---|---|
| GRT-020 | Approver must be a member (groups at last login) of the grant namespace's **current** `approver_group` **and** must not be the requester. One approval is enough (two-approver is a future option). Break-glass and tokens cannot approve. |
| GRT-027 | A grant request for a namespace without `approver_group` → 409 `approver_group_missing`. A namespace with tier `prod` must have `approver_group` (400 on create/update without it). Only `global_admin` sets it (D-50). |
| GRT-021 | A user may hold at most one `requested`/`approved`/`active` grant per scope (409 `grant_exists`). |
| GRT-022 | The requester must hold at least `operator` in the namespace at request time (403 otherwise). |
| GRT-023 | Status transitions on time are computed on read (`now` vs `starts_at`/`expires_at`) and persisted by a 1-minute sweeper that writes `access_grant.activate` / `access_grant.expire` audit events (`actor_type=system`). |
| GRT-024 | Revoke: the requester (own grant), a member of the namespace's `approver_group`, or `global_admin`. Reason required. |
| GRT-025 | Approver may shorten, never lengthen, the duration on approval. |
| GRT-026 | Grants are never deleted (audit trail). |

## API

| Method | Path | Purpose | Who |
|---|---|---|---|
| POST | `/api/v1/access-grants` | Request `{target, namespace, reason, ticket_ref?, duration_minutes, starts_at?}` → 201 | operator in ns |
| GET | `/api/v1/access-grants` | List, filters `?status=&namespace=&target=&requester=` | global_admin: all; approver-group members: their namespaces; others: own |
| GET | `/api/v1/access-grants/mine` | Caller's grants, newest first | any |
| GET | `/api/v1/access-grants/{id}` | One grant | requester, namespace approver-group member, global_admin |
| POST | `/api/v1/access-grants/{id}/approve` | `{duration_minutes?, note?}` | namespace approver-group member ≠ requester |
| POST | `/api/v1/access-grants/{id}/deny` | `{note}` | namespace approver-group member ≠ requester |
| POST | `/api/v1/access-grants/{id}/revoke` | `{reason}` (also cancels a `requested` one) | GRT-024 |

Every call writes one audit event (`access_grant.request|approve|deny|revoke`), target `access_grant`, namespace set.

## Notifications (webhook)

| ID | Rule |
|---|---|
| GRT-040 | If `C8_WEBHOOK_URL` is set, Control 8 POSTs JSON `{event, at, grant}` for `access_grant.requested`, `.approved`, `.denied`, `.revoked`, `.expired`. `grant` = the API shape. |
| GRT-041 | Header `X-C8-Signature: sha256=<hex HMAC of body>` with key from `C8_WEBHOOK_SECRET` (Secret env). Timeout 5 s, 3 attempts (1 s, 5 s backoff), then logged + audit `webhook.failed`. Never blocks the API call. |
| GRT-042 | No other notification channels in v1. |

## Enforcement boundary

| ID | Rule |
|---|---|
| GRT-050 | Control 8 is the only holder of write credentials for prod targets. Prod kubeconfigs live only in Control 8's own Kubernetes Secret (injected as env, D-49), are never stored in the DB and never returned by any API; restrict `get` on that Secret to cluster admins. Users have no direct prod push path **through Control 8** other than a grant. |
| GRT-051 | Residual bypass: anyone holding their own kubeconfig with write rights to a prod cluster can skip Control 8. Control 8 cannot prevent that; it surfaces it as drift (`label_mismatch`/`image_mismatch`) and `ownership_conflict` on the next apply. |
| GRT-060 | Recommended hardening (operator, documented in `docs/ops/prod-rbac.md`, not built): give humans only the built-in `view` ClusterRole in prod clusters; bind write verbs only to Control 8's service account / kubeconfig user; optionally a `ValidatingAdmissionPolicy` that denies writes to `control-8/managed=true` objects by any user other than Control 8's. |
