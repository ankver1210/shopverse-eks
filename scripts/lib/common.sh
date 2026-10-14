#!/usr/bin/env bash
# Shared helpers for every script in scripts/. Compatible with macOS's bash 3.2.
# shellcheck disable=SC2034  # variables here are used by the scripts that source this file

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TF_DIR="${ROOT_DIR}/infra/terraform"
HELM_CHART="${ROOT_DIR}/deploy/helm/microservice"
VALUES_DIR="${ROOT_DIR}/deploy/helm/values"
K8S_DIR="${ROOT_DIR}/deploy/k8s"
PLATFORM_DIR="${ROOT_DIR}/deploy/platform"
APP_NAMESPACE="shopverse"
DATA_NAMESPACE="shopverse-data"

SERVICES=(
  frontend
  api-gateway
  user-service
  product-service
  cart-service
  order-service
  inventory-service
  payment-service
  recommendation-service
  notification-service
)

# ------------------------------------------------------------------ output
if [ -t 1 ]; then
  BOLD=$'\033[1m'; DIM=$'\033[2m'; RED=$'\033[31m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; BLUE=$'\033[34m'; RESET=$'\033[0m'
else
  BOLD=""; DIM=""; RED=""; GREEN=""; YELLOW=""; BLUE=""; RESET=""
fi

step()  { printf '\n%s==> %s%s\n' "${BOLD}${BLUE}" "$*" "${RESET}"; }
info()  { printf '    %s\n' "$*"; }
ok()    { printf '    %s✔ %s%s\n' "${GREEN}" "$*" "${RESET}"; }
warn()  { printf '    %s⚠ %s%s\n' "${YELLOW}" "$*" "${RESET}"; }
die()   { printf '\n%s✘ %s%s\n\n' "${RED}${BOLD}" "$*" "${RESET}" >&2; exit 1; }

# Ask for confirmation unless AUTO_APPROVE=1
confirm() {
  if [ "${AUTO_APPROVE:-0}" = "1" ]; then return 0; fi
  printf '\n%s%s [y/N] %s' "${BOLD}" "$1" "${RESET}"
  read -r answer
  case "$answer" in
    y|Y|yes|YES) return 0 ;;
    *) die "Cancelled." ;;
  esac
}

require_cmd() {
  for cmd in "$@"; do
    command -v "$cmd" >/dev/null 2>&1 || die "'$cmd' is not installed. Run ./scripts/00-check-prereqs.sh for install help."
  done
}

# Portable in-place sed (GNU and macOS/BSD).
sed_inplace() {
  local expression="$1" file="$2"
  sed -i.bak -E "$expression" "$file" && rm -f "${file}.bak"
}

# ------------------------------------------------------------------ terraform outputs
tf_output() {
  terraform -chdir="$TF_DIR" output -raw "$1" 2>/dev/null
}

load_infra() {
  require_cmd terraform aws kubectl
  [ -d "${TF_DIR}/.terraform" ] || die "Infrastructure not found. Run ./scripts/01-create-infrastructure.sh first."
  REGION="$(tf_output region)" || die "Could not read Terraform outputs. Did 01-create-infrastructure.sh finish?"
  CLUSTER_NAME="$(tf_output cluster_name)"
  ACCOUNT_ID="$(tf_output account_id)"
  ECR_REGISTRY="$(tf_output ecr_registry)"
  VPC_ID="$(tf_output vpc_id)"
  export AWS_REGION="$REGION" AWS_DEFAULT_REGION="$REGION"
}

ensure_kubeconfig() {
  if ! kubectl config current-context 2>/dev/null | grep -q "$CLUSTER_NAME"; then
    info "Pointing kubectl at cluster ${CLUSTER_NAME}..."
    aws eks update-kubeconfig --region "$REGION" --name "$CLUSTER_NAME" >/dev/null
  fi
  kubectl get nodes >/dev/null 2>&1 || die "kubectl cannot reach the cluster. Check your AWS credentials (aws sts get-caller-identity)."
}

load_versions() {
  # shellcheck source=../../deploy/platform/versions.env
  . "${PLATFORM_DIR}/versions.env"
}

# Read image.tag from a service values file
service_tag() {
  sed -n -E 's/^  tag: "?([^"]+)"?.*/\1/p' "${VALUES_DIR}/$1.yaml" | head -1
}

is_valid_service() {
  local candidate="$1" s
  for s in "${SERVICES[@]}"; do [ "$s" = "$candidate" ] && return 0; done
  return 1
}
