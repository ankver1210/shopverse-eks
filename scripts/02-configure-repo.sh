#!/usr/bin/env bash
# Step 2 — Write your AWS account/region into the config files (image registry, secret store).
# Safe to run more than once. Usage: ./scripts/02-configure-repo.sh
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
load_infra

step "Configuring the repository for account ${ACCOUNT_ID} in ${REGION}"

sed_inplace "s#^(  imageRegistry: ).*#\1\"${ECR_REGISTRY}\"#" "${VALUES_DIR}/common.yaml"
ok "deploy/helm/values/common.yaml -> imageRegistry: ${ECR_REGISTRY}"

sed_inplace "s#^(      region: )[^ ]+(.*)#\1${REGION}\2#" "${K8S_DIR}/platform/cluster-secret-store.yaml"
ok "deploy/k8s/platform/cluster-secret-store.yaml -> region: ${REGION}"

github_role="$(tf_output github_actions_role_arn || true)"
step "Done"
if [ -n "$github_role" ]; then
  info "For CI/CD, add these GitHub repository variables (Settings > Secrets and variables > Actions > Variables):"
  info "  AWS_REGION   = ${REGION}"
  info "  AWS_ROLE_ARN = ${github_role}"
fi
info "If you use Git, commit these changes:  git add -A && git commit -m 'Configure for my AWS account'"
info "Next: ./scripts/03-install-platform.sh"
