# Control 8 — product vision (draft)

Control 8 is a **governance control plane for Kubernetes**: the one place teams deploy through, with policy, audit, and time-boxed elevated access to prod. It is a **separate codebase** from **Parachute**; a Docker + Parachute track (Parachute as a sidecar image, integrated over HTTP and labels) is a later, optional addition.

## One-liner

**Control 8 is the only door onto your clusters: anyone can ship to non-prod, prod needs an approved, time-boxed grant, and every change is audited.**

## Role split

| Layer | Responsibility | Explicitly not |
|-------|----------------|----------------|
| **Control 8** | **Governed deploy and operations** on Kubernetes: namespaces, asset catalog, raw-manifest stacks applied with server-side apply, policy, prod access grants, inventory, audit; a small **managed rollout** for image updates (health wait + automatic rollback) | Image builds, artifact storage, full CI/CD, Helm/Kustomize, data snapshots, progressive delivery |
| **Argo CD** | GitOps writer for the workloads it owns | — (Control 8 only registers and links them) |
| **Parachute** (later, Docker track) | Safe **image** lifecycle on Docker hosts: detect digest, snapshot, update, verify, rollback | Kubernetes; first deploy |

Images are produced elsewhere (CI → registry). Control 8 **registers** and **binds** images to stacks and rolls new digests forward itself on Kubernetes.

## Mandatory path and governance

- No application is meant to reach a cluster **without** going through Control 8. Control 8 is the only holder of prod write credentials; humans get read-only cluster access (recommended RBAC hardening). Anyone with their own write kubeconfig can still bypass it; Control 8 detects that as drift and ownership conflicts.
- **Governance** = system of record for what **should** and **does** exist:
  - Inventory: targets (clusters), namespaces, stacks, workloads, image ref + digest as deployed
  - Desired state: manifest revisions, safety profile, deploy approvals
  - Policy: allowed registries, namespace allow-list, required labels, no privileged/hostPath by default, protected databases → 4-eyes
  - Access: environment tiers; prod needs an elevated-approved, time-boxed access grant
  - Audit: who deployed what, under which grant
- **Assets** = registered applications and deployment metadata, not a blob or artifact store.

## Environment tiers and prod access

Every namespace and target is `prod` or `non-prod`. Non-prod: a normal namespace role deploys freely. Prod: nobody deploys by default; a user requests a **time-boxed access grant** (scope, reason, optional ticket, duration), a member of the **owning product team's approver group** approves it (never the requester), and it expires on its own. Break-glass exists for when the IdP is down and is loudly audited.

## Products, environments and the whole cluster

Teams think in **products**: a product owns several applications, its own environments (dev, uat, staging, prod — staging and prod are prod tier) and its own approver group, and the product page shows every app across every environment live. Control 8 also **sees everything in each cluster**, not only what it deployed: workloads are discovered read-only, classified (Control 8, Argo, Flux, Helm, operator, system, ungoverned), mapped to products and environments, and checked against policy as findings — "ungoverned in prod" is the headline number. Findings report; they cannot stop someone with their own kubeconfig (an admission policy can, documented as optional hardening). Ungoverned workloads can be adopted into a product without restarting anything; tool-managed ones are registered, never taken over. A migration assistant moves legacy apps into products and environments with a reviewable, reversible plan.

## Safety profiles

Chosen per revision; must be obvious in UI and API:

| Mode | Behaviour |
|------|-----------|
| **Managed rollout** (Kubernetes) | Apply, wait for rollout health, automatically re-apply the previous revision on failure; image updates (manual or opt-in digest poll) go through the same path. No data snapshots |
| **As-is** | Apply and report; no automatic rollback — experiments or deliberate "fail loud" stacks |
| **With Parachute** (Docker, later) | Compose with `parachute.*` labels; Parachute owns digest upgrades, snapshot, rollback |

Managed rollout is a conscious, bounded exception to "Control 8 does not do rollback": Parachute cannot run on Kubernetes, so Control 8 does the minimum (patch image, apply, health, revert). Anything bigger (canary, traffic shifting, data restore) is out of scope.

## Authentication

No custom identity product. **OIDC** with **Authentik** as the reference IdP (others like Keycloak, Authelia should work), plus optional **local break-glass**. IdP groups → namespace roles; each namespace names its owning product group as approvers.

## API and UI

- **Headless-first**: every UI action has an equivalent REST API; agents use scoped tokens.
- Config, cluster credentials and API tokens are delivered as Kubernetes Secrets/ConfigMaps (env), not entered in the UI.
- **v1**: a live web frontend (React SPA, embedded in the single binary) on top of that API. Inventory, pod/rollout status, drift, deploy progress, grant requests/approvals/expiry and the audit feed update in real time over Server-Sent Events; cluster state is watched with Kubernetes informers, not polled.

## Relationship to other tools

### Portainer / Lens / Rancher
Adjacent, not identical. They are broad cluster admin UIs. Control 8 is a mandatory deploy path + governance + prod access control. Avoid becoming a universal cluster IDE (exec, logs, scale buttons, CRD browsers = clone).

### Argo CD
Work alongside, not replace.

| Workload | Control 8 | Argo CD |
|-------|-----------|-----------|
| Argo-managed (Kubernetes) | Register, link, metadata; **no writes** | Writer |
| Control 8–managed (Kubernetes) | Deploy (server-side apply, field manager `control-8`), govern, managed rollout | Not involved |
| Control 8–managed (Docker, later) | Deploy + govern; Parachute per safety profile | — |

One **writer per workload**. Control 8 labels what it owns (`control-8/stack`) and refuses to apply over objects owned by anything else, Argo included.

## Repository strategy

Sibling repos (`control-8` + `parachute`), integrate by **contract** (OpenAPI, label conventions), not shared Go packages. Repo: https://github.com/KazzyAPI/control-8

## Naming

"Control 8" is generic; check domains, npm, container registries, trademarks before launch.

## Open risks

- **Bypass**: personal kubeconfigs with write rights skip Control 8 unless cluster RBAC is hardened.
- **Two rollback owners**: managed rollout on Kubernetes and Parachute on Docker must stay clearly separated by runtime.
- **Scope creep**: Helm, progressive delivery, cluster admin features → converges on Argo/Rancher unless deliberately constrained.
- **Approval fatigue**: grants that are too short or too slow push people to break-glass or personal kubeconfigs.

## Next steps

1. Build M0–M13 (`docs/specs/07-roadmap.md`): Kubernetes + auth + audit, then grants, then managed rollout, then UI.
2. Authentik manual test with a product approver group (e.g. `shop-approvers`).
3. Docker + Parachute track after v1, if still wanted.
