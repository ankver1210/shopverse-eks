#!/usr/bin/env bash
# Inject failures into a running service to practise debugging and see alerts fire.
#
# Usage:
#   ./scripts/chaos.sh <service> errors <rate>      e.g. chaos.sh product-service errors 0.3   (30% HTTP 500)
#   ./scripts/chaos.sh <service> latency <ms>       e.g. chaos.sh payment-service latency 1500
#   ./scripts/chaos.sh <service> kill               delete one random pod (watch it come back)
#   ./scripts/chaos.sh <service> off                remove injected chaos
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
load_infra
ensure_kubeconfig

svc="${1:-}"; action="${2:-}"; value="${3:-}"
is_valid_service "$svc" || die "Usage: $0 <service> errors|latency|kill|off [value]. Services: ${SERVICES[*]}"

if kubectl -n "$APP_NAMESPACE" get deploy "$svc" -o jsonpath='{.metadata.labels}' | grep -q "argocd.argoproj.io/instance"; then
  managed_by_argo=1
else
  managed_by_argo=0
fi

if [ "$action" != "kill" ] && [ "$managed_by_argo" = "1" ]; then
  warn "${svc} is managed by Argo CD with self-heal: it would undo a manual change within seconds."
  info "Do chaos the GitOps way instead — edit deploy/helm/values/${svc}.yaml:"
  info "    env:"
  info "      CHAOS_ERROR_RATE: \"0.3\"     # or CHAOS_LATENCY_MS: \"1500\""
  info "then commit and push. Remove the lines (and push) to recover."
  exit 0
fi

case "$action" in
  errors)
    [ -n "$value" ] || die "Give an error rate between 0 and 1, e.g. 0.3"
    kubectl -n "$APP_NAMESPACE" set env "deploy/${svc}" CHAOS_ERROR_RATE="$value"
    ok "${svc} will now fail ${value} of requests. Watch Grafana and the Platform page." ;;
  latency)
    [ -n "$value" ] || die "Give a latency in milliseconds, e.g. 1500"
    kubectl -n "$APP_NAMESPACE" set env "deploy/${svc}" CHAOS_LATENCY_MS="$value"
    ok "${svc} now adds ${value}ms to every request." ;;
  kill)
    pod="$(kubectl -n "$APP_NAMESPACE" get pods -l "app.kubernetes.io/name=${svc}" -o jsonpath='{.items[0].metadata.name}')"
    kubectl -n "$APP_NAMESPACE" delete pod "$pod" --wait=false
    ok "Deleted ${pod}. Watch Kubernetes replace it:  kubectl -n shopverse get pods -w" ;;
  off)
    kubectl -n "$APP_NAMESPACE" set env "deploy/${svc}" CHAOS_ERROR_RATE- CHAOS_LATENCY_MS-
    ok "Chaos removed from ${svc}." ;;
  *) die "Unknown action '${action}'. Use errors, latency, kill or off." ;;
esac
