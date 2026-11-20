# 07 Roadmap

One milestone per session. Each is independently shippable: `make test` green, `docker compose -f deploy/compose.yaml up` still works, nothing half-wired. Do them in order. Do not start M(n+1) work inside M(n). Kubernetes and prod governance come first (M0–M16 = v1); the Docker + Parachute track (D1–D3) is later and optional.

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

## Docker track (later, optional — after v1)

| Milestone | Scope | Acceptance / tests |
|---|---|---|
| D1 — Docker runtime driver | `internal/runtime/docker` (04 render, compose CLI apply/teardown, Engine reads), `C8_DOCKER_HOST` seeds target `local`, `docker-cli` + compose in image, compose `group_add` docker GID (D-38), as-is profile only | As 04 deploy execution on a real engine + local `registry:2`; container runs as UID 10001 and can reach the socket via group; `-tags integration` |
| D2 — Parachute integration | 04 entirely: sidecar `ghcr.io/kazzyapi/parachute:latest` (**pin before release**, D-39), `parachute` profile + label template, poller, proxy endpoints, guard, failure modes, UI additions | Fake Parachute httptest fixtures (STS-002) + one integration test with the real unarmed sidecar |
| D3 — docker-socket-proxy | Put `tecnativa/docker-socket-proxy` (or equivalent) between Control 8/Parachute and the socket with only needed API sections | Same integration suite passes through the proxy; direct socket mount removed |

## Later (not v1, do not build)

Helm/Kustomize input; adopting existing objects; two-approver grants; token minting/revocation via API/UI; targets editable via API; data snapshots; Docker multi-host; per-stack Parachute cycle; log viewing; multiple IdPs; admission-policy enforcement shipped by Control 8.
