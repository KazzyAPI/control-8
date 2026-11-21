# 01 Architecture

## Components

```
    browser (React SPA)  /  agent (token)
          REST + SSE /api/v1/events
                      |
                HTTPS (reverse proxy or Ingress, operator-provided)
                      |
+---------------------v----------------------+
| control8 (container or Pod, UID 10001)      |
|  http: /api/v1, /api/v1/events (SSE), /auth,|
|        embedded SPA (/)                     |
|  event bus (in-process, replay buffer)      |
|  service layer (one per domain area)        |
|  grants (prod gate, sweeper, webhook)       |
|  policy engine (pure functions)             |
|  executor (deployment worker, per-stack lock)|
|    -> runtime.Driver                        |
|         kubernetes: client-go SSA  ---------+----> Kubernetes API (targets: kubeconfig / in-cluster SA)
|         docker (later): compose CLI --------+----> Docker engine  (later; + Parachute sidecar, 04)
|  informers per target (watch) -> inventory  |
|    (services, drift, health) -> event bus   |
|  digest poller (go-containerregistry)  -----+----> registries (read)
|  SQLite /var/lib/control8/control8.db       |
+---------------------------------------------+
```

| Component | Package | Responsibility |
|---|---|---|
| `cmd/control8` | main | Load config, open DB, run migrations, start HTTP + background loops |
| `internal/config` | | Parse `C8_*` env (ConfigMap/Secret `envFrom`), optional `_FILE` variants, `C8_TARGETS`, `C8_TOKENS` |
| `internal/store` | | SQLite access, embedded SQL migrations |
| `internal/auth` | | OIDC, sessions, break-glass, static tokens, role + approver-group resolution |
| `internal/grants` | | Access grants, prod gate `grants.Require`, expiry sweeper, webhook sender (09) |
| `internal/policy` | | Pure evaluation of policy rules against a revision |
| `internal/runtime` | | `Driver` interface (below) and shared types |
| `internal/runtime/kubernetes` | | Manifest parse/render, SSA apply, prune, teardown, ownership guard, health; per-target shared informers (08) |
| `internal/runtime/docker` | | *(later)* Compose normalize/render, `docker compose up/down`, Engine SDK reads (04) |
| `internal/executor` | | Deployment worker: state machine, per-stack lock, grant re-check, calls `Driver`; managed-rollout rollback orchestration |
| `internal/inventory` | | Consumes `Driver.Watch` changes (debounced per stack), writes `service` rows, drift, health; publishes `stack.updated` |
| `internal/events` | | Event bus, replay buffer, SSE handler, per-viewer filter (10) |
| `internal/digest` | | Opt-in registry digest poll for `managed_rollout` stacks (K8S-051) |
| `internal/parachute` | | *(later)* HTTP client for Parachute, poller, mapping (04) |
| `internal/audit` | | Append-only audit writer + query |
| `internal/api` | | REST handlers (JSON) |
| `internal/web` | | Serves the embedded SPA (`web/dist` via `go:embed`) with SPA fallback and cache headers (UI-005) |
| `web/` | | React + Vite + TypeScript SPA source; generated API client in `web/src/api/schema.d.ts` |

### Runtime driver

```go
type Driver interface {
    // Parse + validate + render user input into the stored rendered form. No target I/O.
    Render(in RevisionInput, ctx RenderContext) (Rendered, []policy.Violation, error)
    // Pre-write checks against the target (reachability, ownership, dry-run).
    Precheck(ctx context.Context, s Stack, r Rendered) error
    // Apply desired state; returns once applied (not once healthy).
    Apply(ctx context.Context, s Stack, r Rendered) (Output, error)
    // Health of the applied revision (Kubernetes: 08 criteria; Docker: compose --wait result).
    Health(ctx context.Context, s Stack, r Rendered) (RolloutStatus, error)
    // Delete everything not in r's inventory (Kubernetes); no-op for Docker (--remove-orphans).
    Prune(ctx context.Context, s Stack, r Rendered) error
    Teardown(ctx context.Context, s Stack) (Output, error)
    Observe(ctx context.Context, t Target) ([]ObservedService, error) // full list (resync / fallback)
    // Start watching a target; emits stack IDs whose observed state changed. Kubernetes: shared informers.
    Watch(ctx context.Context, t Target, changed chan<- string) error
}
```

Selected by `stack.runtime`. v1 ships `kubernetes`; `docker` lands in the Docker track. A `C8_TARGETS` entry whose kind has no enabled driver → exit 2 (`runtime_not_enabled`).

| ID | Rule |
|---|---|
| ARC-001 | The SPA talks only to the public REST API and SSE stream (UI-001); there are no UI-only endpoints and no business logic in `internal/web`. |
| ARC-002 | Control 8 MUST NOT import Parachute Go packages or read Parachute's state file. Integration is HTTP + labels only (Docker track). |
| ARC-003 | Only `internal/runtime/*` drivers mutate targets. Kubernetes: K8S-000. Docker: only `docker compose -p c8-*`. Only `internal/executor` calls mutating driver methods. |
| ARC-004 | Every prod-gated service method calls `grants.Require(principal, stack)` before any work and once more in the worker (GRT-012). |
| ARC-005 | Every state change publishes its event (EVT-004) after commit; handlers never publish before the DB write succeeds. |

## Tech stack

| Concern | Choice |
|---|---|
| Language | Go 1.23+ |
| DB | SQLite, `modernc.org/sqlite`, WAL, `foreign_keys=ON`, embedded `NNNN_name.sql` migrations tracked in `schema_migrations` |
| HTTP | stdlib `net/http` (method+path patterns) |
| Kubernetes | `k8s.io/client-go` (dynamic client + discovery/RESTMapper), `k8s.io/apimachinery`, `sigs.k8s.io/yaml` |
| Registry | `github.com/google/go-containerregistry` (digest lookups only) |
| Docker (later) | `docker` CLI + compose v2 in the image; `github.com/docker/docker/client` for reads |
| OIDC | `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2` |
| Passwords | `golang.org/x/crypto/argon2` |
| Live updates | In-process bus + Server-Sent Events (stdlib `net/http` flushing; no library) |
| Frontend | React 18, Vite, TypeScript (strict), TanStack Query, `react-router`, `openapi-typescript` + `openapi-fetch`, plain CSS (custom properties); Node 22 LTS for builds only |
| Tests | `testing`, `httptest`, `github.com/oauth2-proxy/mockoidc`, `k8s.io/client-go/dynamic/fake`; build tag `k8s` for tests against a real cluster (kind or k3d); build tag `integration` for Docker-backed tests (later); Vitest + Testing Library and Playwright (+ `@axe-core/playwright`) for the SPA |
| Logging | `log/slog` JSON |

No other runtime (Go) or frontend (`web/package.json`) dependencies without a decision-log entry.

## Configuration (`C8_*`)

| Var | Default | Meaning |
|---|---|---|
| `C8_LISTEN` | `:8000` | HTTP listen address |
| `C8_DATA_DIR` | `/var/lib/control8` | DB (+ rendered stacks, Docker track) |
| `C8_PUBLIC_URL` | required | External base URL (OIDC redirect, cookies) |
| `C8_OIDC_ISSUER`, `C8_OIDC_CLIENT_ID` | unset | OIDC (ConfigMap); login disabled when unset |
| `C8_OIDC_CLIENT_SECRET` | unset | Secret |
| `C8_OIDC_GROUPS_CLAIM` | `groups` | Claim holding group names |
| `C8_BREAKGLASS_HASH` | unset | Secret: argon2id PHC string; break-glass disabled when unset |
| `C8_BOOTSTRAP_ADMIN_GROUP` | unset | IdP group made `global_admin` on startup if no global admin binding exists (AUTH-021) |
| `C8_GRANT_MAX` | `4h` | Default max grant duration |
| `C8_GRANT_MAX_CAP` | `24h` | Hard cap; `C8_GRANT_MAX` must be ≤ it |
| `C8_TARGETS` | `[]` | ConfigMap: JSON list of targets (K8S-001); M19 adds per-target `discovery` (`on`/`off`, default `on`), `discovery_system_namespaces`, `discovery_exclude_namespaces` (DSC-001/006) |
| `C8_KUBECONFIG_*` | unset | Secret: kubeconfig contents referenced by a target's `kubeconfig_env` |
| `C8_TOKENS` | `[]` | Secret: JSON list of static token definitions with hashes (AUTH-031) |
| `C8_WEBHOOK_URL` / `C8_WEBHOOK_SECRET` | unset | Grant notifications (GRT-040); secret in Secret |
| `C8_INFORMER_RESYNC` | `10m` | Informer resync period (full relist fallback) |
| `C8_TARGET_CHECK` | `30s` | Reachability check (`ServerVersion`) per target, also used as the list-poll interval when watch is unavailable |
| `C8_EVENTS_REPLAY` | `10000` | SSE replay buffer size (events) |
| `C8_EVENTS_HEARTBEAT` | `15s` | SSE heartbeat interval |
| `C8_EVENTS_MAX_CONNS` | `500` | Max concurrent SSE streams |
| `C8_ROLLOUT_TIMEOUT` | `5m` | Default health wait (revision may set 30 s..30 min) |
| `C8_DIGEST_POLL` | `15m` | Digest poll interval; `0` = off |
| `C8_REGISTRY_AUTH` | unset | Secret: Docker `config.json` contents for private registry digest lookups |
| `C8_DOCKER_HOST` | unset | *(Docker track)* e.g. `unix:///var/run/docker.sock`; seeds target `local` when set |
| `C8_PARACHUTE_URL`, `C8_PARACHUTE_TOKEN`, `C8_PARACHUTE_POLL`, `C8_PARACHUTE_SCOPE` | unset / unset / `30s` / `control-8` | *(Docker track)* 04 |
| `C8_ENVIRONMENT_PRESETS` | `[{"name":"dev","tier":"non-prod"},{"name":"uat","tier":"non-prod"},{"name":"staging","tier":"prod"},{"name":"prod","tier":"prod"}]` | M17: environment presets offered by the wizard (PRD-041/042) |
| `C8_MIGRATION_UNDO_WINDOW` | `168h` | M17b: undo window for migration plans (MIG-007) |
| `C8_DISCOVERY_MAX` | `20000` | M19: max discovered workloads per target in the read model (DSC-005) |
| `C8_DISCOVERY_SYSTEM_NAMESPACES` | `kube-system,kube-public,kube-node-lease` | M19: class `system` namespaces (DSC-006) |
| `C8_DISCOVERY_EXCLUDE_NAMESPACES` | empty | M19: namespaces not recorded (DSC-006) |
| `C8_LOG_LEVEL` | `info` | |

Every secret var (`C8_OIDC_CLIENT_SECRET`, `C8_BREAKGLASS_HASH`, `C8_TOKENS`, `C8_KUBECONFIG_*`, `C8_WEBHOOK_SECRET`, `C8_REGISTRY_AUTH`, `C8_PARACHUTE_TOKEN`) is read from env, or from a file when `<VAR>_FILE` is set (both set → exit 2). Invalid config MUST exit with code 2 and name the variable (value redacted when secret). Nothing in config is editable through the UI/API.

## Deployment of Control 8 itself

Neither way is managed by Control 8 (D-28). Config arrives as env from a ConfigMap and a Secret (`envFrom`); nothing is entered through the UI.

**In-cluster (reference)** — `deploy/kubernetes/` (sample, trimmed):

```yaml
apiVersion: v1
kind: ConfigMap
metadata: {name: control8-config, namespace: control8}
data:
  C8_PUBLIC_URL: https://control8.example.com
  C8_OIDC_ISSUER: https://authentik.example.com/application/o/control8/
  C8_OIDC_CLIENT_ID: control8
  C8_BOOTSTRAP_ADMIN_GROUP: platform-admins
  C8_GRANT_MAX: 4h
  C8_GRANT_MAX_CAP: 24h
  C8_TARGETS: |
    [{"name": "home", "kind": "kubernetes", "environment_tier": "non-prod", "kube_auth": "in_cluster"},
     {"name": "prod-eu", "kind": "kubernetes", "environment_tier": "prod", "kube_auth": "kubeconfig",
      "kubeconfig_env": "C8_KUBECONFIG_PROD_EU", "kube_context": "prod-eu"}]
---
apiVersion: v1
kind: Secret
metadata: {name: control8-secrets, namespace: control8}
type: Opaque
stringData:
  C8_OIDC_CLIENT_SECRET: "<from Authentik>"
  C8_BREAKGLASS_HASH: "$argon2id$v=19$..."        # optional
  C8_KUBECONFIG_PROD_EU: |
    apiVersion: v1
    kind: Config
    # ... prod cluster credentials for Control 8's own user only
  C8_TOKENS: |
    [{"name": "ci-shop", "owner_sub": "8f2c1d0a-example-sub", "namespace": "shop", "role": "operator",
      "sha256": "<control8 gen-token output>", "expires_at": "2027-06-30T00:00:00Z"}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: control8, namespace: control8}
spec:
  replicas: 1                                      # ARC-014
  strategy: {type: Recreate}
  selector: {matchLabels: {app: control8}}
  template:
    metadata: {labels: {app: control8}}
    spec:
      serviceAccountName: control8                 # bound by rbac.yaml (K8S-005), used by kube_auth=in_cluster
      securityContext: {runAsUser: 10001, runAsGroup: 10001, fsGroup: 10001, runAsNonRoot: true}
      containers:
        - name: control8
          image: ghcr.io/kazzyapi/control-8:v0.1.0
          envFrom:
            - configMapRef: {name: control8-config}
            - secretRef: {name: control8-secrets}
          ports: [{containerPort: 8000}]
          readinessProbe: {httpGet: {path: /readyz, port: 8000}}
          livenessProbe: {httpGet: {path: /healthz, port: 8000}}
          securityContext: {readOnlyRootFilesystem: true, allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}}
          volumeMounts: [{name: data, mountPath: /var/lib/control8}]
      volumes: [{name: data, persistentVolumeClaim: {claimName: control8-data}}]
```

Plus `control8-data` PVC, Service, ServiceAccount and `rbac.yaml`. Changing targets, tokens or secrets = edit the ConfigMap/Secret and roll out the Deployment.

**Compose** (`deploy/compose.yaml`), Control 8 outside the clusters, same variables via `env_file`:

```yaml
name: control8
services:
  control8:
    image: ghcr.io/kazzyapi/control-8:${C8_VERSION:-dev}
    restart: unless-stopped
    user: "10001:10001"
    read_only: true
    env_file: [./control8.env, ./control8.secrets.env]   # same keys as the ConfigMap / Secret above
    volumes: [control8-data:/var/lib/control8]
    ports: ["${C8_PORT:-8000}:8000"]
volumes: {control8-data: {}}
```

| Rule | |
|---|---|
| ARC-010 | The image runs as UID/GID 10001 (`USER 10001`); `/var/lib/control8` is created and owned by 10001 in the Dockerfile so named volumes inherit it (D-38). |
| ARC-011 | Credentials (kubeconfigs, OIDC secret, token hashes, webhook secret, Parachute token) come from Secret-injected env (or files), are held in memory only, never logged, never stored in the DB, never returned by any API. |
| ARC-012 | Control 8's own deployment is not managed by Control 8 (D-28); upgrade by editing the version. |
| ARC-013 | Control 8 starts and serves its API when any target, the registry or (later) Parachute is down; affected features degrade (08, 04 failure modes). |
| ARC-014 | Single replica only (SQLite + in-process locks). |

### Docker track additions (later)

When the Docker runtime lands, compose gains the socket and the Parachute sidecar:

```yaml
  control8:
    group_add: ["${DOCKER_GID:?set DOCKER_GID (stat -c %g /var/run/docker.sock)}"]
    environment: {C8_DOCKER_HOST: unix:///var/run/docker.sock, C8_PARACHUTE_URL: http://parachute:8080}
    volumes: [/var/run/docker.sock:/var/run/docker.sock]
  parachute:
    image: ghcr.io/kazzyapi/parachute:latest   # TODO: pin version/digest before release (D-39)
    # env, secrets, volumes, network as in 04
```

Socket access is root-equivalent on the host regardless of UID; non-root limits only what a compromised process can do outside the socket. A later milestone puts `docker-socket-proxy` in front (D-38).

## Ownership boundary

| Aspect | Control 8 | Argo CD | Parachute (Docker track) |
|---|---|---|---|
| Asset registration, namespaces, policy, grants, audit of deploys | **Writer** | - | - |
| Control 8-owned Kubernetes stacks: manifests, image refs, prune, teardown | **Writer** (field manager `control-8`) | - | - |
| Image updates + rollback on `managed_rollout` stacks | **Writer** (bounded, D-33) | - | - |
| Argo-owned Kubernetes workloads | Register, link, metadata; **no writes** | **Writer** | - |
| Docker compose structure, image tag, teardown (later) | **Writer** | - | - |
| Image digest upgrades on `parachute` Docker stacks (later) | Reads/approves only | - | **Writer** |

## Writer rules

| ID | Rule | Enforcement |
|---|---|---|
| ARC-020 | Control 8 changes a stack only by applying a whole revision or tearing it down. An image update is a new revision (K8S-052). No per-workload restart/scale/pull action. | No endpoint exists |
| ARC-021 | Kubernetes: never write an object that is not ours or is Argo's; never force-apply. | K8S-041..043 |
| ARC-022 | Changing safety profile requires a new revision + deployment. | Revision flow |
| ARC-023 | Argo-managed assets have no stacks. | 409 `argo_managed` |
| ARC-024 | User-supplied `control-8/*` (Kubernetes) or `control8.*`/`parachute.*` (Docker) labels are rejected; Control 8's template is the only source. | Policy POL-R6 |
| ARC-025 | Prod stacks: no apply, teardown or image update without an active grant (or break-glass). | ARC-004, 09 |
| ARC-026 | *(Docker track)* Parachute-specific writer rules: 04 (tag refs only, no digest action, `parachute_busy` guard). | 04 |
