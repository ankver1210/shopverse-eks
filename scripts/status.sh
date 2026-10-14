#!/usr/bin/env bash
# One-page overview of the cluster and the application.
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
load_infra
ensure_kubeconfig

step "Nodes"
kubectl get nodes -L topology.kubernetes.io/zone,node.kubernetes.io/instance-type,eks.amazonaws.com/capacityType
step "Microservices (namespace ${APP_NAMESPACE})"
kubectl -n "$APP_NAMESPACE" get deploy,hpa -o wide 2>/dev/null || warn "nothing deployed yet"
step "Pods not Running/Completed"
kubectl get pods -A --field-selector=status.phase!=Running,status.phase!=Succeeded 2>/dev/null || true
step "Data stores (namespace ${DATA_NAMESPACE})"
kubectl -n "$DATA_NAMESPACE" get statefulset,pvc 2>/dev/null || true
step "Secrets sync"
kubectl get externalsecrets -A 2>/dev/null || true
step "Argo CD applications"
kubectl -n argocd get applications 2>/dev/null || info "(GitOps not enabled)"
step "Resource usage"
kubectl top nodes 2>/dev/null || info "(metrics not ready yet)"
url="$(kubectl get ingress frontend -n "$APP_NAMESPACE" -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || true)"
step "Store URL"
info "${url:+http://$url}${url:-not available yet}"
