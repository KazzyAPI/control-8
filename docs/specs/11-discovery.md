# 11 Cluster discovery and adoption

v1 sees only what Control 8 deployed. From M19 it **picks up everything** in each cluster, read-only: every workload is discovered, classified by who manages it, mapped to a Control 8 namespace (tier, approver group), and checked against policy as **findings**. Ungoverned workloads can be explicitly **adopted** (labels added, spec untouched) or, if a tool owns them, **registered** (no cluster write). Adoption and registration always land in a **product** + application (12); findings roll up per product.

Control 8 cannot stop anyone with their own kubeconfig from creating workloads. Discovery and findings **report**; they never block. Blocking needs an admission policy in the cluster (DSC-060, optional hardening, not built).

## Discovery (M19)

| ID | Rule |
|---|---|
| DSC-001 | `C8_TARGETS` entries get `discovery: "on" \| "off"` (default `"on"`). Off = no discovery informers, no read model, no findings for that target. |
| DSC-002 | Per target, a **separate** dynamic shared informer factory (no label selector; the v1 managed-object informers, K8S-060, are untouched) watches cluster-wide: `apps/v1` Deployment, StatefulSet, DaemonSet; `batch/v1` Job, CronJob; `v1` Pod; `v1` Namespace. **Never** Secrets, ConfigMaps or any other kind. |
| DSC-003 | Discovery status per target: `off` \| `syncing` \| `on` \| `unavailable` (+ `reason`, e.g. `forbidden: list pods at cluster scope`). A missing RBAC permission or failed sync makes discovery `unavailable` (retried every `C8_TARGET_CHECK`) and **never** affects reachability, deploys or the v1 informers of that target. Shown in `Target.discovery` and published via `target.status`. |
| DSC-004 | Memory: every informer has a `cache.TransformFunc` that keeps only what discovery needs — `metadata` (name, namespace, uid, generation, labels, the annotations listed in DSC-011, ownerReferences; **no** `managedFields`), replica fields of `spec`/`status`, and from the pod template (or Pod spec) only container/initContainer names + images, `securityContext.privileged`/`allowPrivilegeEscalation`/`capabilities.add`, `hostNetwork`/`hostPID`/`hostIPC`, container `hostPort`s and `hostPath` volume paths. Pods that have an `ownerReference` are reduced to name + namespace + ownerReferences + phase/ready. |
| DSC-005 | Count cap: at most `C8_DISCOVERY_MAX` workloads per target (default 20 000) enter the read model; beyond it new workloads are not recorded, the target shows `truncated: true` with `seen` vs `recorded` counts, and a banner says so. Informer caches still hold (stripped) objects; the cap bounds DB rows, events and findings. |
| DSC-006 | Namespaces: `C8_DISCOVERY_SYSTEM_NAMESPACES` (default `kube-system,kube-public,kube-node-lease`, prefix `*` allowed) are class `system` (DSC-010) and collapsed into one "system" group in API/UI. `C8_DISCOVERY_EXCLUDE_NAMESPACES` (default empty) are not recorded at all (counted in `excluded`). Per-target overrides `discovery_system_namespaces` / `discovery_exclude_namespaces` in `C8_TARGETS`. |
| DSC-007 | Workload = a Deployment, StatefulSet, DaemonSet, CronJob, a Job **without** a CronJob owner, or a **bare Pod** (no ownerReferences). Objects owned by another watched built-in (ReplicaSet/Pods of a Deployment, Jobs of a CronJob, Pods of a Job) are folded into their owner (replica/ready counts), never listed separately. |
| DSC-008 | Informer changes enqueue the workload key; a debounced (1 s) recompute updates the read model row, classification, mapping and findings, and publishes `discovery.updated` only on a change. No API-server call per change (same rule as K8S-061). |

## Classification (M19)

Pure function `discovery.Classify(obj, namespaceInfo) -> (class, tool_ref)`, table-tested with fixture objects. First match wins:

| # | Class | Matches | `tool_ref` |
|---|---|---|---|
| 1 | `managed` | label `control-8/managed=true` **or** a `managedFields` entry with manager `control-8` (checked before the transform strips it) | stack id from `control-8/stack` |
| 2 | `argo` | label `argocd.argoproj.io/instance` or annotation `argocd.argoproj.io/tracking-id` | Argo app name (instance label, or app part of tracking id) |
| 3 | `flux` | any label with prefix `kustomize.toolkit.fluxcd.io/` or `helm.toolkit.fluxcd.io/` | `<kind>/<namespace>/<name>` from the Flux labels |
| 4 | `helm` | label `app.kubernetes.io/managed-by=Helm` or annotation `meta.helm.sh/release-name` | release name (+ `meta.helm.sh/release-namespace`) |
| 5 | `operator` | an ownerReference with `controller: true` to a kind that is not one of the DSC-007 built-ins | root owner `apiVersion/kind/name` (one hop; Control 8 does not fetch custom resources) |
| 6 | `system` | namespace is in the system set (DSC-006) | — |
| 7 | `ungoverned` | anything else | — |

| ID | Rule |
|---|---|
| DSC-010 | Classification precedence is the table order and is the only place class logic lives (UI and findings read `class`). |
| DSC-011 | Annotations retained by the transform: `argocd.argoproj.io/tracking-id`, `meta.helm.sh/release-name`, `meta.helm.sh/release-namespace`. Managed-by-`control-8` field manager is computed in the transform and kept as a boolean. |

## Namespace mapping and read model (M19)

| ID | Rule |
|---|---|
| DSC-020 | A Kubernetes namespace on a target maps to a Control 8 namespace by an explicit `namespace_mapping` row (target, k8s_namespace → namespace) if one exists, else **implicitly** to the Control 8 namespace whose slug equals it (K8S-010), else it is **unmapped**. Mappings are set by `global_admin` (`PUT /targets/{t}/namespace-mappings/{k8s_ns}`), audited (`namespace_mapping.set/delete`). |
| DSC-021 | Effective tier of a discovered workload = `prod` if the mapped Control 8 namespace **or** the target is prod (D-42 rule); unmapped ⇒ target tier. Mapped namespaces bring their `approver_group` and effective policy; unmapped ones use the global policy. |
| DSC-022 | Visibility: workloads in a mapped namespace are visible to its `viewer`s and up; unmapped and `system` workloads only to `global_admin` (same rule for findings and events). |
| DSC-024 | Product attribution of a discovered workload: the product of its linked application (adopted stack, or registered asset via DSC-051), else an explicit **assignment** (`POST /discovery/workloads/assign {workloads: [ids], product}` by `operator` on the product and `global_admin` or `admin` of the mapped namespace; stored by workload key so it survives restarts; audit `discovery.assign`), else none. Product role holders see their assigned/linked workloads and findings (in addition to DSC-022). |
| DSC-023 | Read model `discovered_workload` (02) is a persisted summary keyed by (target, k8s_namespace, kind, name), with `first_seen`, `last_seen`, `last_changed`. Rows whose object is gone are kept 24 h as `gone=true` (UI shows "deleted 3 min ago"), then purged. On startup the table is reconciled with the first full informer sync (rows not seen → `gone`). |

## Findings (M20)

Report-only governance checks on discovered workloads, recomputed with each read-model change.

| Type | Severity | Condition |
|---|---|---|
| `ungoverned_in_prod` | high | class `ungoverned`, effective tier `prod` |
| `ungoverned_non_prod` | info | class `ungoverned`, tier `non-prod` |
| `image_registry_not_allowed` | medium | Any image fails POL-R1 against the effective policy (any class except `system`) |
| `privileged_workload` | high | POL-R7 conditions (privileged, host namespaces, dangerous caps, hostPort) without `allow_privileged` (any class except `system`) |
| `host_path_mount` | medium | POL-R4: a hostPath outside `allowed_bind_prefixes` (any class except `system`) |
| `argo_unlinked` | low | class `argo` and no application with `managed_by=argo` and `external_ref` = the Argo app name on that target |

| ID | Rule |
|---|---|
| DSC-030 | Policy checks reuse `policy.Evaluate` rule functions read-only on the stripped object; no new rule logic. |
| DSC-031 | A finding is unique per (workload, type). States: `open` → `resolved` (condition gone or workload gone; `resolved_at` set) → `open` again if it reappears (same row, `reopened_count`). |
| DSC-032 | Acknowledge: `POST /findings/{id}/acknowledge {reason (10..500), expires_at? (≤ 90 d)}` by `admin` of the mapped namespace (`global_admin` for unmapped/system). Acknowledged findings stay listed with the reason and are excluded from counts and webhooks; at `expires_at` they return to `open` (sweeper, audit `finding.ack_expired`). `POST .../unacknowledge` reverts early. Both audited (`finding.acknowledge`, `finding.unacknowledge`). |
| DSC-033 | Webhook: a **new or reopened** `high` finding that is not acknowledged sends `finding.opened` through the grant webhook (GRT-040/041: same URL, HMAC, retries), payload `{event, at, finding}`. Nothing else is sent. |
| DSC-034 | Findings never cause a cluster write. |
| DSC-035 | Findings carry the workload's product (DSC-024) and roll up per product (product page Findings tab, `open_findings` in PRD-030). Product `admin`s may acknowledge findings of their product's workloads in addition to DSC-032. |

## Adoption (M21)

Turn ungoverned workloads into a Control 8 asset + stack **without restarting anything**.

| ID | Rule |
|---|---|
| DSC-040 | Only class `ungoverned` workloads of kinds Deployment, StatefulSet, DaemonSet, CronJob are adoptable; optionally with companion objects in the same namespace picked by kind/name from K8S-021 (Service, ConfigMap, PVC, ServiceAccount, Ingress, NetworkPolicy, HPA, PDB). Jobs, bare Pods, `system`, `managed`, and every tool class (`argo`, `flux`, `helm`, `operator`) → 409 `not_adoptable` / `tool_managed`. A `Secret` in the set → 422 `secret_not_adoptable`; Secrets referenced by the workloads are listed as warnings and stay unmanaged (K8S-023). |
| DSC-041 | **Plan** (`POST /discovery/adoptions`, no cluster write): fetch the live objects with GET (fresh, not the stripped cache), record their `uid` + `resourceVersion`, and **sanitise**: drop `status`, `managedFields`, `resourceVersion`, `uid`, `creationTimestamp`, `generation`, `selfLink`, `ownerReferences`, annotations `kubectl.kubernetes.io/last-applied-configuration` and `deployment.kubernetes.io/revision`, Service `clusterIP`/`clusterIPs`, PVC `volumeName` and `pv.kubernetes.io/*` annotations, and `spec.replicas` when an HPA targets the workload (warning). The result is the revision-1 manifest (`origin=adoption`). |
| DSC-042 | The plan runs the normal render (K8S-024) and `policy.Evaluate`. Violations block execution (DOM-003); the UI offers acknowledging the finding instead. The plan stores preview manifests, diff vs live, policy result, warnings, `requires_grant`, and expires after 30 min. |
| DSC-043 | **No restart**: adoption writes only object `metadata.labels` (the K8S-024 labels) — **never** pod-template labels. Revision 1 is stored with `template_labels=false`; the first later deploy adds template labels and therefore rolls pods (the deploy dialog says so). Before writing, a server-side dry-run of each patch must return the same `metadata.generation` as live; any increase → refuse `spec_change_detected`. |
| DSC-044 | **Field ownership**: other field managers owning fields under `spec` → refuse 409 `ownership_conflict` listing them, never force — **except** a fixed allow-list of manual-edit managers (`kubectl-client-side-apply`, `kubectl-edit`, `kubectl-create`, `kubectl-patch`, `kubectl-set`, `kubectl-scale`, `kubectl`, `before-first-apply`), whose ownership is migrated to `control-8` with client-go `csaupgrade.UpgradeManagedFields` (a `managedFields` patch, no spec change), so later deploys do not conflict (K8S-043). Controller-owned `status` is ignored. |
| DSC-045 | **Execute** (`POST /discovery/adoptions/{id}/execute {confirm_environment_tier, reason}`): creates the application under the plan's **product** (if new, `managed_by=control8`), stack (`runtime=kubernetes`, state `draft`), revision 1, and a deployment with `action=adopt`, then runs it through the normal worker: prod grant gate at request and at start (GRT-010..016), DOM-001, audit, `deployment.updated`/`stack.updated` events. The worker re-checks each object's `uid` (replaced object → `plan_stale`), migrates ownership (DSC-044), patches labels, and on success sets stack `active`, `desired_revision=1`. |
| DSC-046 | **Idempotent, resumable**: an object already carrying this stack's `control-8/stack` label is skipped; executing an already-executed plan returns its deployment; a failed adopt leaves stack `failed` and can be retried (`POST .../execute` again with a fresh plan if stale) or undone with release. K8S-041 accepts unlabelled objects only for `action=adopt` and only with the planned `uid`. |
| DSC-039 | A plan names a `product` (required) and an application (existing slug in that product, or new `{slug, name}`). Requires `operator` on the product and the namespace's `allowed_products` (PRD-012). The prod grant is for (target, namespace, product) (PRD-020). |
| DSC-047 | Namespace: the Kubernetes namespace must map to a Control 8 namespace whose slug equals it (K8S-010); otherwise 409 `namespace_mapping_mismatch` (create the Control 8 namespace with that slug first). |
| DSC-048 | Safety profile default for adoption is `as_is` (adoption itself rolls nothing). Choosing `managed_rollout` is allowed and takes effect from the first deploy. |
| DSC-049 | **Release** (undo adoption, also usable for any stack): `POST /namespaces/{ns}/stacks/{stack}/release {reason, confirm_environment_tier}` → deployment `action=release`: removes the `control-8/*` and `app.kubernetes.io/managed-by=control-8` labels from object metadata (JSON merge patch, manager `control-8`), leaves objects and their spec running, stack → `removed`. Prod-gated like teardown. The workloads reappear as `ungoverned`. |

## Register-only (M21)

| ID | Rule |
|---|---|
| DSC-050 | Workloads of class `argo`, `flux`, `helm`, `operator` (or `ungoverned` ones the team will not adopt) are registered as an application of a **product** with `managed_by` ∈ `argo` \| `flux` \| `helm` \| `operator` \| `external`, `external_ref` (= `tool_ref`) and `external_url`, via `POST /products/{p}/applications`; no cluster write, no stack (D-22 extended, PRD-005). |
| DSC-051 | Discovered workloads link to an application when (target, class, `tool_ref`) matches its `managed_by` + `external_ref`; the link is shown both ways and resolves `argo_unlinked`. |

## RBAC

| ID | Rule |
|---|---|
| DSC-055 | `deploy/kubernetes/rbac-discovery.yaml` (optional, separate): ClusterRole `control8-discovery` with only `get/list/watch` on `deployments, statefulsets, daemonsets` (apps), `jobs, cronjobs` (batch), `pods, namespaces` (core). No secrets, no configmaps, no writes. Bound with a ClusterRoleBinding to Control 8's identity. Without it discovery is `unavailable` (DSC-003). |
| DSC-056 | Adoption and release need `get` + `patch` on the adopted kinds (and `get` on chosen companions) in that namespace — exactly the v1 `control8` ClusterRole. Where the cluster is shared, bind that ClusterRole with **per-namespace RoleBindings** only in namespaces the platform admin allows Control 8 to adopt/deploy into; a plan in any other namespace fails its dry-run with `forbidden_by_rbac`. `docs/ops/prod-rbac.md` gains: the discovery role, the RoleBinding option, and that discovery read access is cluster-wide by design. |

## Optional hardening: blocking ungoverned workloads (documented, not built)

| ID | Rule |
|---|---|
| DSC-060 | `docs/ops/admission.md` documents an optional `ValidatingAdmissionPolicy` (Kubernetes ≥ 1.30) — and the equivalent Kyverno `ClusterPolicy` — that rejects create/update of Deployment/StatefulSet/DaemonSet/CronJob in prod namespaces unless the object has `control-8/managed=true` or an Argo/Flux/Helm marker, exempting Control 8's identity and system namespaces. Control 8 does not install or check it. |

```yaml
# docs/ops/admission.md (sketch)
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: control8-governed-workloads}
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [deployments, statefulsets, daemonsets]}
      - {apiGroups: [batch], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [cronjobs]}
    namespaceSelector: {matchLabels: {environment-tier: prod}}
  validations:
    - expression: >-
        request.userInfo.username == 'system:serviceaccount:control8:control8' ||
        ('labels' in object.metadata && (
          object.metadata.labels.exists(k, k == 'control-8/managed') ||
          object.metadata.labels.exists(k, k.startsWith('argocd.argoproj.io/') || k.startsWith('kustomize.toolkit.fluxcd.io/') || k.startsWith('helm.toolkit.fluxcd.io/')) ||
          object.metadata.labels.exists(k, k == 'app.kubernetes.io/managed-by' && object.metadata.labels[k] == 'Helm')))
      message: "Prod workloads must be deployed through Control 8 (or a GitOps tool)."
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata: {name: control8-governed-workloads}
spec: {policyName: control8-governed-workloads, validationActions: [Deny]}
```

## Audit and events

| Audit action | Actor | When |
|---|---|---|
| `namespace_mapping.set`, `namespace_mapping.delete` | user | DSC-020 |
| `finding.acknowledge`, `finding.unacknowledge`, `finding.ack_expired` | user / system | DSC-032 |
| `adoption.plan` | user/token | DSC-041 |
| `discovery.assign` | user | DSC-024 |
| `deployment.request` with `action=adopt` / `release` | user/token | DSC-045, DSC-049 (plus the usual deployment events) |

Findings opening/resolving are not audited (they are observations, not actions); they publish events.

| Event | Payload | Published when |
|---|---|---|
| `discovery.updated` | `DiscoveredWorkload` (`gone: true` on delete) | Read-model row change, coalesced ≤ 1/s per workload |
| `discovery.finding` | `Finding` | Finding opened, resolved, reopened, acknowledged, ack expired |

Discovery status changes ride on `target.status` (`Target.discovery`).

## Failure modes

| Situation | Behaviour |
|---|---|
| Discovery RBAC missing | `discovery: unavailable` + reason; everything else on the target unaffected |
| Huge cluster | Stripped caches; read model capped (`truncated`); exclude namespaces to reduce |
| Target unreachable | Rows keep last state; `last_seen` stops; findings stay open; nothing marked `gone` until a successful resync |
| Live object changed between plan and execute | `plan_stale` (uid replaced) or new dry-run on execute fails → refuse, re-plan |
| Someone else's manager owns spec fields | `ownership_conflict`, no force (DSC-044) |
| Patch would bump generation | `spec_change_detected`, nothing written (DSC-043) |
| Adopt interrupted midway | Stack `failed`, partial labels; retry is idempotent, release removes labels |
