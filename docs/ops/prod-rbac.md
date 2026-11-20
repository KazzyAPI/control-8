# Prod RBAC hardening (GRT-060)

Recommended, not built by Control 8. Control 8 is the only holder of write credentials for prod targets (GRT-050); this keeps humans from bypassing grants (GRT-051).

1. Give humans only the built-in `view` ClusterRole in prod clusters. No `edit`, `admin` or `cluster-admin` for day-to-day users.
2. Bind write verbs on the kinds in K8S-021 only to Control 8's service account or kubeconfig user (see `deploy/` RBAC).
3. Keep prod kubeconfigs only in Control 8's own Kubernetes Secret (injected as env, D-49); restrict `get` on that Secret to cluster admins.
4. Optionally add a `ValidatingAdmissionPolicy` that denies writes to objects labelled `control-8/managed=true` by any user other than Control 8's, so manual `kubectl` edits of managed objects are refused.
5. Anything done with a private kubeconfig still shows up as drift (`label_mismatch`/`image_mismatch`) or `ownership_conflict` on the next apply.
