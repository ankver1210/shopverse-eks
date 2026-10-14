#!/usr/bin/env bash
# Open every UI through secure kubectl port-forwards (nothing is exposed to the internet).
# Press Ctrl+C to close them all.
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
load_infra
ensure_kubeconfig

pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; echo; info "Port-forwards closed."; }
trap cleanup EXIT INT TERM

forward() {
  local ns="$1" svc="$2" ports="$3"
  if kubectl -n "$ns" get svc "$svc" >/dev/null 2>&1; then
    kubectl -n "$ns" port-forward "svc/${svc}" "$ports" >/dev/null 2>&1 &
    pids+=("$!")
    return 0
  fi
  return 1
}

step "Starting port-forwards"
forward monitoring kube-prometheus-stack-grafana 3000:80 && ok "Grafana       http://localhost:3000"
forward monitoring kube-prometheus-stack-prometheus 9090:9090 && ok "Prometheus    http://localhost:9090"
forward monitoring kube-prometheus-stack-alertmanager 9093:9093 && ok "Alertmanager  http://localhost:9093"
forward argocd argocd-server 8080:80 && ok "Argo CD       http://localhost:8080"
forward "$DATA_NAMESPACE" rabbitmq 15672:15672 && ok "RabbitMQ      http://localhost:15672"

grafana_pw="$(kubectl -n monitoring get secret grafana-admin -o jsonpath='{.data.admin-password}' 2>/dev/null | base64 --decode || true)"
argocd_pw="$(kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' 2>/dev/null | base64 --decode || true)"
rabbit_pw="$(kubectl -n "$DATA_NAMESPACE" get secret shopverse-secrets -o jsonpath='{.data.rabbitmq-password}' 2>/dev/null | base64 --decode || true)"

step "Logins"
info "Grafana:  admin / ${grafana_pw:-?}"
info "Argo CD:  admin / ${argocd_pw:-?}"
info "RabbitMQ: shopverse / ${rabbit_pw:-?}"
info ""
info "Grafana > Dashboards > ShopVerse — Microservices Overview"
info "Press Ctrl+C to stop."
wait
