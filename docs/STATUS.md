# Control 8 — v1 status (M0–M16)

Written at the end of the first autonomous build. All of M0–M16 is on `main`; CI (go, k8s on kind, web, image) is green on the last commit.

## What works (verified in CI on a real kind cluster)

- Image builds, runs as uid 10001, serves `/healthz` and the embedded SPA with CSP headers.
- Image loads into kind; `deploy/kubernetes` applies; Deployment goes Ready.
- `scripts/smoke.sh` end to end: break-glass login → namespace (non-prod) + asset + stack → deploy → workload healthy → objects carry `control-8` labels → prod deploy via audited break-glass bypass → teardown.
- Kubernetes driver (server-side apply, no force, prune and teardown never delete PVCs or the namespace), informers/inventory/drift, rollout health, kind-based driver tests.
- SPA: live read screens and write flows; web typecheck, Vitest, Playwright (with axe) in CI.

## Verified only with fakes / mocks (no real cluster or IdP run)

- Image updates, automatic rollback and the `rollback_failed` freeze (fake driver table tests; kind test exists but see CI for what actually ran).
- Digest poll (in-process registry, stubbed resolver).
- Prod access grants: full state machine, sweeper, webhook and approver rules are covered by unit tests with a fake clock; **never exercised with a real OIDC login**. The smoke test has no `403 grant_required` leg (needs a non-break-glass principal).
- Approver/requester live-unlock UI flow runs against in-memory mocks (D-97).
- Authentik OIDC: `docs/ops/authentik.md` is written but **not run** against a real Authentik.
- Release workflow (`.github/workflows/release.yaml`, pushes to `ghcr.io/kazzyapi/control-8` on `v*` tags): never triggered. No tags were created.

## Not built (by design)

Docker + Parachute track (D1–D3), Helm/Kustomize, Postgres/HA (SQLite, single replica), streaming deployment logs.

## Deploy to a cluster

See `docs/ops/install.md`. Short version: build or pull the image, create the ConfigMap/Secret from the samples in `deploy/kubernetes/` (OIDC issuer/client, `C8_TARGETS`, break-glass hash from `control8 hash-password`, token hashes from `control8 gen-token`), `kubectl apply -f deploy/kubernetes/`, and put a reverse proxy in front with buffering disabled for `/api/v1/events`. Set each namespace's `approver_group` (prod namespaces require one).

## Known issues / follow-ups

- The Kubernetes driver reports any Forbidden API error as `forbidden_by_rbac`, including a write into a Terminating namespace. Misleading; should distinguish.
- Control 8 holds cluster credentials: anyone with their own prod kubeconfig bypasses it. Pair with cluster RBAC (`docs/ops/prod-rbac.md`).
- Pin the Parachute image tag before the Docker track starts.
- Repo is public; review before sharing widely.

## Open questions for the owner

1. Run the Authentik manual test (`docs/ops/authentik.md`) and confirm group → role and approver-group mapping with real users.
2. One approver vs two for prod grants (v1 = one).
3. Cut a `v0.1.0` tag to exercise the release workflow when ready.
4. Decide whether to start the Docker + Parachute track or more Kubernetes hardening next.
