# 08 Kubernetes runtime

Kubernetes is the primary deploy target (v1). A stack has `runtime` `kubernetes` (this file) or, in the later Docker track, `docker` (compose, 04). Desired state on Kubernetes is **raw manifests**, applied with client-go **server-side apply** (field manager `control-8`), pruned by label + inventory, torn down by delete. No Helm, no Kustomize (D-31). Parachute does not run on Kubernetes; Docker stacks keep using Parachute only.

**K8S-000** Only `internal/runtime/kubernetes` writes to a cluster, only with field manager `control-8`, only to objects that carry this stack's `control-8/stack` label (or do not exist yet).

## Targets

| ID | Rule |
|---|---|
| K8S-001 | Targets are defined in `C8_TARGETS` (ConfigMap JSON: `name`, `kind`, `environment_tier`, `kube_auth`, `kubeconfig_env` or `kubeconfig_path`, `kube_context`). `kube_auth` = `in_cluster` (client-go `rest.InClusterConfig()`, Control 8 running as a Pod) or `kubeconfig` (content from the Secret-injected env var named by `kubeconfig_env`, or a mounted file at `kubeconfig_path`). The DB stores the reference, never the content. |
| K8S-002 | On startup targets are synced into the DB by name (audit `target.config_sync`, system). `/targets` is read-only; there is no create/update/delete endpoint in v1. A configured target removed while non-`removed` stacks reference it → exit 2 `target_in_use`. Tier changes apply at the next rollout and are audited in the sync event. |
| K8S-003 | Reachability = `ServerVersion()` every `C8_TARGET_CHECK` (30 s, timeout 10 s) **and** informer watch health: a target is `reachable=false` when the check fails or every informer's watch has been failing for > 30 s. Error text stored (redacted); every change publishes `target.status` (10). |
| K8S-004 | `stack.runtime` must equal `target.kind`; immutable. Create with a mismatch → 400 `runtime_target_mismatch`. |
| K8S-005 | Required RBAC is shipped as `deploy/kubernetes/rbac.yaml`: ClusterRole with `get/list/watch/create/patch/delete` on the K8S-021 kinds, `get/list/watch` on `pods`, `get/create` on `namespaces`; no `escalate`, no `bind`, no other cluster-scoped writes. `watch` is needed for informers (K8S-060). |

## Namespace mapping

| ID | Rule |
|---|---|
| K8S-010 | Kubernetes namespace of a stack = its Control 8 `namespace.slug` (1:1, immutable because the slug is). |
| K8S-011 | On first apply, Control 8 creates the Kubernetes namespace if missing (labels `app.kubernetes.io/managed-by=control-8`, `control-8/namespace=<slug>`). An existing namespace is used as-is. Control 8 never deletes Kubernetes namespaces. |
| K8S-012 | The mapped namespace must pass policy POL-R13 (allow-list; `kube-*` and `default` always refused). |
| K8S-013 | Several stacks may share one Kubernetes namespace; they are separated by the `control-8/stack` label. Name collisions are ownership conflicts (K8S-041). |

## Manifests (revision input)

| ID | Rule |
|---|---|
| K8S-020 | Revision field `manifests`: multi-document YAML, parsed locally with `sigs.k8s.io/yaml` into unstructured objects. Each doc needs `apiVersion`, `kind`, `metadata.name`. `kind: List`, empty docs and duplicates (same group/kind/name) → 422 `invalid_manifests`, no revision. Server-side validation happens as a dry-run apply at deploy precheck. |
| K8S-021 | Allowed kinds (fixed in code, v1): `Deployment`, `StatefulSet`, `DaemonSet`, `Job`, `CronJob`, `Service`, `ConfigMap`, `PersistentVolumeClaim`, `ServiceAccount`, `Ingress`, `NetworkPolicy`, `HorizontalPodAutoscaler`, `PodDisruptionBudget`. Anything else (incl. all cluster-scoped kinds, `Secret`, RBAC, CRDs) → violation `kind_not_allowed`. |
| K8S-022 | `metadata.namespace` must be empty or equal the mapped namespace (violation `namespace_not_allowed`); the renderer sets it. |
| K8S-023 | No `Secret` manifests (Control 8 stores no secret values). Workloads reference existing Secrets by name (`secretKeyRef`, `envFrom`, volumes); Control 8 does not check they exist. |
| K8S-024 | The renderer strips `status`, `metadata.managedFields`, `resourceVersion`, `uid`, `creationTimestamp`, and injects the labels below on every object **and** on pod templates (`spec.template.metadata.labels`, CronJob `spec.jobTemplate.spec.template`). Selectors are never modified. |
| K8S-025 | User labels/annotations starting with `control-8/`, `app.kubernetes.io/managed-by`, or `argocd.argoproj.io/` → violation `label_forbidden`. |
| K8S-026 | `revision.rendered` holds the final object list; the **inventory** of a revision = its list of (group, kind, name). |

### Injected labels (Kubernetes)

| Key | Value |
|---|---|
| `app.kubernetes.io/managed-by` | `control-8` |
| `control-8/managed` | `true` |
| `control-8/stack` | `<stack.id>` |
| `control-8/namespace` | `<namespace.slug>` |
| `control-8/revision` | `<revision.number>` (object metadata only, not pod templates, so a structural no-op does not restart pods) |
| `control-8/safety-profile` | `managed_rollout` \| `as_is` |

Docker keeps its `control8.*` labels (04); the Kubernetes prefix is `control-8/` because label prefixes must be DNS names.

## Apply

1. Precheck: prod grant still active (GRT-012); policy allowed (DOM-003); DOM-001; target reachable; ownership guard (K8S-040..043); server-side dry-run apply of every object (`DryRun: All`) — any error → `failed`, `error=invalid_manifests|ownership_conflict`, nothing written.
2. Ensure namespace (K8S-011).
3. Server-side apply each object in kind order: ServiceAccount, ConfigMap, PersistentVolumeClaim, Service, NetworkPolicy, workloads, HorizontalPodAutoscaler, PodDisruptionBudget, Ingress. `FieldManager: control-8`, **`Force: false`**.
4. Wait for rollout health (below) up to `revision.rollout_timeout_seconds` (default `C8_ROLLOUT_TIMEOUT` 300 s, max 1800).
5. Healthy → prune: list objects of the K8S-021 kinds in the namespace with `control-8/stack=<id>`, delete those not in this revision's inventory (background propagation). **PVCs are never pruned.** Then `succeeded`, stack `active`, `desired_revision=N`.
6. Unhealthy / timeout → per safety profile (below).

Teardown: same precheck (minus dry-run), delete every object with `control-8/stack=<id>` of the K8S-021 kinds **except PersistentVolumeClaims** (data kept, as compose `down` without `-v`). Stack `removed`. Kept PVCs are listed in the deployment output.

## Ownership (one writer per workload)

| ID | Rule |
|---|---|
| K8S-040 | Ownership is recorded in the DB (`asset.managed_by`, stack ↔ target/namespace) and in the cluster (labels above). The `control-8/stack` label is the claim. |
| K8S-041 | Before apply, Control 8 GETs every object in the revision. An existing object without `control-8/stack=<this stack id>` → refuse (`ownership_conflict`, names the object). No adoption in v1. |
| K8S-045 | Exception (M21): an adopt deployment (DSC-045) may label existing unlabelled objects, only those listed in its adoption plan with matching `uid`, only in object metadata, and may migrate field ownership from the DSC-044 manual-manager allow-list. Everything else in K8S-041..044 still applies. Cluster-wide discovery (11) uses separate read-only informers and never writes. |
| K8S-042 | An existing object carrying `argocd.argoproj.io/instance` label or `argocd.argoproj.io/tracking-id` annotation → refuse (`argo_managed`), even if it also has Control 8 labels. |
| K8S-043 | Server-side apply runs with `Force: false`; a field-manager conflict (someone else owns a field we set, e.g. `kubectl edit`, an HPA owning `replicas`) fails the deployment with `ownership_conflict` and the conflicting managers in `error`. Control 8 never force-applies. Users omit `replicas` when an HPA manages the workload. |
| K8S-044 | Argo-owned workloads stay register-only: `asset.managed_by=argo` has no stacks (D-22), on any runtime. Control 8 writes nothing for them. |

## Safety profiles on Kubernetes

| Profile | UI label | On failed rollout | Image updates | Digest poll |
|---|---|---|---|---|
| `managed_rollout` | **Managed rollout** | Automatic rollback to the previous successful revision | `POST .../image-updates` | Optional per revision (`digest_poll`) |
| `as_is` | **As-is (no safety net)** | Deployment `failed`, cluster left as applied, no rollback | Only via a new revision | No |

`parachute` is Docker-only and `managed_rollout` is Kubernetes-only: a revision whose profile does not fit the stack runtime → 400 `safety_profile_invalid_for_runtime`. `as_is` is valid on both.

## Managed rollout: a bounded exception

Control 8 "never reimplements snapshot/rollback" — except here, because Parachute cannot run on Kubernetes (D-33). The exception is deliberately small:

| Does | Does not |
|---|---|
| Patch an image ref in desired-state manifests, apply, wait for health, re-apply the previous revision on failure | Snapshot or restore data / PVCs / volumes (out of scope; use your storage tooling) |
| Use the same revision + deployment flow as any other change | Canary, blue/green, traffic shifting, metric analysis, Argo Rollouts |
| Poll the registry for a new digest of the deployed tag (opt-in) | Follow tags to newer versions (tag change = new revision by a human) |
| Disable automatic updates after a failed rollback | Retry, backoff loops, notifications |

### Image update flow

| ID | Rule |
|---|---|
| K8S-050 | Trigger **manual**: `POST .../stacks/{stack}/image-updates {workload: "Deployment/web", container: "web", image, reason}` (operator; prod needs an active grant, 09). Only for `kubernetes` + `managed_rollout` stacks in state `active`; else 409 `image_update_not_allowed`. `image` must keep the same repository (tag and/or digest change only), else 400 `image_repo_change` (a repo change is a structural revision). |
| K8S-051 | Trigger **digest poll** (opt-in, revision `digest_poll: true`): every `C8_DIGEST_POLL` (default 15m, `0` = off) resolve each container's `repo:tag` to a digest with `go-containerregistry` (`C8_REGISTRY_AUTH` optional docker `config.json` from the Secret). If it differs from the digest in the rendered image (or, if unpinned, from the running pods' `imageID`), record it in `stack.digest_status` and, on **non-prod** stacks only, request an image update to `repo:tag@sha256:<new>` as `actor_type=system`. On prod stacks the poll only reports; a human with an active grant applies it (D-46, 09). Skipped while a deployment is open, the stack is frozen, or the same digest already failed for this stack. |
| K8S-052 | An image update creates revision `N+1` = desired revision with only that container's `image` replaced (`origin=image_update`, `base_revision=N`), re-runs policy (registry allow-list, POL-R9 protected-DB approval), then a deployment with `trigger=manual\|digest_poll`. Everything else (approval, DOM-001, audit) is the normal flow. |
| K8S-053 | Every apply of a `managed_rollout` revision that has a predecessor (image update **or** structural) uses health wait + automatic rollback. First apply has nothing to roll back to: it fails like `as_is`. |
| K8S-054 | Rollback = server-side apply of the previous successful revision's rendered objects + prune to its inventory, then health wait with the same timeout. `desired_revision` stays at the previous number. (Not `kubectl rollout undo`: that is kubectl logic, and re-applying stored desired state also reverts ConfigMaps etc.) |
| K8S-055 | Rollback healthy → deployment `failed`, `rollout.result=rolled_back`, stack `active`. Rollback unhealthy → `rollout.result=rollback_failed`, stack `failed`, `stack.rollout_frozen=true`: digest poll and image updates are refused (409 `rollout_frozen`) until an admin's successful deployment clears it. Health shows `frozen`. |

### Rollout health criteria

Evaluated as a pure function over informer-cached objects (K8S-063); all workloads in the revision must pass.

| Kind | Healthy when |
|---|---|
| Deployment | `status.observedGeneration ≥ metadata.generation`, `updatedReplicas == availableReplicas == spec.replicas` (default 1), no old replicas left (`replicas == updatedReplicas`) |
| StatefulSet | `observedGeneration ≥ generation`, `readyReplicas == updatedReplicas == spec.replicas`, `currentRevision == updateRevision` (or `OnDelete` strategy: ready only) |
| DaemonSet | `observedGeneration ≥ generation`, `updatedNumberScheduled == numberAvailable == desiredNumberScheduled` |
| Job | `Complete` condition true; `Failed` condition → unhealthy immediately |
| Others (Service, ConfigMap, PVC, CronJob, ...) | Applied without error |

Immediately unhealthy (no need to wait for timeout): Deployment condition `Progressing=False` reason `ProgressDeadlineExceeded`, or any pod of the new revision in `CrashLoopBackOff` / `ImagePullBackOff` / `ErrImagePull` for > 60 s.

## Observe and drift (informers)

| ID | Rule |
|---|---|
| K8S-060 | Per target, one `dynamicinformer` shared informer factory with label selector `control-8/managed=true`, cluster-wide, for the K8S-021 workload kinds plus `Pod` (pods carry the injected template labels). Resync `C8_INFORMER_RESYNC` (10 m). Started at boot for every reachable target; restarted with backoff after a target recovers. |
| K8S-061 | Informer add/update/delete handlers only enqueue the object's `control-8/stack` id. `internal/inventory` debounces per stack (1 s), recomputes from the informer caches (never from the API server), writes `service` rows, drift and health, and publishes `stack.updated` only when something changed (10). |
| K8S-062 | Each workload becomes a `service` row (`name` = `Kind/name`, `kind`, `desired_replicas`, `ready_replicas`, `image_ref` from the pod template, `image_id` from a ready pod's `imageID`). Drift kinds reuse 04 names with `service` = `Kind/name`: `service_missing` (in inventory, not in cache), `service_extra` (labelled, not in inventory; prune candidate), `image_mismatch` (template image repo:tag ≠ rendered), `label_mismatch` (`control-8/revision` ≠ desired). No auto-correction. |
| K8S-063 | Rollout health (above) is evaluated on every informer change for the deployment's stack, with a 5 s timer as a safety net; progress is published as `deployment.updated` (coalesced 1/s). |
| K8S-064 | Fallback: if a kind cannot be watched (RBAC lacks `watch`, or watch keeps failing while `ServerVersion` succeeds), Control 8 lists it with the same selector every `C8_TARGET_CHECK` and logs `informer_degraded`; `target.status` shows `sync: polling`. Target `sync` ∈ `syncing` \| `watching` \| `polling` \| `down`. |
| K8S-065 | While a target is unreachable, its stacks keep their last observed state with health `unknown` and `observed_at`; caches are not cleared. |
| K8S-066 | (M17e, 13) Deploy plans use server-side `dryRun=All` applies and never write (PLN-001/007). While a deployment runs, a namespace-scoped Event informer (`involvedObject.kind=Pod`) feeds the image-pull and rollout steps (STP-006); it is stopped when no deployment in that namespace is running. `rbac.yaml` adds `get/list/watch` on `events` (core); without it steps fall back to pod statuses. All messages pass the redactor (RED-020). |

## Audit events

| Action | Actor | When |
|---|---|---|
| `target.config_sync` | system | Startup sync of `C8_TARGETS` (added/removed/tier changes) |
| `image_update.request` | user/token, or `system` (poll) | K8S-050/051 |
| `digest_poll.detected` | system | New digest found (details: workload, container, old/new digest) |
| `rollout.healthy` | system | Health wait passed |
| `rollout.failed` | system | Unhealthy / timeout (details: first failing workload + reason) |
| `rollout.rollback` | system | Rollback finished; `outcome=success` (rolled back) or `failed` (rollback_failed) |
| `stack.rollout_frozen`, `stack.rollout_unfrozen` | system | K8S-055 |

## Failure modes

| Situation | Behaviour |
|---|---|
| Target unreachable | Target shows error and `sync: down` (live via `target.status`); stack health `unknown`; deploy → 503 `target_unreachable`; reads unaffected |
| Kubeconfig file missing/invalid | Target `reachable=false`, error `kubeconfig_invalid`; Control 8 still starts |
| RBAC forbids a verb | Deployment `failed`, `error=forbidden_by_rbac` with the verb/kind |
| Object exists, not ours / Argo | Refused before any write (K8S-041/042) |
| SSA field conflict | `ownership_conflict` (K8S-043); never forced |
| Rollout timeout on `managed_rollout` | Rollback (K8S-054/055) |
| Rollout timeout on `as_is` | `failed`, nothing reverted |
| Registry unreachable during digest poll | Logged + `digest_poll` error on stack; no update; retried next tick |
| Someone `kubectl edit`s a workload | Drift on next tick; next apply fails with `ownership_conflict` until the manual field owner is removed |
