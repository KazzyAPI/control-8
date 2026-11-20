# Installing Control 8

Image: `ghcr.io/kazzyapi/control-8:<tag>` (non-root, uid/gid 10001, read-only root filesystem, data in `/var/lib/control8`). One replica only (ARC-014): SQLite on a single volume.

## Secrets you generate first

```sh
# Break-glass password hash (reads the password from stdin; paste into C8_BREAKGLASS_HASH)
printf '%s' 'a long passphrase' | docker run -i --rm ghcr.io/kazzyapi/control-8:<tag> hash-password

# Static API token: prints the plaintext (give it to the CI job, shown once) and its sha256 (goes into C8_TOKENS)
docker run --rm ghcr.io/kazzyapi/control-8:<tag> gen-token
```

`C8_TOKENS` is a JSON list in the Secret, hashes only, `expires_at` at most 365 days out:

```json
[{"name": "ci-shop", "owner_sub": "<OIDC sub of the owner>", "namespace": "shop", "role": "operator",
  "sha256": "<gen-token sha256>", "expires_at": "2027-06-30T00:00:00Z"}]
```

The owner must have logged in via OIDC at least once; the token's power is the lower of its role and the owner's. Revoke by removing the entry and rolling out.

## In-cluster (Kubernetes)

`deploy/kubernetes/` holds the Namespace, ServiceAccount, ConfigMap, Secret, PVC, Service and Deployment (`control8.yaml`) and the ClusterRole/Binding (`rbac.yaml`). CI applies exactly these files on kind and runs `scripts/smoke.sh` against them.

1. Edit the ConfigMap in `control8.yaml`: `C8_PUBLIC_URL` (the external URL users open), OIDC issuer and client id, `C8_BOOTSTRAP_ADMIN_GROUP`, and `C8_TARGETS`. The sample target `home` is the cluster Control 8 runs in (`kube_auth: in_cluster`, uses the ServiceAccount and `rbac.yaml`).
2. Put secrets in the Secret, never the ConfigMap: `C8_BREAKGLASS_HASH`, `C8_OIDC_CLIENT_SECRET`, `C8_TOKENS`, `C8_WEBHOOK_SECRET`, `C8_REGISTRY_AUTH`, and `C8_KUBECONFIG_<NAME>` for each remote target (referenced by `kubeconfig_env`). Prefer creating the Secret out of band (`kubectl create secret ...`) over committing values.
3. Pin the image to a release tag (or digest) in the Deployment, then:

```sh
kubectl apply -f deploy/kubernetes/rbac.yaml -f deploy/kubernetes/control8.yaml
kubectl -n control8 rollout status deployment/control8
kubectl -n control8 port-forward svc/control8 8000:8000   # or expose via Ingress
```

The Service is ClusterIP; exposing it (Ingress, Gateway) is yours to add. Terminate TLS there and set `C8_PUBLIC_URL` to the https URL (session cookies are `Secure` for https).

## Compose

```sh
$EDITOR deploy/control8.env                          # C8_PUBLIC_URL, C8_TARGETS, C8_OIDC_*, ...
$EDITOR deploy/control8.secrets.env                  # C8_BREAKGLASS_HASH=..., C8_OIDC_CLIENT_SECRET=...
C8_VERSION=<tag> docker compose -f deploy/compose.yaml up -d
```

Both env files are git-ignored and optional. Escape `$` as `$$` in Compose env files (argon2 hashes contain `$`). Compose runs only the control plane; a Kubernetes target needs a `C8_KUBECONFIG_*` secret.

## Reverse proxy

- `/api/v1/events` is Server-Sent Events: **disable response buffering** and use a long read timeout. nginx: `proxy_buffering off; proxy_read_timeout 1h; proxy_http_version 1.1;` (Control 8 also sends `X-Accel-Buffering: no`, and a heartbeat every 15 s). Traefik and Caddy stream by default; for Caddy add `flush_interval -1` if you see delays.
- Forward `Host`; do not rewrite paths. Control 8 sets its own CSP, `nosniff`, `Referrer-Policy: no-referrer` and `X-Frame-Options: DENY`; do not override them.

## Authentik (OIDC) and approver groups

Provider setup is in [authentik.md](authentik.md). Summary: redirect URI `<C8_PUBLIC_URL>/auth/callback`, a `groups` scope mapping, issuer into `C8_OIDC_ISSUER`, client id into the ConfigMap, client secret into the Secret.

Approver groups: create one Authentik group per product (for example `shop-approvers`), then a global_admin sets it as the namespace's `approver_group` (`PATCH /api/v1/namespaces/shop`, or the UI). Members then get `approver_for: ["shop"]` in `/me` and can approve prod access grants for that namespace. Access to a namespace itself comes from role bindings (`/api/v1/role-bindings`: IdP group to role per namespace); `C8_BOOTSTRAP_ADMIN_GROUP` makes the first group global_admin. Prod namespaces require an `approver_group`.

## Verify

`scripts/smoke.sh` (needs `curl`, `jq`, `kubectl`): `C8_URL=http://localhost:8000 C8_SMOKE_PASSWORD=<break-glass password> scripts/smoke.sh`. It deploys a ConfigMap and a pause Deployment to a non-prod and a prod namespace on target `home`, checks the `control-8/*` labels with kubectl, then tears down. Run it only against a throwaway cluster.

Back up the `/var/lib/control8` volume (SQLite, WAL: snapshot the volume or stop the pod first). Prod hardening: [prod-rbac.md](prod-rbac.md).
