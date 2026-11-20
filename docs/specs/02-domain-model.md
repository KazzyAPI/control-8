# 02 Domain model

Conventions: IDs are `<prefix>_<16 base32 lowercase>` (D-24). Times are RFC 3339 UTC strings in JSON, `TEXT` in SQLite. `slug` = `^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$` (valid as a Kubernetes namespace name). JSON columns are `TEXT` holding JSON. Every table has `created_at`; mutable tables have `updated_at`. Fields marked *(docker)* are used only by the later Docker track.

## Entities

### target (`tgt_`)
| Field | Type | Notes |
|---|---|---|
| id, name | text | `name` slug, unique; rows synced from `C8_TARGETS` at startup (D-49) |
| kind | enum | `kubernetes` \| `docker` |
| environment_tier | enum | `prod` \| `non-prod` (GRT-001) |
| kube_auth | enum | `in_cluster` \| `kubeconfig`; kubernetes only |
| kubeconfig_env, kubeconfig_path | text | Reference to the Secret-injected env var or mounted file (never content); `kubeconfig` only |
| kube_context | text | optional |
| docker_endpoint | text | *(docker)* `unix:///var/run/docker.sock` |
| parachute_host | text | *(docker)* name Parachute uses (`local`) |
| reachable | bool | Last reachability check (K8S-003) |
| sync | enum | `syncing` \| `watching` \| `polling` \| `down` (K8S-064) |
| last_error | text | Redacted error from last check |
| last_seen_at | time | |

### namespace (`ns_`)
| Field | Type | Notes |
|---|---|---|
| id, slug, display_name | text | `slug` unique, immutable; = Kubernetes namespace name (K8S-010) |
| environment_tier | enum | `prod` \| `non-prod` |
| approver_group | text | OIDC group of the owning product team whose members approve grants (09); required when tier is `prod`; set by `global_admin` only |
| description | text | optional |

### asset (`ast_`)
| Field | Type | Notes |
|---|---|---|
| id, namespace_id, slug | text | (`namespace_id`,`slug`) unique |
| name, description, owner | text | `owner` free text |
| image_repo | text | informational + policy hint |
| managed_by | enum | `control8` \| `argo` |
| external_url | text | Argo app URL when `argo`; else null |

### stack (`stk_`)
| Field | Type | Notes |
|---|---|---|
| id, namespace_id, asset_id, target_id, slug | text | (`namespace_id`,`slug`) unique |
| runtime | enum | `kubernetes` \| `docker`; = `target.kind`, immutable (K8S-004) |
| k8s_namespace | text | kubernetes: = namespace slug |
| compose_project | text | *(docker)* `c8-<ns>-<stack>`, unique, immutable |
| state | enum | see below |
| desired_revision | int | Revision last **successfully applied**; null before first apply |
| safety_profile | enum | Denormalized from desired revision; null in `draft` |
| rollout_frozen | bool | K8S-055; default false |
| drift | json | Last computed drift list |
| digest_status | json | `{checked_at, error, updates: [{workload, container, current, available}]}`; kubernetes `managed_rollout` only |
| parachute | json | *(docker)* stack-level Parachute slice |

Effective `environment_tier` (GRT-002) is computed, not stored.

### revision (`rev_`)
Immutable once created.
| Field | Type | Notes |
|---|---|---|
| id, stack_id | text | |
| number | int | 1..n per stack |
| safety_profile | enum | `managed_rollout` \| `as_is` (kubernetes); `parachute` \| `as_is` (docker). Required, no default |
| origin | enum | `user` \| `image_update` |
| base_revision | int | For `image_update`: revision it was derived from |
| manifests_input | text | kubernetes: user YAML as submitted |
| compose_input, compose_normalized | text, json | *(docker)* |
| rendered | json | Final objects (kubernetes) / compose JSON (docker) |
| inventory | json | kubernetes: `[{group, kind, name}]` (K8S-026) |
| rollout_timeout_seconds | int | 30..1800, default `C8_ROLLOUT_TIMEOUT` |
| digest_poll | bool | `managed_rollout` only; default false |
| env, secret_refs | json | *(docker)* compose env and host-file secret refs |
| backup_mode, service_options | enum, json | *(docker)* Parachute options (04) |
| database_services | json | Workload names (`Kind/name`) or compose services flagged database (auto-detect, 04 list) |
| policy_result | json | `{allowed, violations, requires_approval, reasons}` |
| created_by | text | user/token id, or `system` |

### deployment (`dep_`)
| Field | Type | Notes |
|---|---|---|
| id, stack_id, revision_id | text | `revision_id` null for teardown |
| action | enum | `apply` \| `teardown` |
| trigger | enum | `manual` \| `image_update` \| `digest_poll` |
| status | enum | see below |
| requested_by, approved_by | text | `approved_by` ≠ `requested_by` (4-eyes) |
| grant_id | text | Access grant used (prod), or null |
| grant_bypass | text | `break_glass` or null |
| reason | text | Required for teardown and image updates |
| rollout | json | kubernetes: `{state, result, timeout_seconds, workloads: [{kind, name, desired, updated, ready, available, reason}], rolled_back_to}` |
| started_at, finished_at | time | |
| output | text | Driver output, last 64 KiB, redacted |
| error | text | Short machine code + message |

### service (`svc_`) — observed, rewritten by sync
| Field | Type | Notes |
|---|---|---|
| id, stack_id, name | text | (`stack_id`,`name`) unique. Kubernetes: `Kind/name` |
| kind | text | Kubernetes kind; null for docker |
| desired_replicas, ready_replicas | int | |
| image_ref | text | kubernetes: first container of pod template; docker: container ref |
| image_id | text | Digest from a ready pod's `imageID` / Engine image ID |
| state | enum | `running` \| `progressing` \| `degraded` \| `missing` (kubernetes); `running` \| `restarting` \| `exited` \| `missing` (docker) |
| health | enum | `healthy` \| `unhealthy` \| `starting` \| `none` |
| is_database | bool | From revision |
| containers | json | kubernetes: `[{name, image}]` (for image-update choices) |
| pch_* | | *(docker)* Parachute fields, 04 |
| observed_at | time | |

### access_grant (`agr_`)
Fields, states and rules in 09. Never deleted.

### audit_event (integer `id` autoincrement)
| Field | Type | Notes |
|---|---|---|
| at | time | |
| actor_type | enum | `user` \| `token` \| `system` \| `parachute` |
| actor_id, actor_display | text | |
| auth_method | enum | `oidc` \| `break_glass` \| `token` \| null |
| namespace_id | text | null for global actions |
| action | text | dotted verb, e.g. `stack.create`, `deployment.approve`, `access_grant.approve`, `rollout.rollback` |
| target_type, target_id | text | Audit subject (not a deploy target) |
| outcome | enum | `success` \| `denied` \| `failed` |
| details | json | Redacted context; prod-gated actions include `grant_id` or `grant_bypass` (GRT-014) |
| request_id | text | |

### policy (`pol_`)
| Field | Type | Notes |
|---|---|---|
| id | text | |
| scope | enum | `global` \| `namespace` |
| namespace_id | text | null for global; unique per namespace |
| rules | json | See 05 |
| updated_by | text | |

### user (`usr_`), session, role_binding (`rb_`), token (`tok_`)
| Entity | Fields |
|---|---|
| user | id, issuer, subject (unique together), email, display_name, groups (json, last login), is_break_glass, last_login_at |
| session | id_hash, user_id, csrf_token, auth_method, expires_at (8 h; break-glass 1 h), created_at |
| role_binding | id, idp_group, namespace_id (null = global), role (`viewer`\|`operator`\|`admin`\|`global_admin`) |
| token | id, name, hash, owner_sub, owner_user_id (resolved, null if unknown), namespace_id (null = all the owner's namespaces), role, expires_at, last_used_at, revoked_at. Synced from `C8_TOKENS` at startup; never written by the API (AUTH-033) |

### safety_profile (enum, not a table)
| Value | Runtime | UI label | On failure | Image changes |
|---|---|---|---|---|
| `managed_rollout` | kubernetes | **Managed rollout** | auto rollback (K8S-053..055) | image-updates endpoint, opt-in digest poll |
| `as_is` | both | **As-is (no safety net)** | stays failed | new revision only |
| `parachute` | docker (later) | **With Parachute** | Parachute rolls back (04) | Parachute only |

## States

### stack.state
| State | Entered when | Exits to |
|---|---|---|
| `draft` | Stack created, never applied | `active`, `removed` (delete draft) |
| `active` | Apply succeeded, or failed apply rolled back | `active`, `failed`, `removed` |
| `failed` | Last apply failed without successful rollback | `active`, `removed` |
| `removed` | Teardown ok or draft deleted | terminal (row kept) |

### deployment.status
```
pending_approval --approve--> queued --worker (grant re-check)--> running --> succeeded | failed
       |                        |
       +--reject--> rejected    +--precheck/grant fail--> failed (error=grant_expired|ownership_conflict|...)
pending_approval / queued --cancel--> cancelled
```
Created as `pending_approval` when `policy_result.requires_approval` (or teardown under protected DB policy), else `queued`. `rollout.result` on Kubernetes: `healthy` \| `failed` \| `rolled_back` \| `rollback_failed`; a rolled-back deployment has `status=failed`.

## Invariants

| ID | Invariant |
|---|---|
| DOM-001 | At most one deployment per stack in `queued` or `running`. Otherwise 409 `deployment_in_progress`. |
| DOM-002 | Revisions are immutable; a change creates `number = max+1`. |
| DOM-003 | A revision with `policy_result.allowed=false` can never be deployed. |
| DOM-004 | `approved_by` ≠ `requested_by`; break-glass may approve only when it is not also the requester. |
| DOM-005 | `asset.managed_by=argo` ⇒ no stack rows reference it. |
| DOM-006 | `namespace.slug`, `stack.runtime`, `stack.target_id`, `stack.compose_project` never change. |
| DOM-007 | `service` rows are derived; only sync writes them. |
| DOM-008 | `audit_event` has no UPDATE or DELETE path in code; a SQLite trigger raises on UPDATE/DELETE. |
| DOM-009 | Every state-changing API call writes exactly one audit event with outcome, including `denied`. |
| DOM-010 | Secret values, kubeconfig contents and token plaintexts are never stored or logged. |
| DOM-011 | Deleting a namespace (409) or removing a target from config (exit 2) requires no non-`removed` stacks. |
| DOM-012 | `stack.safety_profile` = profile of revision `desired_revision`; profile is valid for `stack.runtime`. |
| DOM-013 | A deployment on a prod stack has `grant_id` or `grant_bypass` set; the grant's requester = the acting user (token owner for tokens) and its scope = stack's (target, namespace). |
| DOM-014 | `access_grant.approver_id` ≠ `requester_id`, and the approver was a member of the namespace's `approver_group` at decision time. |
