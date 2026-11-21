# 10 Events (bus + SSE)

Everything in the UI is live. State changes publish to an in-process **event bus**; browsers and agents subscribe over **Server-Sent Events** at `GET /api/v1/events`. Events are notifications carrying the resource's current API shape; the REST API stays the source of truth, the audit table stays the durable log. Nothing about events is persisted in SQLite (D-55).

## Bus

| ID | Rule |
|---|---|
| EVT-001 | `internal/events.Bus`: `Publish(Event)` is non-blocking; each subscriber has a buffered channel (256). A subscriber whose buffer is full is disconnected (it reconnects and resumes, EVT-011). |
| EVT-002 | Event IDs are a process-wide monotonic `uint64`, assigned at publish. After a restart IDs start again from the Unix-ms start time × 1000, so they never go backwards across restarts. |
| EVT-003 | Replay ring buffer: last `C8_EVENTS_REPLAY` events (default 10 000). |
| EVT-004 | Publishers: every service method that changes state (after its DB commit, in the same call that writes the audit event), the executor (deployment/rollout progress), the informer pipeline (08), the grant sweeper, and the target checker. Publishing never fails the caller. |
| EVT-005 | High-frequency sources are coalesced: at most one `stack.updated` per stack, one `deployment.updated` per deployment and (M19) one `discovery.updated` per workload per second (latest wins). |

## SSE endpoint

`GET /api/v1/events?types=<csv>&namespace=<slug>` → `200 text/event-stream`. Both filters optional.

```
retry: 3000

id: 1732183200000123
event: deployment.updated
data: {"type":"deployment.updated","at":"2026-11-21T10:00:00Z","namespace":"shop","data":{...Deployment...}}

: ping
```

| ID | Rule |
|---|---|
| EVT-010 | Auth: session cookie (browsers; GET, so no CSRF) or `Authorization: Bearer c8_...`. Unauthenticated → 401 before the stream starts. |
| EVT-011 | `Last-Event-ID` header (or `?last_event_id=` for clients that cannot set headers) resumes: buffered events with a greater ID that the viewer may see are sent first. If the ID is older than the buffer or from a future/unknown epoch, the server sends one `reset` event; the client refetches everything it shows. |
| EVT-012 | Heartbeat: a `: ping` comment every `C8_EVENTS_HEARTBEAT` (default 15 s). Response headers `Cache-Control: no-store`, `X-Accel-Buffering: no`; each event is flushed. |
| EVT-013 | Limits: 5 concurrent streams per principal, `C8_EVENTS_MAX_CONNS` total (default 500); excess → 429. |
| EVT-014 | On every heartbeat the server re-checks the principal (session expired/logged out, token removed/expired) and closes the stream with a final `session.ended` event if it is no longer valid. |
| EVT-015 | Graceful shutdown closes all streams (clients reconnect to the new process; IDs from the old epoch → `reset`). |

## Per-viewer filtering

Evaluated per event per subscriber, against the principal's current roles (same functions as REST: `authz.Require`, grant visibility). A viewer never receives an event for a resource they could not `GET`.

| Event scope | Delivered to |
|---|---|
| Namespaced (`stack.*`, `revision.*`, `deployment.*`) | Principals with ≥ `viewer` in that namespace, or (M17) on the stack's product |
| `product.*`, `application.updated`, `environment.updated` (M17) | Principals who can see the product (product role, or a namespace role on any of its stacks) |
| `migration.*` (M17b) | Plan creator, admins of the plan's product, `global_admin` |
| `discovery.updated`, `discovery.finding` (M19/M20) | Viewers of the mapped namespace, product role holders of the attributed product, `global_admin`; unmapped/system: `global_admin` only (DSC-022/024) |
| `access_grant.updated` | The requester, members of the grant's resolved approver group (product's, or namespace's for `unassigned`), product admins (M17), `global_admin` |
| `audit.created` | Namespaced: ≥ `viewer` in that namespace. Global (null namespace): `global_admin` only |
| `target.status` | Any principal with a role in any namespace (as `GET /targets`) |
| `reset`, `session.ended` | That subscriber only |

## Event catalogue

Payload `data` is exactly the REST schema named (openapi `components/schemas`), so the SPA can write it straight into its query cache.

| Type | Payload | Published when |
|---|---|---|
| `stack.updated` | `Stack` (incl. services, drift, health, digest_status) | Create, revision deployed, informer-observed change, health/drift change, freeze/unfreeze |
| `stack.removed` | `{namespace, slug}` | Draft deleted or teardown succeeded |
| `revision.created` | `Revision` (without `rendered`) | Revision created (user or image update) |
| `deployment.updated` | `Deployment` (incl. `rollout` progress, without `output`) | Every status change; rollout progress (coalesced) |
| `access_grant.updated` | `AccessGrant` | Request, approve, deny, revoke, activate, expire |
| `audit.created` | `AuditEvent` | Every audit write |
| `target.status` | `Target` | Reachability, informer sync state or server version change |
| `product.updated` / `product.removed` | `Product` / `{slug}` | M17: product create/update/delete |
| `application.updated` | `Asset` (application) | M17: create/update/reassign/delete (`deleted: true`) |
| `environment.updated` | `Environment` | M17: create/update/delete |
| `migration.plan.created` | `MigrationPlan` (summary) | M17b: plan stored |
| `migration.applied` / `migration.undone` | `MigrationResult` | M17b: apply / undo |
| `discovery.updated` | `DiscoveredWorkload` (`gone: true` on delete) | M19: read-model change, coalesced ≤ 1/s per workload |
| `discovery.finding` | `Finding` | M20: opened, resolved, reopened, acknowledged, ack expired |
| `reset` | `{reason}` | Resume impossible (EVT-011) |
| `session.ended` | `{reason}` | EVT-014 |

Deployment output is not streamed in v1; the deployment page refetches `GET /deployments/{id}` on each `deployment.updated` for that id.

## Tests

`httptest` server + a raw SSE reader: ordering, `Last-Event-ID` resume, `reset` on gap, heartbeat, per-viewer filtering matrix (role × event type), slow-consumer disconnect, session expiry close, connection limits. Bus unit tests with `-race`.
