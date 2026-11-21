# 07 Roadmap

One milestone per session. Each is independently shippable: `make test` green, `docker compose -f deploy/compose.yaml up` still works, nothing half-wired. Do them in order. Do not start M(n+1) work inside M(n). Kubernetes and prod governance come first (M0–M16 = v1; post-v1 M17–M21 below); the Docker + Parachute track (D1–D3) is later and optional.

## Rules for every milestone

| Rule | |
|---|---|
| R-1 | Read README.md decision log + the specs the milestone cites before coding. |
| R-2 | Tests cite requirement IDs (`// K8S-041` or `TestGRT012_...`). |
| R-3 | Unit tests: `go test ./...` (no cluster, no Docker). Cluster tests: `go test -tags k8s ./...` (`make test-k8s`) against a throwaway **kind** cluster (`scripts/kind-up.sh`; k3d accepted locally). CI runs both on GitHub Actions (`helm/kind-action` or plain `kind` binary). Docker tests (track D): `-tags integration`. Frontend (from M13): `make web-test` (Vitest) and `make e2e` (Playwright against the built binary on kind). |
| R-4 | New dependency only if listed in 01 tech stack; otherwise add a decision-log row first. |
| R-5 | If a spec is wrong or ambiguous, pick the smallest reading, implement it, and add a decision-log row. |
| R-6 | Every state-changing endpoint writes an audit event (DOM-009) from the milestone that adds it. |
| R-7 | Update `openapi.yaml` in the same change as a new route (API-002 parity test from M1 on). |
| R-8 | From M4 on, every new state change publishes its event (ARC-005, 10 catalogue) in the same milestone, with a test that a subscribed viewer receives it and an unauthorised one does not. |

## M0 — Skeleton (K8s-ready, no Parachute)

1. `go mod init github.com/KazzyAPI/control-8`; `go 1.23`.
2. Layout: `cmd/control8/main.go`, `internal/{config,store,api,auth,audit}/` (others created by the milestone that needs them), `deploy/compose.yaml`, `deploy/kubernetes/`, `scripts/`, `docs/`.
3. `internal/config`: parse `C8_*` from 01 (all vars, incl. later ones, parsed and validated); env first, optional `<VAR>_FILE`; exit 2 naming the variable; table test.
4. `internal/store`: SQLite pragmas `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`; embedded migrations + `schema_migrations`; `0001_init.sql` creates `target` (no seed). Test: migrate twice is a no-op.
5. HTTP: `GET /healthz`, `GET /readyz` (DB ping), request-id middleware (API-007), slog JSON access log, graceful shutdown (10 s).
6. `Dockerfile`: multi-stage, `CGO_ENABLED=0`, runtime `gcr.io/distroless/static` or `alpine:3.22` pinned by digest; `USER 10001`; `/var/lib/control8` owned by 10001 (ARC-010). No docker CLI yet.
7. `deploy/compose.yaml` as 01 (control8 only); `deploy/kubernetes/` Deployment/PVC/Service/ServiceAccount (non-root, read-only root FS); `rbac.yaml` placeholder filled in M7.
8. `scripts/kind-up.sh` / `kind-down.sh` (cluster `c8-test`, writes kubeconfig to `./tmp/kubeconfig`).
9. `Makefile`: `build`, `test`, `test-k8s`, `lint` (`go vet` + `gofmt -l`), `image`, `up`, `down`, `kind-up`, `kind-down`.
10. `.gitignore`, minimal `README.md`, keep `CLAUDE.md`.

Acceptance: `make test` passes; `make image` builds; `make up` → `curl localhost:8000/healthz` = 200; `docker exec control8 id -u` = `10001`; `kubectl apply -f deploy/kubernetes/` on kind → Pod Ready.
Tests: unit for config + migrations; `scripts/smoke-m0.sh`.

## M1 — Audit, tokens, break-glass, authz core

Scope: `user`, `session`, `token`, `role_binding`, `audit_event` (+ UPDATE/DELETE trigger); `audit.Write`; `authz.Require`; break-glass (AUTH-010..015) incl. `hash-password`; static tokens from `C8_TOKENS` + `gen-token` + startup sync + `GET /tokens` (AUTH-030..035; owner resolution by OIDC sub lands with users here, OIDC in M3); `/api/v1/me`; role-bindings API; problem+json (API-003); body limit + unknown fields (API-001); CSRF (API-004); OpenAPI parity test (API-002) honouring `x-milestone`.
Acceptance: break-glass login → `/me` shows `global_admin`; a `gen-token` token listed in `C8_TOKENS` authenticates, removing it + restart → 401, expired → 401; no token hash or plaintext in any response or log; invalid `C8_TOKENS` → exit 2; audit row per call incl. denied; UPDATE on audit fails.
Tests: httptest per endpoint; table test for AUTH-020; argon2 verify.

## M2 — Namespaces and assets (with tiers)

Scope: `namespace` (with `environment_tier`, `approver_group`, GRT-001/003/027, D-50), `asset`; CRUD; API-005, API-006; Argo assets; `/audit` query.
Acceptance: viewer of `a` gets 404 on `b`; operator creates asset, viewer 403; create without tier → 400; prod namespace without `approver_group` → 400; tier and `approver_group` changes only by global_admin, audited; audit filters work.
Tests: httptest matrix role × endpoint.

## M3 — OIDC login (Authentik reference) + approver groups

Scope: AUTH-001..007, AUTH-021, AUTH-023; `approver_for` in `/me` from groups vs `namespace.approver_group`; token owner resolution by OIDC sub (AUTH-032); `/auth/*`; minimal `/login`, `/no-access`.
Acceptance: mockoidc login creates user + session; groups map to roles; member of `shop-approvers` (approver group of `shop`) gets `approver_for: ["shop"]` in `/me`; a token whose `owner_sub` matches the logged-in user acts with that user's role; no bindings → `/no-access`; tampered `state` → 400.
Tests: mockoidc in-process. **Manual test against Authentik** (owner's instance), recorded in `docs/ops/authentik.md` (provider setup, client secret into the Control 8 Secret, groups mapping, a product approver group) — only claim it passed if it was run.

## M4 — Event bus and SSE

Scope: 10 entirely for the bus and endpoint: `internal/events` (EVT-001..005), `GET /api/v1/events` (EVT-010..015), per-viewer filter (AUTH-040..043), `audit.created` published from `audit.Write`; `csrf_token` in `/me`; openapi `getEvents` + `Event` schemas; `C8_EVENTS_*` config.
Acceptance: a viewer of `a` receives `audit.created` for `a` and not for `b`; global audit only to global_admin; reconnect with `Last-Event-ID` gets missed events in order; ID older than the buffer → `reset`; heartbeat every 15 s (fake clock); slow consumer disconnected without blocking publishers; logout closes the stream with `session.ended`; 6th stream for one principal → 429.
Tests: `httptest` + raw SSE reader; bus tests with `-race`; filter matrix table test.

## M5 — Targets and Kubernetes client

Scope: `C8_TARGETS` parsing + startup sync (K8S-001..004, D-49), kubeconfig from Secret env or file; client construction (kubeconfig / in-cluster); reachability check (`ServerVersion`, `C8_TARGET_CHECK`); read-only `/targets`; `/readyz` reports targets; `target.status` events.
Acceptance: kind target → `reachable: true` within one check and a `target.status` event; missing/garbage kubeconfig → `reachable: false` + `kubeconfig_invalid`, Control 8 still starts; kubeconfig content never appears in any response, event, log or DB row; removing a target with stacks from config → exit 2 (test added in M6); `deploy/kubernetes/` sample (ConfigMap + Secret + Deployment with `envFrom`) runs on kind with an in-cluster target.
Tests: unit with a fake discovery client; `k8s` tag test against kind.

## M6 — Stacks, revisions, manifest render, policy (no cluster writes)

Scope: `stack` (with `runtime`, K8S-004), `revision`, `policy` tables; stack create/delete draft; revision create: parse manifests (K8S-020), render (K8S-022/024/026, injected labels), DB auto-detect; `policy.Evaluate` POL-R1..R4, R6, R7, R9, R10, R12..R14 and rules merge; policy endpoints; revision diff; profile/runtime validation; `stack.updated`, `stack.removed`, `revision.created` events.
Acceptance: rendered objects carry exactly the 08 labels incl. pod templates and not selectors; `Secret`, `ClusterRole`, `Namespace` → `kind_not_allowed`; `hostPath` → `host_path_not_allowed`; `privileged: true` → `privileged_not_allowed`; namespace `kube-system` → `namespace_not_allowed`; `parachute` profile on kubernetes stack → 400; `compose` on kubernetes stack → 400 `spec_runtime_mismatch`.
Tests: golden files `internal/runtime/kubernetes/testdata/*.in.yaml` → `*.golden.yaml` (`-update` flag); table test per POL rule.

## M7 — Kubernetes apply, prune, teardown, ownership (non-prod)

Scope: `runtime.Driver` interface + kubernetes driver Precheck/Apply/Prune/Teardown (08 Apply section, K8S-000, K8S-011, K8S-040..044, D-37); `deployment` table + state machine; request/approve/reject/cancel; worker + per-stack mutex + DOM-001; POL-R9/R10 approvals, DOM-004; Argo guard; `deploy/kubernetes/rbac.yaml` (K8S-005); `deployment.updated` on every status change. Apply returns success right after apply in this milestone (health wait in M10).
Acceptance (kind): apply as-is → objects exist with labels and field manager `control-8`; rev 2 removing a ConfigMap prunes it, keeps the PVC; pre-existing unlabelled Deployment with the same name → `ownership_conflict`, nothing written; object with `argocd.argoproj.io/instance` → `argo_managed`; `kubectl edit` a field then re-apply → `ownership_conflict` (no force); teardown deletes all but PVCs, namespace kept; Control 8 runs with only the shipped RBAC; an SSE subscriber sees `queued → running → succeeded`.
Tests: unit with `dynamic/fake` for ordering, ownership and prune selection; `k8s` tag suite on kind.

## M8 — Prod access grants

Scope: 09 entirely: `access_grant` table, state machine, GRT-010..027 checks, `grants.Require` on deployment request/approve/teardown (and image-updates when it lands), worker re-check (GRT-012/013), break-glass bypass (GRT-016), sweeper, grant API, `grant_id` on deployments + audit, webhook (GRT-040/041), `access_grant.updated` events (incl. sweeper activate/expire), `docs/ops/prod-rbac.md` (GRT-060).
Acceptance: non-prod deploy needs no grant; prod deploy without grant → 403 `grant_required`; request → approve by a member of the namespace's approver group (not the requester) → deploy succeeds and audit has `grant_id`; self-approve → 403; member of another namespace's approver group → 403; namespace without `approver_group` → 409; second open grant same scope → 409; grant expiring while queued → deployment `failed grant_expired`, nothing applied; grant expiring mid-apply → deployment finishes; revoke stops new deploys; break-glass deploys to prod with `grant_bypass`; webhook receiver sees signed `requested`/`approved`/`expired`; token deploy uses owner's grant; the approver's SSE stream receives the request and the requester's receives the approval; a third user receives neither.
Tests: unit with a fake clock for every state transition and GRT rule; httptest for the API; webhook via `httptest.Server` checking HMAC.

## M9 — Informers, observe, drift

Scope: K8S-060..062, 064, 065: `Driver.Watch` with per-target dynamic shared informers (label selector, resync `C8_INFORMER_RESYNC`), target `sync` state + watch-based reachability (K8S-003), list-poll fallback; `internal/inventory` debounced recompute from caches; `service` rows; drift kinds; stack `health`; services + `/inventory` endpoints; `stack.updated` only on change.
Acceptance (kind): `kubectl scale` a managed Deployment → `stack.updated` with new ready count within 2 s, no API-server list per change (fake counter in unit test); `kubectl delete deploy` → `service_missing`; `kubectl set image` → `image_mismatch`; RBAC without `watch` → `sync: polling` and updates still arrive within `C8_TARGET_CHECK`; stopping the kind API server → `sync: down`, stacks `unknown`, recovery restarts informers.
Tests: inventory recompute is a pure function over cached objects (unit); informer wiring with `dynamicfake` + a fake watch; `k8s` tag tests on kind.

## M10 — Rollout health

Scope: kubernetes `Health` (08 criteria, immediate-fail conditions) evaluated on informer changes (K8S-063); worker waits with timeout; `as_is` failure leaves cluster as is; `rollout` JSON on deployments; coalesced `deployment.updated` progress; `rollout.healthy` / `rollout.failed` audit.
Acceptance (kind): image that never becomes ready (`busybox` exiting 1) → `rollout.failed` within timeout, `as_is` stack `failed`; a 3-replica rollout emits progress events with rising `updated`/`ready`; Job completion counts as healthy.
Tests: health as a pure function over fixtures in `testdata/health/*.yaml` (Deployment/StatefulSet/DaemonSet/Job states); one `k8s` end-to-end.

## M11 — Managed rollout: image updates and automatic rollback

Scope: K8S-050, 052..055; `POST .../image-updates`; revision `origin=image_update`; rollback orchestration in executor; `rollout_frozen` + 409 + admin clear; `image_update.request`, `rollout.rollback`, `stack.rollout_frozen` audit; grant gate on image updates; events for every step.
Acceptance (kind): update to a good digest → healthy, revision N+1 desired; update to a broken tag → rolled back to N, deployment `failed` with `rolled_back`, pods back on old image, SSE shows `rolling_back` then `done`; rollback also failing (test hook) → `rollback_failed`, stack frozen, image updates 409 until an admin deploy succeeds; repo change → 400; `as_is` stack → 409.
Tests: executor rollback logic with a fake driver (table of apply/health outcomes); `k8s` suite with a local registry (`registry:2` on the kind network).

## M12 — Digest poll

Scope: K8S-051, D-46: `internal/digest` with go-containerregistry, `C8_REGISTRY_AUTH`, opt-in per revision, skip rules, non-prod auto-request, prod report-only (`digest_status.updates` via `stack.updated`), `digest_poll.detected` audit.
Acceptance: pushing a new digest for the tag to the local registry → non-prod stack gets an image-update deployment by `system`; prod stack only shows `update_available`; a digest that failed once is not retried; registry down → `digest_status.error`, no update.
Tests: unit with `go-containerregistry/pkg/registry` in-process; one `k8s` end-to-end.

## M13 — Frontend scaffold, embed, CI

Scope: `web/` with Vite + React + TypeScript (strict), TanStack Query, `react-router`, generated client (`openapi-typescript` → `web/src/api/schema.d.ts`, `openapi-fetch`); `internal/web` serving embedded `web/dist` with SPA fallback and cache headers (UI-005/006); committed stub `index.html` (D-58); app shell: login redirect, `/me` + CSRF, `EventSource` hook with connection state machine and cache-update dispatcher (UI-020..022, 025), stale banner (UI-023), `ProfileBadge`/`TierBadge`; Dockerfile Node stage; Makefile `web`, `web-test`, `e2e`, `gen-client`; CI job: `npm ci`, `gen-client` + `git diff --exit-code`, typecheck, Vitest, build, Go build with real bundle, Playwright smoke.
Acceptance: `make image` ships one image serving the SPA at `/` and API at `/api/v1`; deep link `/n/shop/s/web` reloads into the SPA; `go test ./...` passes without Node; killing the server shows the stale banner within 5 s and recovery clears it; generated client drift fails CI.
Tests: Vitest for the event→cache dispatcher (one case per event type, `reset`), connection state machine; Playwright smoke: login via break-glass, shell renders, stream reconnect banner; axe check (UI-033).

## M14 — SPA: live read screens

Scope: overview, namespace, asset, stack (workloads, rollout progress, drift, digest updates), revision, deployment (timeline + rollout table), audit live feed with pause, admin targets, my grants and active grants with countdowns (06 screens, UI-010..018, UI-024, UI-030..032).
Acceptance (Playwright on kind): `kubectl scale` reflects on the stack page without reload; a deployment page shows progress then result live; a new audit event appears at the top of the feed (and is held while paused); badges with text on every stack row; document title suffixes; `<script>` in a workload name renders as text (UI-003); axe passes on every screen.
Tests: Vitest component tests per screen with fixture data; Playwright scenarios above.

## M15 — SPA: write flows

Scope: create asset/stack, set approver group, new revision form (draft in `sessionStorage`), deploy dialog (UI-013/014, prod typed confirm), image update dialog, approvals page, request access form, approver queue (approve/deny/shorten), revoke, teardown, admin access + policy forms (no token or target forms: config only).
Acceptance (Playwright, break-glass + one mockoidc approver in a second browser context): non-prod deploy happy path; prod deploy blocked → request access → approver sees it appear live → approves → requester's buttons unlock live → deploy; grant expiry flips the buttons back without reload; as-is deploy requires checkbox; missing CSRF → 403 surfaced as an error toast.
Tests: Vitest for forms/validation; Playwright scenarios above.

## M16 — Hardening and release (v1)

Scope: security headers (CSP per UI-005, nosniff, referrer-policy); output redaction check; event payload review (AUTH-043); `scripts/smoke.sh` end-to-end on kind; CI (unit, `k8s` on kind, web, e2e, image push on tag); `docs/ops/install.md` (compose and in-cluster, reverse-proxy note: disable buffering for `/api/v1/events`), `docs/ops/prod-rbac.md` reviewed.
Acceptance: smoke passes on a clean machine; manual security checklist in PR; image published for a tag.

## Post-v1: Products (M17–M18b) and discovery (M19–M21)

Order: products first (the frame), then discovery. Same rules R-1..R-8. Every new route lands with its `x-milestone` marker removed in openapi (API-002). Kind fixtures for M19–M21 live in `internal/discovery/testdata/kind/` and are applied by the `k8s` tests.

## M17 — Products, applications, environments; approver and grant rework (backend)

Scope: 12 PRD-001..006, 010..014, 020..024, 030..031, 040..046: `product`, `environment` tables; asset → application (`product_id`, `home_namespace_id`, extended `managed_by`, `external_ref`); migration to built-in `unassigned` with collision renames (PRD-003); product role bindings + AUTH-024 resolution; `allowed_products` (POL-R15); grant scope (target, namespace, product), approver resolution with `unassigned` fallback, `approver_for_products`; product/application/environment routes, place-app route, product overview matrix; `product.*`, `application.updated`, `environment.updated` events; product role holders receive stack events; `C8_ENVIRONMENT_PRESETS`; smoke/e2e fixtures create product `shop` with dev/staging/prod.
Acceptance:
- Migration on a copy of an M16 DB: every asset in `unassigned`, colliding slugs renamed and audited, existing grants keep working for those apps, role bindings unchanged; running it twice is a no-op.
- httptest matrix (binding kind global/namespace/product × resource stack/namespace/product × role) asserts AUTH-024 precedence, including product operator deploying in two namespaces without namespace bindings, and 403 when `allowed_products` excludes the product.
- Approver resolution table test: product group approves; namespace group cannot approve a non-`unassigned` product; `unassigned` app uses namespace group; product without group impossible (400 on create).
- A grant for product A in namespace N does not let a user deploy product B's app in N (403 `grant_required`).
- `staging` environment on a non-prod namespace makes the stack prod (grant required); lowering a tier needs global_admin.
- Kind e2e: one app placed into two environments (dev, staging) on kind, both deployed; `GET /products/shop/overview` shows both cells with image/digest, health and rollout state; an SSE subscriber with only a product binding receives both stacks' events.
Tests: SQL migration tests on a fixture DB; httptest matrices; `k8s` e2e as above.

## M17b — Migration assistant backend

Scope: 12 MIG-001..011 (adoption items stubbed until M21): migration plan storage, dry-run, stored plan with row versions, apply in one transaction, per-entity audit with `migration_id`, undo within `C8_MIGRATION_UNDO_WINDOW`, `migration.*` events, approver-group warning from cached claims, typed `confirm_prod`, permissions MIG-008; overview count of apps needing migration in `GET /products` (built-in `unassigned` counts).
Acceptance (httptest, seeded DB with several `unassigned` applications across three namespaces and stacks on two targets):
- dry-run returns changes, approver resolution before/after, tier changes and "harder/easier" callouts; nothing is written.
- plan → apply creates product, environments (dev/uat non-prod, staging/prod prod), moves applications, sets `stack.environment_id`, creates the team binding; afterwards the approver of those stacks resolves to the product group and effective tiers match.
- apply without `confirm_prod` when prod is involved → 400; re-apply of an applied plan → same result, no duplicate rows; a row changed between plan and apply → 409 `plan_stale`.
- undo restores the previous state (products/environments deleted if empty); undo after a later change to a touched row → 409 `undo_conflict`.
- a product admin who is not admin of every source namespace cannot claim those apps (403).
Tests: unit for plan computation (pure over a snapshot) and reverse-plan generation; httptest as above.

## M18 — Products UI

Scope: 06 "Products" (UI-040..043): Products list, product page tabs (Applications, Environments matrix, Deployments, Access/Grants, Settings; Findings tab placeholder until M20), application page, breadcrumbs, deploy-to-another-environment flow, product-level role binding management on Access tab, overview product roll-up and "needs migrating" card. Live via existing and M17 events.
Acceptance (Playwright on kind): product list cards show counts; matrix shows the app in dev and staging with live rollout progress after a deploy; "Deploy here" into an empty uat cell copies the source revision and deploys; staging cell shows PROD tier badge and the grant prompt; product binding added on Access tab lets a second user see the product; axe passes.
Tests: Vitest for matrix cell reducer (stack/deployment/grant events), Playwright scenarios above.

## M18b — Migration wizard UI

Scope: 06 "Migration wizard": `/products/migrate`, five steps with dry-run on every change, suggestions by name match, tier alerts, bulk assignment table, review with collapses/diff and harder/easier callouts, typed confirm, progress, undo button, `plan_stale` handling, `sessionStorage` resume; entry from overview card.
Acceptance (Playwright, seeded unassigned assets): happy path creates product `shop` with dev/uat/staging/prod mapped to suggested namespaces, assigns three apps, applies; overview card count drops to 0; undo restores them; editing a seeded row in another tab before apply shows the stale alert; staging tier change shows the PROD alert.
Tests: Vitest for step validation and suggestion matching; Playwright as above.

## M19 — Discovery backend

Scope: 11 DSC-001..008, 010..011, 020..024: per-target discovery informers with transforms, status, cap/truncation, system/exclude namespaces; `discovery.Classify`; `namespace_mapping`, `discovered_workload`, `discovered_assignment` tables; mapping + read-model + assign routes; `discovery.updated` events; `Target.discovery`; `deploy/kubernetes/rbac-discovery.yaml`; `docs/ops/prod-rbac.md` update (DSC-056); config vars.
Acceptance (kind, `k8s` tag): in prod-tier namespace `shop` create an unlabelled Deployment, an Argo-labelled Deployment, a Helm-labelled Deployment, a Deployment owned by a fake CR (ownerReference `example.com/v1 Widget`), a CronJob with a Job, and a bare Pod; plus a Control 8-deployed stack. Assert classes `ungoverned`, `argo`, `helm`, `operator` (root owner Widget), `managed`; CronJob listed once (Job folded); bare Pod listed; kube-system workloads class `system` collapsed; no Secret/ConfigMap watch (RBAC without them still `on`); removing the discovery ClusterRoleBinding → `discovery: unavailable` while deploys still work; `C8_DISCOVERY_MAX=3` → `truncated: true`; scaling a workload emits one coalesced `discovery.updated`; a namespace viewer of `shop` sees the workloads, a viewer of another namespace does not; bulk assign to product `shop` is audited and survives restart.
Tests: table tests for `Classify` (fixture YAML per class and precedence ties) and transforms (memory: no managedFields/spec remnants); `k8s` suite above.

## M20 — Findings, acknowledgements, webhook, Cluster and Findings UI

Scope: 11 DSC-030..035 (findings computed from the read model via policy rule functions), `finding` table, findings routes, acknowledge/unacknowledge with expiry sweeper, `finding.opened` webhook for new high findings, `discovery.finding` events, product roll-up (`open_findings` in PRD-030), `docs/ops/admission.md` (DSC-060); UI: Cluster page, Findings page, overview "Ungoverned in prod: N" card, product Findings tab.
Acceptance (kind, same fixtures): unlabelled Deployment in prod namespace → `ungoverned_in_prod` (high) and one signed webhook; same in a non-prod namespace → `ungoverned_non_prod` (info), no webhook; image from a non-allowed registry → `image_registry_not_allowed`; `privileged: true` → `privileged_workload`; hostPath → `host_path_mount`; Argo workload without matching application → `argo_unlinked`, resolved after registering one (M21) or a fixture application; deleting the workload resolves its findings; acknowledging with expiry in the past (fake clock) reopens; Playwright: Cluster page shows class badges, counts and truncated banner; overview card count updates live when a new ungoverned prod Deployment is created; axe passes.
Tests: pure tests for finding computation; httptest for ack rules and webhook HMAC; `k8s` + Playwright as above.

## M21 — Adoption and register-only (+ dialogs and wizard integration)

Scope: 11 DSC-039..051, K8S-045: adoption plans (capture, sanitise, render, policy, dry-run generation check, ownership check + `csaupgrade` migration), execute as `adopt` deployment (grant gate, idempotent/resumable), `release` action, register-only via product applications, workload↔application links; wizard adoption items (MIG-001/005); UI adopt and register dialogs, Cluster "Adopt…" into the wizard.
Acceptance (kind, same fixtures): plan for the unlabelled Deployment + its Service in a prod namespace shows sanitised manifests (no status/uid/clusterIP), policy allowed, `requires_grant`; execute without grant → 403 `grant_required`; with a grant → objects gain `control-8/*` labels, pod template unchanged, **no new ReplicaSet** (pod names unchanged), `metadata.generation` unchanged, stack `active` rev 1, workload class becomes `managed` and its findings resolve; re-executing is a no-op; a `kubectl edit`-owned field is migrated and a following image update deploys without `ownership_conflict`; a field owned by a non-allow-listed manager → 409 `ownership_conflict`; Argo/Helm/operator workloads → 409 `tool_managed`; a Secret in the set → 422; release removes labels, pods keep running, workload back to `ungoverned`; register the Argo workload under product `shop` → `argo_unlinked` resolves; Playwright: adopt from Cluster page through the wizard to a running adopted stack.
Tests: sanitiser table tests (per kind), ownership-migration unit test with fake managedFields, `k8s` suite, Playwright.

## Docker track (later, optional — after v1)

| Milestone | Scope | Acceptance / tests |
|---|---|---|
| D1 — Docker runtime driver | `internal/runtime/docker` (04 render, compose CLI apply/teardown, Engine reads), `C8_DOCKER_HOST` seeds target `local`, `docker-cli` + compose in image, compose `group_add` docker GID (D-38), as-is profile only | As 04 deploy execution on a real engine + local `registry:2`; container runs as UID 10001 and can reach the socket via group; `-tags integration` |
| D2 — Parachute integration | 04 entirely: sidecar `ghcr.io/kazzyapi/parachute:latest` (**pin before release**, D-39), `parachute` profile + label template, poller, proxy endpoints, guard, failure modes, UI additions | Fake Parachute httptest fixtures (STS-002) + one integration test with the real unarmed sidecar |
| D3 — docker-socket-proxy | Put `tecnativa/docker-socket-proxy` (or equivalent) between Control 8/Parachute and the socket with only needed API sections | Same integration suite passes through the proxy; direct socket mount removed |

## Later (not v1, do not build)

Helm/Kustomize input; adopting existing objects; two-approver grants; token minting/revocation via API/UI; targets editable via API; data snapshots; Docker multi-host; per-stack Parachute cycle; log viewing; multiple IdPs; admission-policy enforcement shipped by Control 8.
