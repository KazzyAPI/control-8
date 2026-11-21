# Control 8

Governance control plane for Kubernetes: namespaces, asset catalog, raw-manifest stacks (server-side apply), policy, environment tiers with time-boxed, elevated-approved prod access grants, audit. "The only door onto the cluster." Docker + **Parachute** (sidecar image) is a later, optional track.

## Where things are

- Intent: `docs/vision.md`
- Build contract: `docs/specs/` (start at `docs/specs/README.md`: glossary, v1 scope, non-goals, decision log)
- Kubernetes runtime: `docs/specs/08-kubernetes-runtime.md`; prod access grants: `docs/specs/09-prod-access-grants.md`; live events/SSE: `docs/specs/10-events.md`; SPA: `docs/specs/06-ui.md`
- API contract: `docs/specs/openapi.yaml`
- Parachute's own specs (reference only, separate repo, Docker track): `~/Desktop/parachute/docs/specs/`

## Hard rules

- Only `internal/runtime/*` drivers mutate targets; only `internal/executor` calls them. Kubernetes writes use server-side apply, field manager `control-8`, **never `Force: true`**.
- Never write a cluster object that lacks this stack's `control-8/stack` label or carries Argo tracking labels/annotations; no adoption. Argo-owned assets are register-only.
- Never prune or delete PVCs; never delete Kubernetes namespaces.
- Raw manifests only: no Helm, Kustomize or templating in v1. Kind allow-list in 08 (no Secrets, RBAC, CRDs, cluster-scoped kinds).
- Managed rollout (patch image, apply, health wait, re-apply previous revision) is the **only** rollback Control 8 implements, Kubernetes only, no data snapshots. Docker stacks (later) never get Control 8 rollback: they talk to Parachute over HTTP only (`GET /v1/status`, `POST /v1/approve`, `POST /v1/update`) plus `parachute.*` labels; never import Parachute code or read its state file.
- Prod stacks: every apply, teardown, image update and deployment approval checks for an active access grant (`grants.Require`) at request time **and** when the worker starts; record `grant_id` (or `grant_bypass=break_glass`) in the audit event. One approver, a member of the namespace's `approver_group` (owning product team), never the requester; no global approver group. A rollout already running finishes or rolls back after grant expiry; no new apply starts.
- Digest poll never applies to prod stacks on its own; it only reports.
- Safety profile and environment tier are shown everywhere a stack or deployment appears.
- Config, targets (`C8_TARGETS`), API tokens (`C8_TOKENS`, hashes only) and credentials come from Kubernetes ConfigMaps/Secrets as env (`envFrom`, optional `_FILE`). No UI/API endpoint creates or edits tokens, targets or credentials in v1. Secret content is never stored in the DB, logged, or returned by an API.
- Control 8 runs as non-root UID 10001.
- Every state-changing endpoint writes one audit event **and** publishes its event (`docs/specs/10-events.md`) after the DB commit. Event delivery always goes through the same authz checks as REST.
- The UI is a React + Vite + TS SPA in `web/`, embedded via `go:embed`; it uses only the public `/api/v1` API and `GET /api/v1/events` (generated client, no UI-only endpoints). No interval polling where an event exists.
- Cluster state comes from shared informers (watch); list polling is only a fallback/resync. Never list the API server per change.
- SQLite stays; events are in-memory only (replay buffer).
- Products (M17+, `docs/specs/12-products.md`): prod grants are scoped (target, namespace, product); the approver group comes from the product (only the built-in `unassigned` product falls back to the namespace group). Effective role = highest of global, namespace and product bindings. Effective tier = strictest of environment, namespace, target (`staging` is prod by default). Migrations are plan → apply in one transaction, audited per entity, never touch a cluster except explicit adoption items.
- Discovery (M19+, `docs/specs/11-discovery.md`) is read-only: separate informers, never Secrets/ConfigMaps, findings never block or write. Only an adopt deployment may label pre-existing objects (metadata only, planned uids, no pod-template change, generation must not change); never adopt tool-managed (Argo/Flux/Helm/operator) objects; never force.
- No new dependencies or scope beyond v1 without a decision-log row in `docs/specs/README.md`.

## Build workflow

1. Follow `docs/specs/07-roadmap.md` milestone by milestone (M0..M16 built; then M17, M17b, M18, M18b, M19..M21; Docker track D1..D3 only when the owner asks); one milestone per session.
2. Read the specs the milestone cites first; meet its acceptance criteria; cite requirement IDs in tests.
3. `make test` (and `make test-k8s` against kind where the milestone needs a cluster; `make web-test` / `make e2e` from M13) must pass before a milestone is done.
4. If a spec is ambiguous, choose the smallest reading and record it in the decision log.
5. Do not commit or push unless the owner asks.
