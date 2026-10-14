#!/usr/bin/env bash
# Generate realistic traffic with k6 running INSIDE the cluster, then watch autoscaling.
#
# Usage: ./scripts/07-run-load-test.sh [virtual-users] [duration]
#        ./scripts/07-run-load-test.sh 50 10m
#
# In another terminal:  kubectl -n shopverse get hpa -w
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
load_infra
ensure_kubeconfig

VUS="${1:-20}"
DURATION="${2:-5m}"

step "Starting k6 load test: ${VUS} virtual users for ${DURATION} (+1.5 min ramp up/down)"
kubectl -n "$APP_NAMESPACE" create configmap k6-script \
  --from-file=shopper.js="${K8S_DIR}/loadtest/shopper.js" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$APP_NAMESPACE" delete job k6-load-test --ignore-not-found >/dev/null
sed -e "s/__VUS__/${VUS}/" -e "s/__DURATION__/${DURATION}/" "${K8S_DIR}/loadtest/job.yaml" | kubectl apply -f - >/dev/null
ok "Job created"

info "Tip: open Grafana (./scripts/08-open-dashboards.sh) > Dashboards > ShopVerse — Microservices Overview"
info "Tip: in another terminal run:  kubectl -n shopverse get hpa -w"
info ""
info "Waiting for the k6 pod to start..."
kubectl -n "$APP_NAMESPACE" wait --for=condition=Ready pod -l job-name=k6-load-test --timeout=120s >/dev/null 2>&1 || true
kubectl -n "$APP_NAMESPACE" logs -f job/k6-load-test
