# Control 8 specs

Source of intent: [../vision.md](../vision.md). These specs are the build contract. When a spec and the vision disagree, the spec wins (it records a decision); fix the vision or log a new decision.

## Index

| File | Contents |
|---|---|
| [01-architecture.md](01-architecture.md) | Components, runtime driver interface, stack, deployment, ownership boundary, writer rules |
| [02-domain-model.md](02-domain-model.md) | Entities, fields, states, invariants |
| [03-api.md](03-api.md) + [openapi.yaml](openapi.yaml) | REST resources, auth scopes, core shapes |
| [04-parachute-integration.md](04-parachute-integration.md) | **Later, optional (Docker track).** Label template, proxying, status sync, failure modes, as-is mode |
| [05-auth-and-policy.md](05-auth-and-policy.md) | OIDC (Authentik reference), break-glass, roles, approver groups, env-defined tokens, policy rules |
| [06-ui.md](06-ui.md) | React SPA: live-data rules, screens, safety/tier indicators, reconnect banner, a11y |
| [07-roadmap.md](07-roadmap.md) | Milestones with acceptance criteria (Kubernetes first, Docker/Parachute later) |
| [08-kubernetes-runtime.md](08-kubernetes-runtime.md) | Targets, manifests, server-side apply, ownership, managed rollout, health, drift |
| [09-prod-access-grants.md](09-prod-access-grants.md) | Environment tiers, time-boxed prod access grants, elevated approval, enforcement boundary, webhook |
| [10-events.md](10-events.md) | Event bus, SSE endpoint, resume/replay, per-viewer filtering, event catalogue |

Requirement IDs (`ARC-`, `DOM-`, `API-`, `PCH-`, `AUTH-`, `POL-`, `UI-`, `K8S-`, `GRT-`, `EVT-`) are stable; tests cite them in their names or comments.

## Glossary

| Term | Meaning |
|---|---|
| **Target** | Where Control 8 deploys. v1: Kubernetes clusters defined in config (`C8_TARGETS`; kubeconfig from a Secret-injected env var or file, or in-cluster service account). Later: Docker host `local` (`kind=docker`). |
| **Runtime** | Per stack: `kubernetes` (raw manifests, 08) or `docker` (compose, 04, later). Equals the target's kind. |
| **Environment tier** | `prod` or `non-prod` on every namespace and target. A stack is prod if its namespace **or** its target is prod (09). |
| **Namespace** | Unit of ownership and permission (team/project). Maps 1:1 to a Kubernetes namespace on Kubernetes targets. |
| **Asset** | A registered application: name, image repository, owner, who manages it (`control8` or `argo`). Metadata only; never a blob. |
| **Stack** | One deployable unit of an asset on a target: a set of manifests (Kubernetes) or a compose project (Docker). |
| **Revision** | Immutable, numbered version of a stack's desired state (manifests or compose, env, secret refs, safety profile). |
| **Deployment** | One attempt to apply a revision to a target (or tear the stack down). Has an approval, run status and, on Kubernetes, rollout status. |
| **Service** | A workload of a stack as **observed** (Kubernetes `Kind/name`, or compose service). |
| **Safety profile** | Per revision. Kubernetes: `managed_rollout` (health wait + auto rollback) or `as_is`. Docker (later): `parachute` or `as_is`. |
| **Managed rollout** | Control 8's small, bounded Kubernetes image-update loop: patch image, apply, wait for health, roll back on failure (08). |
| **Access grant** | Temporary, single-scope (target + namespace), elevated-approved permission for one user to deploy to a prod stack (09). |
| **Approver group** | Per namespace: the OIDC group of the product team that owns it (`namespace.approver_group`). One member (not the requester) gives elevated approval to a grant for that namespace (D-40). |
| **Parachute** | Owner's image-lifecycle daemon for Docker, sidecar image `ghcr.io/kazzyapi/parachute`. Later, optional track. |
| **Drift** | Observed state differs from the desired revision (missing/extra workload, wrong image repo:tag, wrong Control 8 labels). |
| **Break-glass** | Single local admin login for when the IdP is down. Off by default, loudly audited; bypasses grants (09). |
| **Writer** | The one system allowed to change a given aspect of a workload. |

## v1 scope

- Kubernetes targets (one or more clusters); raw manifests, server-side apply, prune, teardown (08).
- Namespaces, assets, stacks, revisions, deployments, observed workloads, audit, policy, config-defined API tokens.
- Environment tiers; prod deploys only with an active elevated-approved access grant; simple grant webhook (09).
- Safety profiles `managed_rollout` / `as_is`, visible everywhere; image updates (manual + opt-in digest poll) with automatic rollback for `managed_rollout`.
- OIDC login (Authentik as reference IdP) + local break-glass; IdP group to namespace role mapping; per-namespace approver group; static scoped API tokens supplied via env/Secret (no token minting in UI/API).
- Policy: allowed registries, namespace allow-list, required labels, forbid privileged/hostPath, protected-database 4-eyes approval, allowed safety profiles.
- Argo-managed assets: register + link only.
- Live web UI: React SPA embedded in the binary, fed by REST + SSE (06, 10); cluster state watched with informers.

Later, optional (after v1, roadmap "Docker track"): Docker runtime driver (compose on `local`), Parachute sidecar integration (04), docker-socket-proxy.

## Non-goals (v1)

| Not doing | Why |
|---|---|
| Image builds, CI, artifact or registry hosting/browsing | Images come from CI to a registry (vision) |
| Helm, Kustomize, Jsonnet, any templating | Raw manifests only (D-31); render elsewhere and paste/push the output |
| Data/PVC/volume snapshots or restore | Managed rollout reverts manifests only (D-33); use storage tooling |
| Canary, blue/green, traffic shifting, metric analysis | Managed rollout is deliberately small |
| Argo sync/recreate, writing to Argo-owned objects | Argo is the writer for its workloads (D-22, K8S-042) |
| Adopting existing unlabelled cluster objects | One writer; no takeover in v1 (K8S-041) |
| Cluster-scoped resources, RBAC objects, CRDs, Secrets in manifests | Kind allow-list (K8S-021); no secret storage |
| Docker runtime and Parachute in v1 | Later optional track (D-41) |
| Volume/network/image management UIs, exec/shell, log streaming, restart buttons | Portainer/Lens clone risk (vision) |
| Storing secret values | Only references (files, existing Kubernetes Secrets) |
| Custom RBAC beyond 3 roles + global admin + per-namespace approver group; user management | IdP owns identity |
| Creating/storing API tokens, targets or credentials through UI/API | Delivered via Kubernetes Secrets/ConfigMaps as env (D-49); minting endpoints are later |
| Two-approver grants | One approver from the owning product group in v1 (D-40); two-approver is a future option |
| Notifications beyond the one grant webhook | Audit API covers the rest |
| Blocking direct `kubectl` access to prod | Cluster RBAC is the operator's job; documented hardening (GRT-060) |
| Tamper-evident (hash-chained) audit | Append-only table is enough for v1 |

## Decision log

| ID | Decision | Rationale |
|---|---|---|
| D-01 | Go (1.23+), single static binary | Same toolchain as Parachute; one small image |
| D-02 | SQLite via `modernc.org/sqlite` (pure Go), WAL, one file | No CGO, no extra container, enough for small teams |
| D-03 | *(Docker track)* Drive apply/teardown with the `docker compose` v2 CLI; read with the Docker Engine Go SDK | Exact compose semantics; SDK only reads |
| D-04 | *(Docker track)* Compose normalized by `docker compose config --format json`; labels injected into the JSON | No compose-go dependency |
| D-05 | Targets are rows (`target`); v1 creates Kubernetes targets via API; the Docker target `local` is seeded only when the Docker track lands | Multi-cluster is natural for K8s; one Docker host stays the Docker-track scope |
| D-06 | ~~UI = Go `html/template` + htmx~~ — superseded by D-53 | |
| D-07 | HTTP routing with stdlib `net/http` ServeMux patterns | No framework dependency |
| D-08 | OIDC with `coreos/go-oidc/v3` + `x/oauth2`, auth-code + PKCE, server-side sessions in SQLite; **Authentik** is the reference IdP for manual tests and docs | Standard, small |
| D-09 | Break-glass: one local admin, argon2id hash from a file, disabled unless configured | Optional break-glass; file keeps it out of the DB |
| D-10 | Roles `viewer` < `operator` < `admin` per namespace, plus `global_admin`; grant approval comes from the namespace's `approver_group` | Enough for small teams; avoids RBAC creep |
| D-11 | API tokens: opaque `c8_<base32>`, defined statically in `C8_TOKENS` (JSON from a Secret: name, owner sub, namespace, role, SHA-256 hash, expiry ≤ 365 d); only hashes ever reach Control 8/DB; revoke = remove entry + restart; `control8 gen-token` prints plaintext + hash | Owner decision: config via Secrets, not UI-minted; still scoped and expiring |
| D-12 | *(Docker track)* Parachute sidecar runs `--label-enable --scope control-8`; Control 8 stamps `parachute.enable=true`, `parachute.scope=control-8` | Only Control 8-tagged workloads are in scope |
| D-13 | *(Docker track)* `parachute` profile requires tag refs (no `@sha256:`) | Parachute skips digest-pinned containers |
| D-14 | *(Docker track)* `parachute.tag-follow` forced to `none` | The tag is desired state, owned by Control 8 |
| D-15 | *(Docker track)* Sidecar ships **unarmed**; arming is the operator's env choice; UI bannered | Safe default (Parachute CFG-006) |
| D-16 | *(Docker track)* Sync Parachute by polling `GET /v1/status` every 30 s | No push API |
| D-17 | *(Docker track)* `POST /v1/update` exposed only as global-admin async "run cycle now" | Cycle is all-stacks |
| D-18 | *(Docker track)* Refuse apply/teardown while Parachute shows an open transaction or freeze for the stack | Never two writers at once |
| D-19 | *(Docker track)* Protected DB: 4-eyes deploy approval + `parachute.approval=required`; approve via proxy | Covers both writers |
| D-20 | *(Docker track)* No bind mounts unless under `allowed_bind_prefixes`; secrets are host-file refs mounted read-only | Bind paths are host paths; no secret storage |
| D-21 | *(Docker track)* Revisions rendered to `/var/lib/control8/stacks/<project>/r<N>/compose.json`, mounted read-only into Parachute at the same path | Parachute DEP-011 drift check |
| D-27 | *(Docker track)* `pull_policy: missing` on every rendered service | Redeploys keep the digest Parachute pulled |
| D-22 | Argo assets (`managed_by=argo`) cannot have stacks/deployments (409), any runtime | One writer per workload |
| D-23 | API under `/api/v1`, JSON, errors as RFC 9457 `application/problem+json` | Standard, machine-friendly |
| D-24 | IDs: type prefix + 16 lowercase base32 chars from crypto/rand (e.g. `stk_k3...`) | Readable, no dependency |
| D-25 | Config via env `C8_*`, delivered with Kubernetes ConfigMap/Secret `envFrom`; every secret var also accepts `C8_*_FILE` | Owner decision; works in-cluster and in compose |
| D-26 | Deployment runs in a worker goroutine with a per-stack mutex and timeout (rollout timeout + 5 min); API returns 202 + deployment id | Simple; no queue system |
| D-28 | Control 8's own deployment is never managed by Control 8 | Avoid self-modification; upgrade by hand |
| D-30 | Kubernetes is a first-class runtime; `runtime` on stacks; `runtime.Driver` interface with `kubernetes` (v1) and `docker` (later) implementations | Owner decision; one deploy flow, two backends |
| D-31 | Kubernetes desired state = raw manifests; client-go server-side apply, field manager `control-8`, `Force: false`; prune by `control-8/stack` label + revision inventory; teardown by delete; **no Helm/Kustomize in v1** | Owner decision; smallest surface, exact diff of what is applied |
| D-32 | Dependencies `k8s.io/client-go`, `k8s.io/apimachinery`, `sigs.k8s.io/yaml`; `github.com/google/go-containerregistry` for digest poll | Standard libraries for the job; no kubectl binary |
| D-33 | **Bounded exception** to "never reimplement rollback": on Kubernetes, `managed_rollout` patches image refs in stored manifests, waits for health, and re-applies the previous revision on failure. No data/PVC snapshots. Docker stacks use Parachute only | Parachute cannot run on Kubernetes; owner decision |
| D-34 | Rollback = re-apply previous successful revision (SSA + prune), not `kubectl rollout undo` | Undo is kubectl client logic; re-applying stored state also reverts ConfigMaps etc. |
| D-35 | Kubernetes namespace = Control 8 namespace slug; created if missing, never deleted | 1:1 mapping (owner); deleting namespaces destroys unrelated data |
| D-36 | Ownership claim = `control-8/stack` label; existing objects without it, or with Argo tracking label/annotation, are refused; no adoption | One writer per workload, enforced before any write |
| D-37 | PVCs are never pruned or deleted by teardown | Mirrors compose `down` without `-v`; data safety |
| D-38 | Control 8 image runs as non-root UID 10001. Docker track adds `group_add: [<docker socket GID>]` in compose; socket access is root-equivalent anyway; docker-socket-proxy is a later hardening milestone | Owner decision; least privilege where it is real |
| D-39 | Parachute image `ghcr.io/kazzyapi/parachute:latest` for now. **TODO: pin a version/digest before release** | Owner decision; Parachute has no stable tag yet |
| D-40 | Prod access requires an active grant approved by **one** member of the target namespace's `approver_group` (the owning product team's OIDC group), never the requester. No global approver group and no fallback: a namespace without `approver_group` cannot get grants (409 `approver_group_missing`); prod namespaces must set it. Two-approver grants recorded as a future option | Owner decision; a global fallback would bypass product ownership |
| D-41 | Docker runtime + Parachute moved out of v1 to a later optional track; 04 kept as the contract | Owner: Control 8 is primarily a Kubernetes governance control plane |
| D-42 | A stack is prod if its namespace **or** target is prod (strictest wins) | No accidental prod deploy via a mislabelled namespace or cluster |
| D-43 | Grant max duration `C8_GRANT_MAX` (default 4h), hard cap `C8_GRANT_MAX_CAP` (default 24h); min 15 min; optional future start ≤ 7 days | Owner confirmed 4h/24h, configurable via env |
| D-44 | Grants are checked at request time **and** when the worker starts the deployment; a rollout already running completes or rolls back after expiry | Never cut a rollout in half; no new applies after expiry |
| D-45 | API tokens act with their owner's grants; owner mapped by `owner_sub` (OIDC `sub`) to a user who has logged in via OIDC (unknown or ambiguous sub ⇒ token rejected) | Grants stay per-person even for automation |
| D-46 | Digest poll on prod stacks only reports (`update_available`); applying needs a human with a grant | No automated prod change without a grant |
| D-47 | Grant notifications: one webhook `C8_WEBHOOK_URL`, HMAC-SHA256 signed, fire-and-forget with 3 retries | Minimal; covers Slack/Teams bridges |
| D-48 | Break-glass may deploy to prod without a grant; audited with `grant_bypass=break_glass` | Emergency path when the IdP (and so approval) is down |
| D-49 | Targets defined in `C8_TARGETS` (ConfigMap JSON); kubeconfig content via a Secret-injected env var named by `kubeconfig_env` (or a mounted file); synced into the DB at startup, `/targets` is read-only; removing a target that still has stacks ⇒ exit 2 | Owner decision: credentials and config come from Secrets/ConfigMaps, not the UI |
| D-50 | `namespace.approver_group` is set by `global_admin` only (audited) | Namespace admins must not pick their own approvers |
| D-51 | M0: `C8_TARGETS`/`C8_TOKENS` are only checked to be JSON lists at M0 (entry schemas validated by the milestones that use them); `C8_KUBECONFIG_*` is not parsed at startup; `C8_PUBLIC_URL` must be an absolute URL (compose defaults it to `http://localhost:8000`) | Smallest reading of "all vars parsed and validated" |
| D-52 | Dockerfile base is `gcr.io/distroless/static` pinned by digest; builder is `golang:1.23` (go.mod stays `go 1.23`) | Roadmap M0 task 6 |
| D-53 | UI = React + Vite + TypeScript SPA, TanStack Query, `react-router`; built to `web/dist` and embedded with `go:embed` (one image). Replaces htmx (D-06) | Owner: everything must be live; client-side cache + SSE is simpler than htmx polling fragments everywhere |
| D-54 | Live transport = Server-Sent Events (`GET /api/v1/events`), not WebSocket | One-way server→client is all we need; plain HTTP (works through proxies, cookie auth, `Last-Event-ID` resume built in); stdlib only |
| D-55 | Event bus is in-process with a bounded replay ring buffer; events are not persisted; **SQLite stays** (audit table is the durable log) | Single replica (ARC-014); no broker to run |
| D-56 | Observe via client-go shared (dynamic) informers per target, label selector `control-8/managed=true`, resync `C8_INFORMER_RESYNC`; list polling only as fallback when watch is unavailable; replaces `C8_INVENTORY_POLL` | Watch-driven live status; resync covers missed events |
| D-57 | API client generated from `openapi.yaml` with `openapi-typescript` + `openapi-fetch`; CI fails if the generated file differs | Parity with the API by construction (UI-001) |
| D-58 | A stub `web/dist/index.html` is committed so `go build`/`go test` need no Node; the image build always runs the Node stage | Keeps backend milestones and M0 unaffected |
| D-59 | Event payloads are full REST resource shapes, coalesced to ≤ 1/s per stack/deployment | SPA writes them straight into the query cache; bounded traffic |
| D-60 | M1: `role_binding.namespace_id` holds the namespace slug (no FK) until the `namespace` table lands in M2; `/me.namespaces` lists only namespaces named in bindings, `approver_for` and `active_grants` are empty until M3/M8 | Smallest reading with no namespace table yet |
| D-61 | M1: break-glass login is `POST /auth/break-glass {"password"}` → 204 + cookie only (no GET form until the SPA); source IP for AUTH-013 is `RemoteAddr` (no proxy headers) | Smallest reading of the `GET\|POST` route |
| D-62 | M1: audit rows are written for every state-changing call and every denied call (incl. denied reads and CSRF failures); successful reads are not audited. Token owner (AUTH-032) is resolved live per request, not stored | DOM-009 covers state changes; reads would flood the log |
| D-63 | `golang.org/x/crypto` pinned at v0.31.0 (argon2), the newest release compatible with `go 1.23` | Later versions force a newer go directive |
| D-64 | M2: `namespace.id` is `ns_...` but `role_binding.namespace_id`, `audit_event.namespace_id` and event namespaces keep holding the slug (D-60); deleting a namespace is 409 `namespace_not_empty` while it has assets (stacks arrive in M6); the audit query without `?namespace=` shows non-global callers only events of namespaces they can view; `external_url` is accepted only on Argo assets; `/me` still lists namespaces from bindings |
