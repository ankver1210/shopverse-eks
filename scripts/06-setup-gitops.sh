#!/usr/bin/env bash
# Step 6 (optional, recommended) — Hand the deployments over to Argo CD (GitOps).
# After this, Git is the source of truth: change a values file, push, and Argo CD deploys it.
#
# Usage:
#   ./scripts/06-setup-gitops.sh https://github.com/<you>/shopverse-eks.git
#   GITHUB_TOKEN=ghp_xxx ./scripts/06-setup-gitops.sh https://github.com/<you>/shopverse-eks.git   # private repo
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
load_infra
ensure_kubeconfig

REPO_URL="${1:-}"
case "$REPO_URL" in
  https://github.com/*/*) ;;
  *) die "Usage: $0 https://github.com/<your-user>/<your-repo>.git" ;;
esac
case "$REPO_URL" in *.git) ;; *) REPO_URL="${REPO_URL}.git" ;; esac

kubectl get ns argocd >/dev/null 2>&1 || die "Argo CD is not installed. Run ./scripts/03-install-platform.sh first."

cat <<MSG

Argo CD will deploy whatever is on the ${BOLD}main${RESET} branch of:
  ${REPO_URL}

Make sure you have pushed this repository there, INCLUDING the image tags written by
04-build-and-push-images.sh and the account settings written by 02-configure-repo.sh.
MSG
confirm "Continue?"

if [ -n "${GITHUB_TOKEN:-}" ]; then
  step "Registering private repository credentials"
  kubectl -n argocd create secret generic shopverse-repo \
    --from-literal=type=git --from-literal=url="$REPO_URL" \
    --from-literal=username=git --from-literal=password="$GITHUB_TOKEN" \
    --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n argocd label secret shopverse-repo argocd.argoproj.io/secret-type=repository --overwrite
  ok "Repository credentials stored in the argocd namespace"
fi

step "Handing existing Helm releases over to Argo CD (zero downtime)"
# Deleting only Helm's release records leaves the running resources untouched; Argo CD adopts them.
for svc in "${SERVICES[@]}"; do
  if kubectl -n "$APP_NAMESPACE" get secret -l "owner=helm,name=${svc}" -o name 2>/dev/null | grep -q .; then
    kubectl -n "$APP_NAMESPACE" delete secret -l "owner=helm,name=${svc}" >/dev/null
    ok "released ${svc} from Helm"
  fi
done

step "Creating the Argo CD project and applications"
tmp="$(mktemp -d)"
for f in "${ROOT_DIR}"/deploy/argocd/*.yaml; do
  sed "s#REPO_URL#${REPO_URL}#g" "$f" > "${tmp}/$(basename "$f")"
done
kubectl apply -f "${tmp}/project.yaml"
kubectl apply -f "$tmp"
rm -rf "$tmp"
ok "Applications created"

password="$(kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' 2>/dev/null | base64 --decode || true)"
step "GitOps is on"
info "Watch the sync:  kubectl -n argocd get applications -w"
info "Argo CD UI:      ./scripts/08-open-dashboards.sh  then open http://localhost:8080"
info "Login:           admin / ${password:-<run: kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 --decode>}"
info ""
info "From now on, deploy by pushing to Git. See docs/03-CICD-GITOPS.md."
