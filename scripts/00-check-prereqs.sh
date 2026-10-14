#!/usr/bin/env bash
# Step 0 — Check that every tool is installed and AWS credentials work.
# Usage: ./scripts/00-check-prereqs.sh
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

missing=0
check() {
  local cmd="$1" hint="$2" version_cmd="$3"
  if command -v "$cmd" >/dev/null 2>&1; then
    ok "$(printf '%-10s' "$cmd") $(eval "$version_cmd" 2>/dev/null | head -1)"
  else
    warn "$(printf '%-10s' "$cmd") NOT FOUND -> ${hint}"
    missing=1
  fi
}

step "Checking required tools"
check aws       "https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html  (mac: brew install awscli)" "aws --version"
check terraform "https://developer.hashicorp.com/terraform/install  (mac: brew tap hashicorp/tap && brew install hashicorp/tap/terraform)" "terraform version"
check kubectl   "https://kubernetes.io/docs/tasks/tools/  (mac: brew install kubectl)" "kubectl version --client"
check helm      "https://helm.sh/docs/intro/install/  (mac: brew install helm)" "helm version --short"
check docker    "https://docs.docker.com/get-docker/  (Docker Desktop on mac/windows)" "docker --version"
check git       "https://git-scm.com/downloads" "git --version"
check jq        "https://jqlang.org/download/  (mac: brew install jq)" "jq --version"

[ "$missing" = "0" ] || die "Install the missing tools above, then run this script again."

step "Checking Docker is running"
if docker info >/dev/null 2>&1; then
  ok "Docker daemon is running"
  if docker buildx version >/dev/null 2>&1; then
    ok "docker buildx available"
  else
    warn "docker buildx missing (needed for cross-platform builds). Update Docker Desktop."
  fi
else
  die "Docker is installed but not running. Start Docker Desktop and retry."
fi

step "Checking AWS credentials"
if identity="$(aws sts get-caller-identity --output json 2>/dev/null)"; then
  ok "Account: $(echo "$identity" | jq -r .Account)"
  ok "Identity: $(echo "$identity" | jq -r .Arn)"
  region="$(aws configure get region 2>/dev/null || true)"
  info "Default CLI region: ${region:-not set} (Terraform uses the 'region' variable, default us-east-1)"
else
  die "AWS credentials are not configured. Run 'aws configure' (or 'aws configure sso') first. See docs/02-DEPLOYMENT-GUIDE.md, Step 1."
fi

step "All good!"
info "Next: ./scripts/01-create-infrastructure.sh"
