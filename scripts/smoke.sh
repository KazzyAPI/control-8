#!/usr/bin/env bash
# End-to-end smoke (M16): break-glass login, deploy a ConfigMap + pause Deployment to a non-prod and a prod
# namespace through the API, verify the objects with kubectl, tear down.
# Needs: curl, jq, kubectl (current context = the cluster Control 8 deploys to).
# Env: C8_URL (default http://localhost:8000), C8_SMOKE_PASSWORD (break-glass password), C8_SMOKE_TARGET (default home).
set -euo pipefail
URL=${C8_URL:-http://localhost:8000}
PW=${C8_SMOKE_PASSWORD:?set C8_SMOKE_PASSWORD}
TARGET=${C8_SMOKE_TARGET:-home}
NP=smoke-np PROD=smoke-prod
JAR=$(mktemp) BODY=$(mktemp)
cleanup() {
  set +e
  for ns in $NP $PROD; do api DELETE "/api/v1/namespaces/$ns/stacks/s1" '{"reason":"smoke cleanup","confirm_environment_tier":"'"$([ $ns = $PROD ] && echo prod || echo non-prod)"'"}' >/dev/null 2>&1; done
  sleep 3
  kubectl delete ns $NP $PROD --ignore-not-found --wait=false >/dev/null 2>&1
  rm -f "$JAR" "$BODY"
}
trap cleanup EXIT
log() { echo "smoke: $*"; }
die() { echo "smoke FAILED: $*" >&2; [ -s "$BODY" ] && cat "$BODY" >&2; exit 1; }

CSRF=
# api METHOD PATH [JSON]: prints the HTTP status; the response body is left in $BODY.
api() {
  local m=$1 p=$2 d=${3:-}
  curl -sS -m 30 -o "$BODY" -w '%{http_code}' -X "$m" -b "$JAR" -c "$JAR" -H "X-CSRF-Token: $CSRF" \
    ${d:+-H 'Content-Type: application/json' -d "$d"} "$URL$p"
}
expect() { # expect WANT METHOD PATH [JSON]
  local want=$1 got; shift
  got=$(api "$@") || die "curl failed: $*"
  [ "$got" = "$want" ] || die "$* -> $got, want $want"
}
wait_for() { # wait_for DESC SECONDS CMD...
  local desc=$1 n=$2; shift 2
  for _ in $(seq "$n"); do "$@" && return 0; sleep 1; done
  die "timeout: $desc"
}

log "healthz and SPA"
[ "$(curl -sS -m 10 -o /dev/null -w '%{http_code}' "$URL/healthz")" = 200 ] || die healthz
curl -sS -m 10 "$URL/" | grep -qi '<div id="root"' || die "SPA not served at /"
curl -sSI -m 10 "$URL/" | grep -qi '^content-security-policy:' || die "no CSP header"

log "break-glass login"
expect 204 POST /auth/break-glass "$(jq -nc --arg p "$PW" '{password:$p}')"
expect 200 GET /api/v1/me
CSRF=$(jq -r .csrf_token "$BODY")
[ "$(jq -r .global_admin "$BODY")" = true ] || die "not global_admin"

target_up() { api GET /api/v1/targets >/dev/null && jq -e --arg t "$TARGET" '.items[]|select(.name==$t and .reachable)' "$BODY" >/dev/null; }
wait_for "target $TARGET reachable" 90 target_up

MANIFESTS=$'apiVersion: v1\nkind: ConfigMap\nmetadata: {name: smoke-cfg}\ndata: {a: b}\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata: {name: pause}\nspec:\n  replicas: 1\n  selector: {matchLabels: {app: pause}}\n  template:\n    metadata: {labels: {app: pause}}\n    spec:\n      containers:\n        - {name: pause, image: "registry.k8s.io/pause:3.9"}\n'

deploy() { # deploy NS TIER -> echoes the deployment id
  local ns=$1 tier=$2
  expect 201 POST /api/v1/namespaces "$(jq -nc --arg s "$ns" --arg t "$tier" '{slug:$s,display_name:$s,environment_tier:$t} + (if $t=="prod" then {approver_group:"smoke-approvers"} else {} end)')"
  expect 201 POST "/api/v1/namespaces/$ns/assets" '{"slug":"web","name":"Web"}'
  expect 201 POST "/api/v1/namespaces/$ns/stacks" "$(jq -nc --arg t "$TARGET" '{slug:"s1",asset:"web",target:$t}')"
  expect 201 POST "/api/v1/namespaces/$ns/stacks/s1/revisions" "$(jq -nc --arg m "$MANIFESTS" '{safety_profile:"as_is",manifests:$m}')"
  expect 202 POST "/api/v1/namespaces/$ns/stacks/s1/deployments" "$(jq -nc --arg t "$tier" '{revision:1,confirm_safety_profile:"as_is",confirm_environment_tier:$t,reason:"smoke"}')"
  jq -r .id "$BODY"
}
dep_done() { api GET "/api/v1/deployments/$1" >/dev/null; local s; s=$(jq -r .status "$BODY"); [ "$s" != failed ] || die "deployment failed: $(jq -c '{error,output}' "$BODY")"; [ "$s" = succeeded ]; }
healthy() { api GET "/api/v1/namespaces/$1/stacks/s1" >/dev/null; [ "$(jq -r .health "$BODY")" = healthy ]; }

log "non-prod deploy"
ID=$(deploy $NP non-prod)
wait_for "deployment $ID succeeded" 240 dep_done "$ID"
wait_for "stack $NP healthy" 120 healthy $NP
for k in configmap/smoke-cfg deployment/pause; do
  [ "$(kubectl -n $NP get "$k" -o jsonpath='{.metadata.labels.control-8/managed}')" = true ] || die "$k missing control-8 labels"
done
kubectl -n $NP get deployment pause -o jsonpath='{.metadata.labels.control-8/stack}' | grep -q . || die "no control-8/stack label"

log "prod deploy (break-glass bypass, audited)"
ID=$(deploy $PROD prod)
wait_for "prod deployment $ID succeeded" 240 dep_done "$ID"
[ "$(jq -r .grant_bypass "$BODY")" = break_glass ] || die "prod deploy not recorded as break-glass bypass"
expect 200 GET "/api/v1/audit?namespace=$PROD&action=deployment.request"
jq -e '[.items[]|select(.outcome=="success" and .details.grant_bypass=="break_glass")]|length>=1' "$BODY" >/dev/null || die "bypass not audited"
kubectl -n $PROD get deployment pause >/dev/null || die "prod objects missing"

log "teardown"
for ns in $NP $PROD; do
  t=non-prod; [ $ns = $PROD ] && t=prod
  expect 202 DELETE "/api/v1/namespaces/$ns/stacks/s1" "{\"reason\":\"smoke\",\"confirm_environment_tier\":\"$t\"}"
  tid=$(jq -r .id "$BODY")
  wait_for "teardown $ns" 120 dep_done "$tid"
  kubectl -n $ns get deployment pause >/dev/null 2>&1 && die "$ns objects survived teardown"
done
log "OK"
