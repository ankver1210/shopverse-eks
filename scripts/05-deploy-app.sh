#!/usr/bin/env bash
# Step 5 — Deploy the data stores and all 10 microservices with Helm.
#
# Usage:
#   ./scripts/05-deploy-app.sh                 # deploy / upgrade everything
#   ./scripts/05-deploy-app.sh cart-service    # deploy just one service
#   ./scripts/05-deploy-app.sh --uninstall     # remove the microservices (keeps data stores)
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
require_cmd helm
load_infra
ensure_kubeconfig

if [ "${1:-}" = "--uninstall" ]; then
  confirm "Uninstall all ShopVerse microservices (data stores are kept)?"
  for svc in "${SERVICES[@]}"; do
    if helm uninstall "$svc" -n "$APP_NAMESPACE" 2>/dev/null; then ok "removed $svc"; fi
  done
  exit 0
fi

grep -q "ACCOUNT_ID" "${VALUES_DIR}/common.yaml" && die "Image registry not configured. Run ./scripts/02-configure-repo.sh first."
kubectl get externalsecret shopverse-secrets -n "$APP_NAMESPACE" >/dev/null 2>&1 || die "Platform not installed. Run ./scripts/03-install-platform.sh first."

targets=("$@")
[ ${#targets[@]} -gt 0 ] || targets=("${SERVICES[@]}")
for svc in "${targets[@]}"; do is_valid_service "$svc" || die "Unknown service '$svc'. Valid: ${SERVICES[*]}"; done

step "Checking the images exist in ECR"
for svc in "${targets[@]}"; do
  tag="$(service_tag "$svc")"
  if aws ecr describe-images --repository-name "shopverse/${svc}" --image-ids imageTag="$tag" >/dev/null 2>&1; then
    ok "${svc}:${tag}"
  else
    die "Image shopverse/${svc}:${tag} not found in ECR. Run ./scripts/04-build-and-push-images.sh ${svc}"
  fi
done

if [ ${#targets[@]} -eq ${#SERVICES[@]} ]; then
  step "Deploying data stores (PostgreSQL, Redis, RabbitMQ)"
  kubectl apply -k "${K8S_DIR}/data"
  for sts in postgres redis rabbitmq; do
    kubectl rollout status "statefulset/${sts}" -n "$DATA_NAMESPACE" --timeout=10m
  done
  ok "Data stores are running"

  step "Applying namespace extras (default-deny NetworkPolicy, restock CronJob)"
  kubectl apply -k "${K8S_DIR}/app-extras"
fi

step "Deploying microservices with Helm"
for svc in "${targets[@]}"; do
  helm upgrade --install "$svc" "$HELM_CHART" \
    --namespace "$APP_NAMESPACE" \
    -f "${VALUES_DIR}/common.yaml" \
    -f "${VALUES_DIR}/${svc}.yaml" >/dev/null
  ok "helm release ${svc} ($(service_tag "$svc"))"
done

step "Waiting for rollouts"
for svc in "${targets[@]}"; do
  kubectl rollout status "deployment/${svc}" -n "$APP_NAMESPACE" --timeout=10m
done

step "Waiting for the AWS Application Load Balancer (first time: 2-4 minutes)"
url=""
for _ in $(seq 1 60); do
  url="$(kubectl get ingress frontend -n "$APP_NAMESPACE" -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || true)"
  [ -n "$url" ] && break
  printf '.'; sleep 5
done
echo
[ -n "$url" ] || die "No load balancer address yet. Check: kubectl describe ingress frontend -n shopverse"

step "ShopVerse is live 🎉"
info "Open: ${BOLD}http://${url}${RESET}"
info "(New ALB DNS names can take 1-3 more minutes to resolve. If the page doesn't load, wait and refresh.)"
info ""
info "Useful next steps:"
info "  ./scripts/status.sh             overview of everything running"
info "  ./scripts/08-open-dashboards.sh Grafana, Prometheus, Argo CD, RabbitMQ"
info "  ./scripts/07-run-load-test.sh   generate traffic and watch autoscaling"
