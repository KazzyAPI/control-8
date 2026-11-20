# 04 Parachute integration

> **Status: later, optional (Docker track D1–D2, D-41).** Not in v1. Kept as the contract for when the Docker runtime returns. Applies only to `runtime=docker` stacks on target `local`. Sidecar image: `ghcr.io/kazzyapi/parachute:latest` — **TODO: pin a version/digest before release** (D-39). Prod access grants (09) apply to Docker stacks exactly as to Kubernetes stacks.

Aligned with the Parachute repo specs read on this machine (`~/Desktop/parachute/docs/specs/`): 01-selection (SEL-003/004/011/013), 05-dependencies-and-stacks (DEP-001/011), 09-update-policies, 11-http-api-and-metrics (API-001..012), 14-status-view (STS-002), 16-configuration (label reference). Parachute section IDs are cited in brackets.

**PCH-000** Control 8 talks to Parachute only over HTTP at `C8_PARACHUTE_URL` with `Authorization: Bearer <token>` and via container labels. No Go imports, no state-file reads.

## Facts about Parachute that shape this design

| Fact (Parachute spec) | Consequence for Control 8 |
|---|---|
| Endpoints `/v1/status` (GET), `/v1/approve` (POST), `/v1/update` (GET/POST) need `--http-api-status` / `--http-api-update`; disabled ones return 404 [API-001] | Sidecar env enables both; a 404 from a known path = misconfigured → health `parachute_misconfigured` |
| One Bearer token for everything; 401 + throttling (10 fails/60 s → 429) [API-002, API-010] | Never retry on 401; surface `parachute_auth_failed`; back off on 429 using `Retry-After` |
| No request bodies; any body > 1 KiB → 413 [API-009] | Send no body |
| `/v1/update` runs one cycle over **all** candidates, blocks until done, takes **no** query params (400), 409 `{running}` if busy [API-004/005] | Only global "run cycle now", async (D-17) |
| `/v1/approve?container=<name>` approves a held digest (202 `{host,container,digest}`) or releases a freeze (200 `{host,container,transactions}`); `?stack=<name>` only releases freezes (200); exactly one of them, else 400; 404 when nothing to act on; `host` defaults `local` [API-007] | Two Control 8 endpoints: approve (container) and release (stack) |
| Stack = `com.docker.compose.project` label [DEP-001] | Stack name in Parachute status == `stack.compose_project` |
| Opt-in with `--label-enable` + `parachute.enable=true` [SEL-004]; `--scope X` selects `parachute.scope=X` only and Parachute never touches others [SEL-011] | D-12 |
| Labels count only when set at container creation; most inherited image labels ignored [SEL-013] | Labels go in compose `labels:` (container labels), never relied on from images |
| `@sha256:` refs → `skipped/digest_pinned` [DET] | D-13 / POL-R5 |
| Drift check reads compose file at `com.docker.compose.project.config_files` path [DEP-011] | Rendered files on a volume mounted at the same path in both containers (D-21) |
| Default unarmed: reports `would_update`, changes nothing [CFG-006] | Show `armed` from status; banner when false |
| Status doc `format: parachute.status/v1`, null-not-omitted fields [STS-002] | Strict decode; reject other `format` values |

## Docker writer rules and policy (moved from 01/05)

| ID | Rule |
|---|---|
| PCH-001 | No per-service "update image"/"pull" action; `repull` only on `as_is` (400 `repull_not_allowed`). |
| PCH-002 | A `parachute` revision MUST use tag refs, not digest refs (POL-R5 `digest_pinned`, D-13). Changing the tag is a new revision + deployment. |
| PCH-003 | Apply/teardown of a `parachute` stack is refused (409 `parachute_busy`) while Parachute shows an open `last_transaction.state` or non-empty `frozen` for it; release freezes first. |
| PCH-004 | POL-R8: `parachute` profile requires `C8_PARACHUTE_URL` set (`parachute_not_configured`). POL-R11: `build:` → `build_not_supported`. |
| PCH-005 | Compose project `c8-<ns>-<stack>` (≤ 63 chars); revisions rendered to `/var/lib/control8/stacks/<project>/r<N>/compose.json`, volume mounted read-only into Parachute at the same path (D-21). |
| PCH-006 | Secrets: `secret_refs` `[{name, service, host_path, target}]` mounted read-only; `host_path` subject to POL-R4. |

## Label template — "With Parachute" (`safety_profile=parachute`)

Applied to **every service** in the rendered compose (container `labels:`). `<x>` = substituted.

| Label | Value | Source / rule |
|---|---|---|
| `control8.managed` | `true` | always |
| `control8.stack` | `<stack.id>` | always |
| `control8.namespace` | `<namespace.slug>` | always |
| `control8.revision` | `<revision.number>` | always |
| `control8.safety_profile` | `parachute` | always |
| `parachute.enable` | `true` | always [SEL-004] |
| `parachute.scope` | `<C8_PARACHUTE_SCOPE>` (default `control-8`) | always [SEL-011] |
| `parachute.backup` | `<revision.backup_mode>` | stop\|live\|none [BKP-003] |
| `parachute.tag-follow` | `none` | forced (D-14) [POL-005] |
| `parachute.database` | `true` | only on `database_services` [POL-003] |
| `parachute.approval` | `required` | on `database_services` when effective policy `protected_db_approval=true` [POL-006] |

Compose-level injections:

| Field | Value |
|---|---|
| top-level `name` | `<stack.compose_project>` (user value overwritten) |
| `services.*.pull_policy` | `missing` (D-27) |
| `services.*.container_name` | removed if user set it (compose default `<project>-<service>-<n>` keeps names unique and in-project) |

### Allowed passthrough (`service_options`)
Only these keys may be set per service; anything else under `parachute.*` → violation `label_forbidden`.

| Key | Parachute ref |
|---|---|
| `parachute.verify.url`, `parachute.verify.cmd`, `parachute.verify.timeout`, `parachute.verify.stable` | VER-003..006 |
| `parachute.backup.exclude`, `parachute.backup.dump` | BKP-002/004 |
| `parachute.delay-days` | POL-002 |
| `parachute.maintenance-window`, `parachute.maintenance-window-duration` | POL-007 |
| `parachute.depends-on` | DEP-004. Parachute already reads compose `depends_on` within a stack; setting this label **replaces** links and compose `depends_on` for that container, so the renderer adds a policy warning (not a violation) when it is set on a service that also has `depends_on` |
| `parachute.stop-signal` | UPD-006 |

Forbidden even in passthrough: `parachute.enable`, `parachute.scope`, `parachute.monitor-only`, `parachute.no-pull`, `parachute.tag-follow`, `parachute.self`, `parachute.armed`, `parachute.lifecycle.*` (hooks need sidecar `--enable-lifecycle-hooks`; out of v1), and the Parachute-set labels (`parachute.transaction`, `.helper`, `.snapshot`, `.previous-image`, `.updated-at`, `.restore-verify`).

User `labels:` in compose that start with `control8.` or `parachute.` are rejected (`label_forbidden`), so the template is the only source.

### Database auto-detect
`database_services` = user list ∪ services whose image repo's last path component is in Parachute's POL-003 list: `postgres, postgresql, postgis, timescaledb, mysql, mariadb, percona-server, mongo, mongodb, redis, valkey, influxdb, elasticsearch, opensearch, cassandra, couchdb, clickhouse-server, neo4j`. Keep this list in one Go var with a comment citing POL-003. Control 8 always stamps the explicit `parachute.database=true` on those services so both sides agree even if the lists diverge. Users cannot mark a recognised image as non-database in v1.

## "As-is" mode (`safety_profile=as_is`)

| Label / field | Value |
|---|---|
| `control8.*` | as above, `control8.safety_profile=as_is` |
| `parachute.enable` | `false` (excluded even if the sidecar ever drops `--label-enable`) [SEL-003] |
| any other `parachute.*` | none; `service_options` must be empty (violation otherwise) |
| `pull_policy` | `missing` |

Behaviour: plain `docker compose up -d --remove-orphans`. No snapshot, no verify, no rollback, no Parachute calls, no `parachute_busy` precheck. To move to a newer digest of the same tag the operator redeploys with `repull: true` ("Re-pull" checkbox), which runs `docker compose pull` before `up` (PCH-001). UI/API always show "As-is (no safety net)".

## Deploy execution (both modes)

1. Precheck: policy allowed; no other running deployment (DOM-001); for `parachute`: Parachute reachable and the stack (by `compose_project`) has no service whose `last_transaction.state` is open (`snapshot|update|verify|handover|rolling_back`; terminal = `aborted|committed|rolled_back|rollback_failed`, per Parachute glossary "Transaction state machine") and `frozen == []` (PCH-003). Uses a fresh `GET /v1/status`, not the cache.
2. Write `/var/lib/control8/stacks/<project>/r<N>/compose.json` (rendered) and `.env` (from `env`).
3. `docker compose -p <project> -f <file> --env-file <envfile> up -d --remove-orphans --wait --wait-timeout 300` (exec, no shell).
4. On exit 0: deployment `succeeded`, stack `active`, `desired_revision=N`; trigger inventory sync.
5. Non-zero / timeout: `failed`, stack `failed`; output kept. Control 8 does **not** auto-rollback (re-deploying the previous revision is a manual new deployment).

Teardown: same precheck; `docker compose -p <project> down --remove-orphans` (volumes kept; `-v` never used in v1). After teardown of a `parachute` stack, if Parachute still lists the stack with `frozen != []`, show "release freeze" (Parachute keeps freezes after `compose down` [API-007]).

## Status sync

| Step | Rule |
|---|---|
| PCH-010 | Poller calls `GET /v1/status` every `C8_PARACHUTE_POLL` (30 s), timeout 10 s. |
| PCH-011 | Decode strictly; `format` must be `parachute.status/v1`, else mark `parachute_incompatible` and stop mapping. |
| PCH-012 | Store the whole doc as the latest row in `parachute_status` (single row, replaced) with `polled_at`. |
| PCH-013 | Map `hosts[name=target.parachute_host].stacks[name=stack.compose_project]` → `stack.parachute`; each `services[name]` → `service.pch_*`. Parachute stacks not matching any Control 8 project are ignored (not shown to non-global-admins). |
| PCH-014 | When a service's `last_transaction.id` or `.state` changes vs the previous poll, write audit event `parachute.txn` (`actor_type=parachute`, details = txn object + stack/service). New `frozen` entries → `parachute.freeze`. |
| PCH-015 | Deployed digest = Engine inventory (`image_id`, `repo_digest`), refreshed every poll and after each deployment. Parachute's `image_id` is cross-checked; mismatch is logged at debug only (timing). |
| PCH-016 | `stale = now - polled_at > 3 × C8_PARACHUTE_POLL`. Stale data is shown greyed with its age, never hidden. |
| PCH-017 | `armed` from the doc is shown on the `local` target + every `parachute` stack. |

Inventory (Engine SDK, same tick): list containers with label `control8.managed=true`, group by `control8.stack`, then by `com.docker.compose.service`. Drift items:

| Kind | Condition |
|---|---|
| `service_missing` | Service in desired revision has no container |
| `service_extra` | Container for the stack whose service is not in the revision |
| `image_mismatch` | Container `image_ref` repo:tag ≠ rendered `image` (digest differences ignored) |
| `label_mismatch` | `control8.revision` ≠ `desired_revision` or `control8.safety_profile` ≠ stack profile |
| `parachute_drift` | Parachute service `drift` non-null |

Containers labelled `parachute.enable=true` + scope but **without** `control8.managed` are listed for global admins as "unmanaged in Parachute scope" (bypass signal) on the Admin: targets screen.

## Proxy endpoints

| Control 8 | Parachute call | Mapping |
|---|---|---|
| `POST .../stacks/{s}/parachute/approve {service}` | `POST /v1/approve?container=<service.container_name>&host=<parachute_host>` | 202→202 `{digest}`; 200→200 `{transactions}`; 404→404 `nothing_to_approve`; 400→502 `parachute_bad_request` |
| `POST .../stacks/{s}/parachute/release` | `POST /v1/approve?stack=<compose_project>&host=<parachute_host>` | 200→200; 404→404 `nothing_to_release` |
| `POST /parachute/cycles` | `POST /v1/update` (background, timeout 35 min) | 202 job; Parachute 409 → job `conflict` with `running` id; result body stored |
| `GET /parachute/status` | cached doc | filtered to visible stacks |

Every proxy call writes an audit event (`parachute.approve`, `parachute.release`, `parachute.cycle`) with Parachute's response code. Parachute response bodies are passed through only as the fields above (Parachute already redacts [API-012]).

Note: a frozen database container held with `approval_required` needs two approve calls (first releases, second approves) [API-007]; the UI shows the button again after the first.

## Failure modes

| Situation | Detection | Behaviour |
|---|---|---|
| Parachute down / unreachable | connect error or timeout | Target `local` shows "Parachute unreachable"; status goes stale; approve/release/cycle → 503 `parachute_unavailable`; **apply/teardown of `parachute` stacks refused** (503) — cannot verify no open txn; `as_is` unaffected; read API unaffected |
| Token wrong | 401 | `parachute_auth_failed` on the target; no retries until config reload/restart; poll paused 5 min |
| Throttled | 429 | wait `Retry-After` |
| Endpoint disabled | 404 on `/v1/status` | `parachute_misconfigured` (needs `PARACHUTE_HTTP_API_STATUS=true`) |
| Incompatible version | `format` mismatch | `parachute_incompatible`; no mapping, proxy disabled |
| Parachute unarmed | `armed=false` | Banner "Parachute is unarmed: updates are reported (would_update), not applied" |
| Stack missing from Parachute status for a `parachute` stack | not in doc after a completed cycle newer than the deployment | Drift item `parachute_drift` "not in Parachute scope" (likely scope mismatch between `C8_PARACHUTE_SCOPE` and `PARACHUTE_SCOPE`) |
| Open transaction during deploy request | precheck | 409 `parachute_busy` with txn id |
| Stack frozen (`rollback_failed`) | `frozen != []` | Health `frozen`; apply refused (409 `parachute_busy`); release requires admin |
| Someone `docker compose up`s a stack by hand | inventory label/image mismatch | drift items; no auto-correction in v1 |
| Parachute updated the digest (normal) | `image_id` change, `last_transaction.result=updated` | Not drift; audit `parachute.txn` |
| Parachute rolled back | `result=rolled_back` | Audit + stack page shows reason; no Control 8 action |
| Cycle trigger while running | Parachute 409 | Job `conflict` showing running id |
