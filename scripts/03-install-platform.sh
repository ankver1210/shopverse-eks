#!/usr/bin/env bash
# Step 3 — Install the platform add-ons every production cluster needs:
#   AWS Load Balancer Controller, metrics-server, External Secrets Operator,
#   kube-prometheus-stack (Prometheus + Grafana) and Argo CD.
# Takes about 8-10 minutes. Safe to re-run (it upgrades in place).
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
require_cmd helm
load_infra
ensure_kubeconfig
load_versions

grep -q "AWS_REGION" "${K8S_DIR}/platform/cluster-secret-store.yaml" && die "Run ./scripts/02-configure-repo.sh first."

step "Creating namespaces"
kubectl apply -f "${K8S_DIR}/namespaces.yaml"

step "Adding Helm repositories"
helm repo add eks https://aws.github.io/eks-charts --force-update >/dev/null
helm repo add metrics-server https://kubernetes-sigs.github.io/metrics-server/ --force-update >/dev/null
helm repo add external-secrets https://charts.external-secrets.io --force-update >/dev/null
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update >/dev/null
helm repo add argo https://argoproj.github.io/argo-helm --force-update >/dev/null
helm repo update >/dev/null
ok "Helm repositories ready"

step "1/5 AWS Load Balancer Controller ${AWS_LOAD_BALANCER_CONTROLLER_CHART} (turns Ingress objects into ALBs)"
# region + vpcId are passed explicitly because pods cannot reach the EC2 metadata service (IMDSv2 hop limit = 1).
helm upgrade --install aws-load-balancer-controller eks/aws-load-balancer-controller \
  --namespace kube-system \
  --version "$AWS_LOAD_BALANCER_CONTROLLER_CHART" \
  --set clusterName="$CLUSTER_NAME" \
  --set region="$REGION" \
  --set vpcId="$VPC_ID" \
  --set serviceAccount.name=aws-load-balancer-controller \
  --wait --timeout 5m
ok "AWS Load Balancer Controller installed"

step "2/5 metrics-server ${METRICS_SERVER_CHART} (CPU/memory metrics for 'kubectl top' and autoscaling)"
helm upgrade --install metrics-server metrics-server/metrics-server \
  --namespace kube-system \
  --version "$METRICS_SERVER_CHART" \
  --wait --timeout 5m
ok "metrics-server installed"

step "3/5 External Secrets Operator ${EXTERNAL_SECRETS_CHART} (syncs AWS Secrets Manager -> Kubernetes)"
helm upgrade --install external-secrets external-secrets/external-secrets \
  --namespace external-secrets --create-namespace \
  --version "$EXTERNAL_SECRETS_CHART" \
  -f "${PLATFORM_DIR}/external-secrets.yaml" \
  --wait --timeout 5m
ok "External Secrets Operator installed"

info "Applying StorageClass, ClusterSecretStore and ExternalSecrets..."
kubectl apply -k "${K8S_DIR}/platform"
for ns in "$APP_NAMESPACE" "$DATA_NAMESPACE"; do
  kubectl wait --for=condition=Ready externalsecret/shopverse-secrets -n "$ns" --timeout=180s
done
kubectl wait --for=condition=Ready externalsecret/grafana-admin -n monitoring --timeout=180s
ok "Secrets synced from AWS Secrets Manager"

step "4/5 kube-prometheus-stack ${KUBE_PROMETHEUS_STACK_CHART} (Prometheus, Alertmanager, Grafana)"
helm upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
  --namespace monitoring \
  --version "$KUBE_PROMETHEUS_STACK_CHART" \
  -f "${PLATFORM_DIR}/kube-prometheus-stack.yaml" \
  --wait --timeout 10m
kubectl apply -k "${K8S_DIR}/monitoring"
ok "Monitoring stack installed (ShopVerse dashboard + alerts loaded)"

step "5/5 Argo CD ${ARGO_CD_CHART} (GitOps)"
helm upgrade --install argocd argo/argo-cd \
  --namespace argocd --create-namespace \
  --version "$ARGO_CD_CHART" \
  -f "${PLATFORM_DIR}/argocd.yaml" \
  --wait --timeout 10m
ok "Argo CD installed"

step "Platform ready"
helm list -A
info ""
info "Next: ./scripts/04-build-and-push-images.sh"
